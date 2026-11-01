package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// .policies records what each cluster's Kyverno policies need from a Kates
// install, one environment per kube context: the labels of the namespaces
// deploy creates, the labels of every pod the charts render, and chart
// values. kates kyverno check --write-policies fills in the current
// context's; deploy and the check apply the entry of the context they target.
// It is git-ignored: it names teams, cost centres and registries.
const defaultPoliciesFile = ".policies"

// policiesFilePath is --policies-file. Empty reads ./.policies when it exists.
var policiesFilePath string

// policiesDoc is the .policies file: an environment per kube context.
type policiesDoc struct {
	Environments map[string]deployPolicies `yaml:"environments"`
}

// deployPolicies is one environment of the .policies file.
type deployPolicies struct {
	NamespaceLabels map[string]string `yaml:"namespaceLabels,omitempty"`
	PodLabels       map[string]string `yaml:"podLabels,omitempty"`
	// Values are chart values by chart name, passed to that chart after deploy's own files.
	Values map[string]map[string]any `yaml:"values,omitempty"`
	// Unresolved are the rules the last check could not fix with a value it knew.
	Unresolved []policyGap `yaml:"unresolved,omitempty"`
}

// policyGap is a rule that still fails, and what would fix it.
type policyGap struct {
	Chart     string `yaml:"chart"`
	Namespace string `yaml:"namespace"`
	Policy    string `yaml:"policy"`
	Rule      string `yaml:"rule"`
	Message   string `yaml:"message"`
	Fix       string `yaml:"fix,omitempty"`
}

// deployCharts are the charts kates deploy installs from this repository.
var deployCharts = []string{"strimzi-operator", "monitoring", "cdc-postgresql", "kafka-cluster", "connect-cluster",
	"apicurio-registry", "kates", "kafka-ui", "mirror-maker2", "kates-chaos"}

// chartPodLabelKeys are the values whose maps label every pod the chart
// renders: podLabels, and a wrapped subchart's own. They equal label_args in
// scripts/check-chart-policies.sh, which proves in CI that they reach every pod.
func chartPodLabelKeys(chart string) []string {
	const kps = "kube-prometheus-stack"
	switch chart {
	case "strimzi-operator":
		return []string{"podLabels", "strimzi-kafka-operator.labels"}
	case "monitoring":
		return []string{"podLabels", kps + ".commonLabels", kps + ".grafana.podLabels",
			kps + ".prometheusOperator.podLabels", kps + ".kube-state-metrics.podLabels", kps + ".prometheus-node-exporter.podLabels"}
	case "kates-chaos":
		return []string{"podLabels", "litmus-core.customLabels"}
	}
	return []string{"podLabels"}
}

func addPoliciesFileFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&policiesFilePath, "policies-file", "", "File of the labels and chart values the cluster's Kyverno policies need (default: ./.policies, when it exists)")
}

// policiesFile is the .policies path, and whether it must exist.
func policiesFile() (string, bool) {
	if policiesFilePath != "" {
		return policiesFilePath, true
	}
	return defaultPoliciesFile, false
}

// readPoliciesDoc reads and validates the whole .policies file; a missing
// default file is an empty one.
func readPoliciesDoc() (policiesDoc, error) {
	var doc policiesDoc
	path, required := policiesFile()
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist) && !required:
		return doc, nil
	case err != nil:
		return doc, fmt.Errorf("policies file: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		return doc, fmt.Errorf("%s: %w", path, err)
	}
	for _, env := range slices.Sorted(maps.Keys(doc.Environments)) {
		p := doc.Environments[env]
		where := path + ": environments." + env
		for _, labels := range []struct {
			name string
			m    map[string]string
		}{{"namespaceLabels", p.NamespaceLabels}, {"podLabels", p.PodLabels}} {
			for _, k := range slices.Sorted(maps.Keys(labels.m)) {
				if err := validateNamespaceLabel(k, labels.m[k]); err != nil {
					return doc, fmt.Errorf("%s.%s: %w", where, labels.name, err)
				}
			}
		}
		for chart := range p.Values {
			if !slices.Contains(deployCharts, chart) {
				return doc, fmt.Errorf("%s.values.%s: not a chart kates deploy installs (%s)", where, chart, strings.Join(deployCharts, ", "))
			}
		}
	}
	return doc, nil
}

