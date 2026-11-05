package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/internal/kubectl"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var rightsCmd = &cobra.Command{
	Use:   "rights",
	Short: "Show what each Kates backend on this cluster may do on Kubernetes",
	Long: `Asks the API server what the service account of each Kates deployment on the
current kube context may do in each namespace, and prints the namespaces that
hold the same rights together.

The rights are the effective ones, whichever bindings grant them. Left out are
what every service account gets: the self-review resources and the non-resource
URLs such as /healthz.

It impersonates the service account, so the account running it needs the
impersonate verb on it; a refusal is an error, never an empty list.`,
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if outputMode == "json" {
			return noJSONForm(cmd)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return runRights(ctx)
	},
}

func init() {
	rootCmd.AddCommand(rightsCmd)
}

type deploymentList struct {
	Items []struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec struct {
			Template struct {
				Spec struct {
					ServiceAccountName string `json:"serviceAccountName"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	} `json:"items"`
}

type rulesReview struct {
	Status struct {
		ResourceRules []struct {
			Verbs         []string `json:"verbs"`
			APIGroups     []string `json:"apiGroups"`
			Resources     []string `json:"resources"`
			ResourceNames []string `json:"resourceNames"`
		} `json:"resourceRules"`
		Incomplete      bool   `json:"incomplete"`
		EvaluationError string `json:"evaluationError"`
	} `json:"status"`
}

func runRights(ctx context.Context) error {
	kc := newKubectl("")
	const selector = "app.kubernetes.io/name=kates"

	var deployments deploymentList
	if err := kc.JSON(ctx, &deployments, "get", "deployment", "-A", "-l", selector, "-o", "json"); err != nil {
		// An admin-less account has no cluster-wide list: the kube context's namespace is what it has.
		output.Warn("Cannot list deployments in every namespace, looking in the kube context's namespace only")
		if err := kc.JSON(ctx, &deployments, "get", "deployment", "-l", selector, "-o", "json"); err != nil {
			return cmdErr("Could not list the Kates deployments: " + err.Error())
		}
	}
	kubeContext, _ := kc.Run(ctx, "config", "current-context")
	kubeContext = strings.TrimSpace(kubeContext)
	if len(deployments.Items) == 0 {
		return cmdErr("No Kates deployment on " + kubeContext)
	}

	all, nsErr := kc.Run(ctx, "get", "namespace", "-o", "jsonpath={.items[*].metadata.name}")
	namespaces := strings.TrimSpace(all)
	if nsErr != nil {
		output.Warn("Cannot list namespaces, showing only those of the Kates deployments")
		namespaces = ""
		for _, d := range deployments.Items {
			namespaces += " " + d.Metadata.Namespace
		}
	}
	names := slices.Compact(slices.Sorted(slices.Values(strings.Fields(namespaces))))

	output.Header("Kates rights on " + kubeContext)
	for _, d := range deployments.Items {
		account := d.Spec.Template.Spec.ServiceAccountName
		if account == "" {
			account = "default"
		}
		user := "system:serviceaccount:" + d.Metadata.Namespace + ":" + account
		fmt.Printf("\n  Deployment %s/%s runs as service account %s/%s\n", d.Metadata.Namespace, d.Metadata.Name, d.Metadata.Namespace, account)
		if err := printRights(ctx, kc, user, names); err != nil {
			return err
		}
	}
	return nil
}

// printRights prints one table per set of rights, each under the namespaces
// holding it.
func printRights(ctx context.Context, kc *kubectl.Client, user string, namespaces []string) error {
	var order []string
	tables := map[string][][]string{}
	holders := map[string][]string{}
	var none []string
	for _, ns := range namespaces {
		review, err := reviewRules(ctx, user, ns)
		if err != nil {
			output.Hint("Reading another account's rights takes the impersonate verb on it.")
			return cmdErr(fmt.Sprintf("Could not read the rights of %s in %s: %v", user, ns, err))
		}
		if review.Status.Incomplete {
			output.Warn(fmt.Sprintf("The API server's answer for %s is incomplete, so the rights there may be wider: %s", ns, review.Status.EvaluationError))
		}
		rows := rightsRows(review)
		if len(rows) == 0 {
			none = append(none, ns)
			continue
		}
		key := fmt.Sprint(rows)
		if _, seen := tables[key]; !seen {
			order = append(order, key)
			tables[key] = rows
		}
		holders[key] = append(holders[key], ns)
	}
	for _, key := range order {
		fmt.Printf("\n  In %s:\n", strings.Join(holders[key], ", "))
		output.Table([]string{"Resource", "Verbs", "Only names"}, tables[key])
	}
	if len(order) == 0 {
		output.Warn("No rights beyond what every service account gets")
	}
	if len(none) > 0 {
		fmt.Printf("\n  Nothing in %s\n", strings.Join(none, ", "))
	}
	return nil
}

// reviewRules asks as user, with a SelfSubjectRulesReview: kubectl auth can-i
// --list has the same answer but only as text.
func reviewRules(ctx context.Context, user, namespace string) (*rulesReview, error) {
	body := fmt.Sprintf(`{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectRulesReview","spec":{"namespace":%q}}`, namespace)
	// --validate=false: validating reads CRDs, which the account need not be allowed to.
	out, err := commandRunner.OutputStdin(ctx, body, "kubectl", "create", "--validate=false", "-f", "-", "-o", "json", "--as="+user)
	if err != nil {
		return nil, err
	}
	var review rulesReview
	if err := json.Unmarshal(out, &review); err != nil {
		return nil, fmt.Errorf("reading the review: %w", err)
	}
	return &review, nil
}

// rightsRows flattens a review to one sorted row per resource, verbs merged
// across the rules, leaving out what every service account gets.
func rightsRows(review *rulesReview) [][]string {
	type key struct{ resource, names string }
	verbs := map[key][]string{}
	for _, rule := range review.Status.ResourceRules {
		for _, group := range rule.APIGroups {
			for _, resource := range rule.Resources {
				if strings.HasPrefix(resource, "selfsubject") {
					continue
				}
				if group != "" {
					resource += "." + group
				}
				k := key{resource, strings.Join(rule.ResourceNames, ", ")}
				verbs[k] = append(verbs[k], rule.Verbs...)
			}
		}
	}
	rows := make([][]string, 0, len(verbs))
	for k, v := range verbs {
		rows = append(rows, []string{k.resource, strings.Join(slices.Compact(slices.Sorted(slices.Values(v))), ", "), k.names})
	}
	slices.SortFunc(rows, func(a, b []string) int { return strings.Compare(a[0]+a[2], b[0]+b[2]) })
	return rows
}
