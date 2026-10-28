package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/bmscomp/kates/cli/internal/proc"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	kyvernoCheckNamespace string
	kyvernoCheckValues    []string
	kyvernoCheckSet       []string
)

// kyvernoPathFn finds the Kyverno CLI, which evaluates the policies offline.
var kyvernoPathFn = func() (string, error) { return exec.LookPath("kyverno") }

var kyvernoCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Hold the kates chart, as you would install it, to the cluster's Kyverno policies",
	Long: `Reads the Kyverno policies and policy exceptions the cluster holds, renders the
kates chart with helm template and the values you pass, and evaluates the render
with the Kyverno CLI (kyverno apply), before anything is installed. Each failed
rule is listed with the chart value most likely to fix it, and the
kyvernoPolicy.exceptions entry that would exempt the release from it.

It reads ClusterPolicies, ValidatingPolicies and ImageValidatingPolicies, the
namespace's Policies, and PolicyExceptions in every namespace (the release's
only, when the account may not list the others). A kind the cluster does not
have is skipped; one the account may not read is named, and the check cannot
pass without it. Audit policies show as warnings: they do not block an install.
Rules that call the API or read ConfigMaps evaluate offline without that data.

Needs helm and the kyverno CLI on PATH. Exit 0 when nothing fails, 1 when a
rule fails, 2 when the check could not run or could not read every policy.`,
	Example: `  kates kyverno check -n team -f team-values.yaml
  kates kyverno check -n team --set podLabels.team=payments
  kates kyverno check -n team -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		ns := kyvernoCheckNamespace
		if ns == "" {
			ns = resolveNamespaces().app
		}
		kyvernoBin, err := kyvernoPathFn()
		if err != nil {
			return couldNotTell("the kyverno CLI is not on PATH: install it (brew install kyverno, or https://kyverno.io/docs/kyverno-cli/install/)")
		}
		if err := resolveChartRoot(ctx, "", false, "kates"); err != nil {
			return couldNotTell(err.Error())
		}

		read, err := readKyvernoPolicies(ctx, ns)
		if err != nil {
			return couldNotTell(err.Error())
		}
		report := kyvernoCheckReport{Namespace: ns, Unread: read.unread}
		if len(read.policies) == 0 {
			return finishKyvernoCheck(report)
		}

		render, err := renderKatesChart(ctx, ns)
		if err != nil {
			return couldNotTell(err.Error())
		}
		dir, err := os.MkdirTemp("", "kates-kyverno-check-")
		if err != nil {
			return couldNotTell(err.Error())
		}
		defer os.RemoveAll(dir)
		files := map[string][]byte{
			"policies.yaml":   joinDocuments(read.policies),
			"exceptions.yaml": joinDocuments(read.exceptions),
			"kates.yaml":      render,
		}
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
				return couldNotTell(err.Error())
			}
		}
		kyvernoArgs := []string{"apply", filepath.Join(dir, "policies.yaml"),
			"--resource", filepath.Join(dir, "kates.yaml"),
			// The chart's own PolicyException (kyvernoPolicy.exceptions) is in the render.
			"--exceptions-within-resources",
			"--audit-warn", "--policy-report", "--output-format", "json"}
		if len(read.exceptions) > 0 {
			kyvernoArgs = append(kyvernoArgs, "--exception", filepath.Join(dir, "exceptions.yaml"))
		}
		// kyverno exits 1 when a rule fails; the report says which.
		out, _ := commandRunner.Combined(ctx, kyvernoBin, kyvernoArgs...)
		results, err := parseKyvernoReport(out)
		if err != nil {
			return couldNotTell("kyverno apply: " + err.Error())
		}
		report.add(results)
		return finishKyvernoCheck(report)
	},
}

// kyvernoRead is what the check read from the cluster: each policy and
// exception as one JSON object, and the kinds it was refused.
type kyvernoRead struct {
	policies, exceptions [][]byte
	unread               []string
}

// readKyvernoPolicies reads every policy that can apply to a release in ns,
// and every exception. A kind the cluster lacks is skipped.
func readKyvernoPolicies(ctx context.Context, ns string) (kyvernoRead, error) {
	var read kyvernoRead
	for _, s := range []struct {
		resource  string
		scope     string // "", "namespace" (ns only) or "all" (-A, else ns)
		exception bool
	}{
		{"clusterpolicies.kyverno.io", "", false},
		{"validatingpolicies.policies.kyverno.io", "", false},
		{"imagevalidatingpolicies.policies.kyverno.io", "", false},
		{"policies.kyverno.io", "namespace", false},
		{"policyexceptions.kyverno.io", "all", true},
		{"policyexceptions.policies.kyverno.io", "all", true},
	} {
		items, unread, err := listKyvernoKind(ctx, s.resource, s.scope, ns)
		if err != nil {
			return read, err
		}
		if unread != "" {
			read.unread = append(read.unread, unread)
		}
		if s.exception {
			read.exceptions = append(read.exceptions, items...)
		} else {
			read.policies = append(read.policies, items...)
		}
	}
	return read, nil
}

// listKyvernoKind lists one kind's objects. A refused list of every
// namespace falls back to ns; a refusal there is returned as unread.
func listKyvernoKind(ctx context.Context, resource, scope, ns string) ([][]byte, string, error) {
	where := []string{}
	switch scope {
	case "namespace":
		where = []string{"-n", ns}
	case "all":
		where = []string{"-A"}
	}
	kc := newKubectl("")
	out, err := kc.Output(ctx, append([]string{"get", resource, "-o", "json"}, where...)...)
	if err != nil && scope == "all" && strings.Contains(strings.ToLower(proc.Stderr(err)), "forbidden") {
		where = []string{"-n", ns}
		out, err = kc.Output(ctx, append([]string{"get", resource, "-o", "json"}, where...)...)
	}
	if err != nil {
		stderr := proc.Stderr(err)
		switch {
		case strings.Contains(stderr, "doesn't have a resource type"):
			return nil, "", nil
		case strings.Contains(strings.ToLower(stderr), "forbidden"):
			return nil, strings.TrimSpace(resource + " " + strings.Join(where, " ")), nil
		}
		return nil, "", fmt.Errorf("kubectl get %s: %s", resource, strings.TrimSpace(stderr+" "+err.Error()))
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, "", fmt.Errorf("kubectl get %s: %w", resource, err)
	}
	items := make([][]byte, len(list.Items))
	for i, item := range list.Items {
		items[i] = item
	}
	return items, "", nil
}

// renderKatesChart renders the kates chart as it would be installed in ns.
// kyverno.io/v2 lets the chart render its own PolicyException, as on a
// cluster that has Kyverno.
func renderKatesChart(ctx context.Context, ns string) ([]byte, error) {
	args := []string{"template", "kates", chartRootPath("charts/kates"), "-n", ns, "--api-versions", "kyverno.io/v2"}
	for _, f := range kyvernoCheckValues {
		args = append(args, "-f", f)
	}
	for _, s := range kyvernoCheckSet {
		args = append(args, "--set", s)
	}
	out, err := commandRunner.Output(ctx, "helm", args...)
	if err != nil {
		return nil, fmt.Errorf("helm template: %s", strings.TrimSpace(proc.Stderr(err)+" "+err.Error()))
	}
	return inNamespace(out, ns)
}

// inNamespace sets metadata.namespace on each namespaced object helm template
// left without one, as helm install would: offline, a policy or exception
// that matches by namespace sees nothing else.
func inNamespace(render []byte, ns string) ([]byte, error) {
	dec := yaml.NewDecoder(bytes.NewReader(render))
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("helm template output: %w", err)
		}
		if doc == nil {
			continue
		}
		kind, _ := doc["kind"].(string)
		if meta, ok := doc["metadata"].(map[string]any); ok && meta["namespace"] == nil && !clusterScoped(kind) {
			meta["namespace"] = ns
		}
		if err := enc.Encode(doc); err != nil {
			return nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// clusterScoped is a kind with no namespace.
// shortcut: by name, not discovery; add a kind here if the chart renders a new cluster-scoped one.
func clusterScoped(kind string) bool {
	return strings.HasPrefix(kind, "Cluster") || slices.Contains([]string{"Namespace", "PriorityClass", "StorageClass",
		"CustomResourceDefinition", "ValidatingWebhookConfiguration", "MutatingWebhookConfiguration",
		"ValidatingPolicy", "ImageValidatingPolicy"}, kind)
}

// joinDocuments is a multi-document YAML stream of JSON objects: the Kyverno
// CLI reads exceptions only one per document, not as a List.
func joinDocuments(items [][]byte) []byte {
	return bytes.Join(items, []byte("\n---\n"))
}

// kyvernoResult is one rule's result on one resource, as kyverno apply
// --policy-report gives it (an openreports.io ClusterReport, or a
// wgpolicyk8s.io one from older CLIs: the results are the same shape).
type kyvernoResult struct {
	Policy    string `json:"policy"`
	Rule      string `json:"rule"`
	Result    string `json:"result"`
	Message   string `json:"message"`
	Resources []struct {
		Kind      string `json:"kind"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	} `json:"resources"`
}