// readDeployPolicies is the environment of kube context env in .policies.
func readDeployPolicies(env string) (deployPolicies, error) {
	doc, err := readPoliciesDoc()
	return doc.Environments[env], err
}

// chartValues is what .policies sets in chart: its values, with the pod
// labels under every label key the values do not already set.
func (p deployPolicies) chartValues(chart string) map[string]any {
	values := deepCopyValues(p.Values[chart])
	if len(p.PodLabels) == 0 {
		return values
	}
	if values == nil {
		values = map[string]any{}
	}
	for _, key := range chartPodLabelKeys(chart) {
		m := values
		for _, part := range strings.Split(key, ".") {
			next, ok := m[part].(map[string]any)
			if !ok {
				next = map[string]any{}
				m[part] = next
			}
			m = next
		}
		for k, v := range p.PodLabels {
			if _, set := m[k]; !set {
				m[k] = v
			}
		}
	}
	return values
}

// writeChartValues writes chartValues(chart) into dir and returns the -f
// arguments that pass it, or none when .policies sets nothing there.
func (p deployPolicies) writeChartValues(dir, chart string) ([]string, error) {
	values := p.chartValues(chart)
	if len(values) == 0 {
		return nil, nil
	}
	b, err := yaml.Marshal(values)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, chart+"-policies.yaml")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, err
	}
	return []string{"-f", path}, nil
}

// writeDeployPolicyValues writes, next to deploy's values file, a values
// file for each chart .policies sets something in, and returns the -f
// arguments by chart. It runs before anything installs: a chart installed
// without them is what admission refuses.
func writeDeployPolicyValues(p deployPolicies, valuesFile string) (map[string][]string, error) {
	args := map[string][]string{}
	for _, chart := range deployCharts {
		a, err := p.writeChartValues(filepath.Dir(valuesFile), chart)
		if err != nil {
			return nil, fmt.Errorf("policies values for %s: %w", chart, err)
		}
		if a != nil {
			args[chart] = a
		}
	}
	return args, nil
}

// policyArgs is the -f that passes .policies' values for chart.
func (dc *deployContext) policyArgs(chart string) []string {
	return dc.policyValues[chart]
}

// warnUnresolvedPolicies says which rules the last check left unfixed.
func warnUnresolvedPolicies(p deployPolicies) {
	if len(p.Unresolved) == 0 {
		return
	}
	path, _ := policiesFile()
	output.Warn(fmt.Sprintf("%s lists %d Kyverno rule(s) for this cluster that no value fixes yet; admission may refuse what they cover:", path, len(p.Unresolved)))
	for _, g := range p.Unresolved {
		output.Hint(fmt.Sprintf("  %s in %s: %s/%s", g.Chart, g.Namespace, g.Policy, g.Rule))
	}
}

// saveDeployPolicies writes p as environment env of the .policies file,
// keeping the others, readable by its owner only, and warns when git tracks
// the file.
func saveDeployPolicies(env string, p deployPolicies) (string, error) {
	path, _ := policiesFile()
	doc, err := readPoliciesDoc()
	if err != nil {
		return "", err
	}
	if doc.Environments == nil {
		doc.Environments = map[string]deployPolicies{}
	}
	doc.Environments[env] = p
	b, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	header := "# Written by kates kyverno check --write-policies, and rewritten on each run.\n" +
		"# Fill in the labels it could not know and the values under unresolved, then check again.\n" +
		"# Keep this file out of git: it describes one cluster.\n"
	if err := os.WriteFile(path, append([]byte(header), b...), 0o600); err != nil {
		return "", err
	}
	// WriteFile keeps an existing file's mode.
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	if err := exec.Command("git", "ls-files", "--error-unmatch", path).Run(); err == nil {
		output.Warn(path + " is tracked by git: remove it (git rm --cached " + path + ") and add it to .gitignore")
	}
	return path, nil
}

func deepCopyValues(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			v = deepCopyValues(sub)
		}
		out[k] = v
	}
	return out
}
