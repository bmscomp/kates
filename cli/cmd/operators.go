package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/strimzi"
	"github.com/spf13/cobra"
)

// kates operators — every Strimzi Cluster Operator on the cluster, discovered
// from its Deployment (multi-version plan §3.4): under cluster scope one line,
// under namespace scope the map of who runs what.

var operatorsCmd = &cobra.Command{
	Use:   "operators",
	Short: "Strimzi operators on this cluster: version, scope, watched namespaces, Kafka window",
}

var operatorsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every Strimzi Cluster Operator and its role",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runOperatorsList(cmd.Context())
	},
}

func init() {
	operatorsListCmd.Flags().StringVar(&deployKafkaNS, "kafka-ns", "kafka", "The primary's namespace (decides which operator is the primary's)")
	operatorsCmd.AddCommand(operatorsListCmd)
	rootCmd.AddCommand(operatorsCmd)
}

// operatorRow is one operator as `kates operators list` prints it.
type operatorRow struct {
	Namespace string   `json:"namespace"`
	Version   string   `json:"version"`
	Scope     string   `json:"scope"`
	Watches   []string `json:"watches"`
	Window    []string `json:"window"`
	Role      string   `json:"role"`
	Release   string   `json:"release,omitempty"`
	Shape     string   `json:"shape"`
}

func runOperatorsList(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ops, err := strimzi.InstalledOperators(ctx, defaultRunner)
	if err != nil {
		return fmt.Errorf("listing operators: %w", err)
	}
	pin, _ := pinnedStrimziVersion(".")
	shape := strimzi.Shape(ops)
	primary := primaryOperator(ops, deployKafkaNS)
	rows := make([]operatorRow, 0, len(ops))
	for _, op := range ops {
		role := "additional"
		switch {
		case primary != nil && op.Namespace == primary.Namespace && op.Name == primary.Name:
			role = "primary — owns CRDs and cluster RBAC"
		case op.IsForeign():
			role = "foreign (not installed by kates)"
		}
		if primary != nil && role == "additional" {
			if pv, err1 := parseStrimziRelease(primary.Version); err1 == nil {
				if cv, err2 := parseStrimziRelease(op.Version); err2 == nil {
					switch {
					case cv.major == pv.major && pv.minor-cv.minor <= 1 && pv.minor >= cv.minor:
						role = "additional (adjacent to primary)"
					case cv.major == pv.major && cv.minor > pv.minor:
						role = "additional — NEWER than the primary's operator"
					default:
						role = "additional (non-adjacent — untested by Strimzi)"
					}
				}
			}
		}
		if pin != "" && op.Version != pin && strings.HasPrefix(role, "primary") {
			role += fmt.Sprintf(" (pin: %s)", pin)
		}
		rows = append(rows, operatorRow{
			Namespace: op.Namespace, Version: op.Version, Scope: op.Scope, Watches: op.Watches,
			Window: op.Window.Strings(), Role: role, Release: op.Release, Shape: string(shape),
		})
	}

	if outputMode == "json" {
		output.JSON(rows)
		return nil
	}
	if len(rows) == 0 {
		output.Warn("No Strimzi Cluster Operator found on this cluster (kates deploy installs one).")
		return nil
	}
	output.Header(fmt.Sprintf("Strimzi operators (%s)", shapeLabel(shape)))
	var table [][]string
	for _, r := range rows {
		table = append(table, []string{r.Namespace, r.Version, r.Scope, strings.Join(r.Watches, ","), strings.Join(r.Window, " "), r.Role})
	}
	output.Table([]string{"NAMESPACE", "VERSION", "SCOPE", "WATCHES", "KAFKA WINDOW", "ROLE"}, table)
	if shape == strimzi.ShapeMixed {
		output.Warn("A cluster-wide and a namespace-scoped operator coexist: they reconcile the same namespaces. Remove one before deploying.")
	}
	return nil
}

func shapeLabel(s strimzi.ScopeShape) string {
	switch s {
	case strimzi.ShapeCluster:
		return "cluster scope: one operator watching every namespace"
	case strimzi.ShapeNamespaces:
		return "namespace scope: one operator per Kafka namespace"
	case strimzi.ShapeMixed:
		return "MIXED — cluster-wide and namespaced operators together"
	}
	return "none installed"
}

type strimziRelease struct{ major, minor int }

func parseStrimziRelease(v string) (strimziRelease, error) {
	var r strimziRelease
	_, err := fmt.Sscanf(v, "%d.%d", &r.major, &r.minor)
	return r, err
}