// parseKyvernoReport reads the report from kyverno's output, where warnings
// about deprecated kinds come first, one per line.
func parseKyvernoReport(out []byte) ([]kyvernoResult, error) {
	start := bytes.Index(out, []byte("\n{"))
	if bytes.HasPrefix(out, []byte("{")) {
		start = -1
	} else if start < 0 {
		return nil, fmt.Errorf("no policy report in its output: %s", strings.TrimSpace(string(out)))
	}
	var report struct {
		Results []kyvernoResult `json:"results"`
	}
	if err := json.NewDecoder(bytes.NewReader(out[start+1:])).Decode(&report); err != nil {
		return nil, err
	}
	return report.Results, nil
}

// kyvernoCheckReport is what the check prints, and its -o json form.
type kyvernoCheckReport struct {
	Namespace  string               `json:"namespace"`
	Unread     []string             `json:"unread,omitempty"`
	Pass       int                  `json:"pass"`
	Skip       int                  `json:"skip"`
	Findings   []kyvernoFinding     `json:"findings"`
	Exceptions []kyvernoChartExcept `json:"exceptions,omitempty"`
}

// kyvernoFinding is a rule that did not pass, on the resources it failed.
type kyvernoFinding struct {
	Result    string   `json:"result"` // fail, error or warn
	Policy    string   `json:"policy"`
	Rule      string   `json:"rule"`
	Message   string   `json:"message"`
	Resources []string `json:"resources"`
	Fix       string   `json:"fix,omitempty"`
}

// kyvernoChartExcept is a kyvernoPolicy.exceptions entry of the kates chart.
type kyvernoChartExcept struct {
	PolicyName string   `json:"policyName"`
	RuleNames  []string `json:"ruleNames"`
}

// add folds kyverno's results in: one finding per policy, rule, result and
// message, naming each resource it failed on, and one exception entry per
// policy that failed or errored.
func (r *kyvernoCheckReport) add(results []kyvernoResult) {
	byKey := map[string]int{}
	rules := map[string]map[string]bool{}
	for _, res := range results {
		switch res.Result {
		case "pass":
			r.Pass++
			continue
		case "skip":
			r.Skip++
			continue
		}
		key := res.Result + "\x00" + res.Policy + "\x00" + res.Rule + "\x00" + res.Message
		i, seen := byKey[key]
		if !seen {
			i = len(r.Findings)
			byKey[key] = i
			r.Findings = append(r.Findings, kyvernoFinding{
				Result: res.Result, Policy: res.Policy, Rule: res.Rule, Message: res.Message,
				Fix: kyvernoFix(res.Policy, res.Rule, res.Message),
			})
		}
		for _, o := range res.Resources {
			r.Findings[i].Resources = append(r.Findings[i].Resources, o.Kind+"/"+o.Name)
		}
		if res.Result != "warn" {
			if rules[res.Policy] == nil {
				rules[res.Policy] = map[string]bool{}
			}
			rules[res.Policy][baseRule(res.Rule)] = true
		}
	}
	for policy, names := range rules {
		e := kyvernoChartExcept{PolicyName: policy}
		for name := range names {
			e.RuleNames = append(e.RuleNames, name)
		}
		sort.Strings(e.RuleNames)
		r.Exceptions = append(r.Exceptions, e)
	}
	sort.Slice(r.Exceptions, func(i, j int) bool { return r.Exceptions[i].PolicyName < r.Exceptions[j].PolicyName })
}

// baseRule is the rule a policy declares: Kyverno generates autogen- and
// autogen-cronjob- copies for controllers, and the chart's exception adds
// both itself.
func baseRule(rule string) string {
	return strings.TrimPrefix(strings.TrimPrefix(rule, "autogen-cronjob-"), "autogen-")
}

var labelPath = regexp.MustCompile(`/metadata/labels/([^/]+)/`)

// kyvernoFix is the kates chart value most likely to satisfy a failed rule,
// read from its policy, rule and message; "" when none fits, and the
// exception is then the way.
func kyvernoFix(policy, rule, message string) string {
	text := strings.ToLower(policy + " " + rule + " " + message)
	// Policy and rule names spell keys with hyphens: run-as-non-root.
	squashed := strings.ReplaceAll(text, "-", "")
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(text, w) || strings.Contains(squashed, w) {
				return true
			}
		}
		return false
	}
	switch {
	case labelPath.MatchString(message):
		return "podLabels." + labelPath.FindStringSubmatch(message)[1] + "=<value>"
	case has("label"):
		return "podLabels.<key>=<value>"
	case has("pull secret", "pullsecret"):
		return "global.imagePullSecrets=[<secret>]"
	case has("latest", "image tag"):
		return "image.tag=<a pinned tag> (and the tag of the image the rule names)"
	case has("registry", "registries", "repositor", "trusted image", "allowed image"):
		return "global.imageRegistry=<the allowed registry>"
	case has("runasnonroot", "runasuser", "runasgroup", "privilege", "capabilit", "seccomp", "readonlyrootfilesystem", "securitycontext", "apparmor"):
		return "securityContext and containerSecurityContext (the chart's are restricted by default)"
	case has("request", "limit", "resources"):
		return "resources.requests and resources.limits (and the failing component's own resources values)"
	}
	return ""
}

// finishKyvernoCheck prints the report and ends with the exit code it calls
// for: 1 when a rule fails, else 2 when a rule errored or a kind could not
// be read, else 0.
func finishKyvernoCheck(r kyvernoCheckReport) error {
	failed, errored := 0, 0
	for _, f := range r.Findings {
		switch f.Result {
		case "fail":
			failed++
		case "error":
			errored++
		}
	}
	if r.Findings == nil {
		r.Findings = []kyvernoFinding{}
	}
	output.Render(outputMode == "json", r, func() { renderKyvernoCheck(r) })
	switch {
	case failed > 0:
		return &exitError{code: failedExitCode}
	case errored > 0:
		return &exitError{code: couldNotTellExitCode, msg: fmt.Sprintf("%d rule(s) errored: kyverno could not evaluate them", errored)}
	case len(r.Unread) > 0:
		return &exitError{code: couldNotTellExitCode, msg: "the account may not read " + strings.Join(r.Unread, ", ") + ": the policies there were not checked"}
	}
	return nil
}

func renderKyvernoCheck(r kyvernoCheckReport) {
	output.Banner("Kyverno Check", "the kates chart in "+r.Namespace+" against the cluster's policies")
	fmt.Println()
	for _, u := range r.Unread {
		output.Warn("Not read (forbidden): " + u)
	}
	if r.Pass+r.Skip+len(r.Findings) == 0 {
		output.Success("No Kyverno policy applies: the cluster has none the account can read")
		return
	}
	if len(r.Findings) == 0 {
		output.Success(fmt.Sprintf("Every rule passes (%d passed, %d skipped)", r.Pass, r.Skip))
		return
	}
	rows := make([][]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		fix := f.Fix
		if fix == "" {
			fix = "an exception (below)"
		}
		rows = append(rows, []string{strings.ToUpper(f.Result), f.Policy + "/" + f.Rule, strings.Join(f.Resources, ", "), f.Message, fix})
	}
	output.Table([]string{"Result", "Policy/Rule", "Resources", "Message", "Chart value"}, rows)
	fmt.Println()
	if len(r.Exceptions) > 0 {
		output.SubHeader("Or exempt the release")
		output.Hint("Values that render a PolicyException for the release's own pods (Kyverno must accept exceptions in " + r.Namespace + "):")
		fmt.Println("kyvernoPolicy:")
		fmt.Println("  exceptions:")
		for _, e := range r.Exceptions {
			fmt.Printf("    - policyName: %s\n      ruleNames: [%s]\n", e.PolicyName, strings.Join(e.RuleNames, ", "))
		}
		fmt.Println()
	}
	output.Hint(fmt.Sprintf("%d passed, %d skipped, %d did not pass", r.Pass, r.Skip, len(r.Findings)))
}

func init() {
	kyvernoCheckCmd.Flags().StringVarP(&kyvernoCheckNamespace, "namespace", "n", "", "Namespace the release would be installed in (default: the app namespace)")
	kyvernoCheckCmd.Flags().StringArrayVarP(&kyvernoCheckValues, "values", "f", nil, "Values file for the kates chart (repeatable)")
	kyvernoCheckCmd.Flags().StringArrayVar(&kyvernoCheckSet, "set", nil, "Value for the kates chart, as helm --set (repeatable)")
	kyvernoCmd.AddCommand(kyvernoCheckCmd)
}
