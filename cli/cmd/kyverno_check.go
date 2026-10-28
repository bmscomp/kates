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
	kyvernoCheckCharts    []string
	kyvernoCheckStack     bool
	kyvernoCheckWriteTo   string
	kyvernoCheckLabels    []string
)

// kyvernoPathFn finds the Kyverno CLI, which evaluates the policies offline.
var kyvernoPathFn = func() (string, error) { return exec.LookPath("kyverno") }

// kyvernoStackChart is a chart kates deploy installs, as the check renders it.
type kyvernoStackChart struct {
	name     string
	release  string // "" is the primary Kafka's name (--kafka-name)
	ns       func(resolvedNamespaces) string
	selected func() bool
	// deps is the helm dependency command deploy runs on the chart first.
	deps []string
	// values are files of the chart deploy always passes, under the chart root.
	values []string
	// labelKeys are the values whose maps label every pod the chart renders:
	// podLabels, and a wrapped subchart's own (scripts/check-chart-policies.sh label_args).
	labelKeys []string
	// exceptions: the chart renders kyvernoPolicy.exceptions as a PolicyException.
	exceptions bool
}

// kyvernoStackCharts are the charts kates deploy installs, in its order.
// shortcut: each renders with its defaults and deploy's fixed values files,
// not the values deploy derives from the cluster (domain, bootstrap, versions);
// add one here if a policy turns on it.
var kyvernoStackCharts = []kyvernoStackChart{
	{name: "strimzi-operator", release: "strimzi-operator", ns: func(resolvedNamespaces) string { return operatorNS },
		selected: func() bool { return deployWithStrimzi }, deps: []string{"build"},
		labelKeys: []string{"podLabels", "strimzi-kafka-operator.labels"}},
	{name: "monitoring", release: "monitoring", ns: func(n resolvedNamespaces) string { return n.jaeger },
		selected: func() bool { return deployWithMonitoring }, deps: []string{"update"},
		labelKeys: []string{"podLabels", "kube-prometheus-stack.commonLabels", "kube-prometheus-stack.grafana.podLabels",
			"kube-prometheus-stack.prometheusOperator.podLabels", "kube-prometheus-stack.kube-state-metrics.podLabels",
			"kube-prometheus-stack.prometheus-node-exporter.podLabels"}},
	{name: "kafka-cluster", ns: func(n resolvedNamespaces) string { return n.kafka },
		selected: func() bool { return true }, deps: []string{"update"},
		values: []string{"charts/kafka-cluster/values-platform.yaml"}, labelKeys: []string{"podLabels"}},
	{name: "connect-cluster", release: "connect-cluster", ns: func(n resolvedNamespaces) string { return n.connect },
		selected: func() bool { return deployWithKafkaConnect }, deps: []string{"update", "--skip-refresh"},
		labelKeys: []string{"podLabels"}},
	{name: "mirror-maker2", release: "mm2", ns: func(n resolvedNamespaces) string { return n.mm2 },
		selected: func() bool { return deployWithMirrorMaker2 }, deps: []string{"update", "--skip-refresh"},
		labelKeys: []string{"podLabels"}},
	{name: "kates", release: "kates", ns: func(n resolvedNamespaces) string { return n.app },
		selected: func() bool { return true }, labelKeys: []string{"podLabels"}, exceptions: true},
}

// kyvernoCheckedCharts are the charts this run checks: --stack's, --chart's,
// or the kates chart.
func kyvernoCheckedCharts() ([]kyvernoStackChart, error) {
	switch {
	case kyvernoCheckStack && len(kyvernoCheckCharts) > 0:
		return nil, fmt.Errorf("--stack checks every chart deploy installs: drop --chart, or --stack")
	case kyvernoCheckStack:
		var charts []kyvernoStackChart
		for _, c := range kyvernoStackCharts {
			if c.selected() {
				charts = append(charts, c)
			}
		}
		return charts, nil
	case len(kyvernoCheckCharts) == 0:
		return []kyvernoStackChart{kyvernoStackCharts[len(kyvernoStackCharts)-1]}, nil
	}
	var charts []kyvernoStackChart
	for _, c := range kyvernoStackCharts {
		if slices.Contains(kyvernoCheckCharts, c.name) {
			charts = append(charts, c)
		}
	}
	for _, name := range kyvernoCheckCharts {
		if !slices.ContainsFunc(kyvernoStackCharts, func(c kyvernoStackChart) bool { return c.name == name }) {
			return nil, fmt.Errorf("--chart %s: not a chart kates deploy installs (%s)", name, strings.Join(kyvernoStackChartNames(), ", "))
		}
	}
	return charts, nil
}

func kyvernoStackChartNames() []string {
	names := make([]string, len(kyvernoStackCharts))
	for i, c := range kyvernoStackCharts {
		names[i] = c.name
	}
	return names
}

// forChart is the entries of a -f or --set list that apply to chart: those
// prefixed <chart>=, without the prefix, and those with no chart prefix.
func forChart(entries []string, chart string) []string {
	var out []string
	for _, e := range entries {
		prefix, rest, ok := strings.Cut(e, "=")
		switch {
		case ok && prefix == chart:
			out = append(out, rest)
		case ok && slices.Contains(kyvernoStackChartNames(), prefix):
		default:
			out = append(out, e)
		}
	}
	return out
}

var kyvernoCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Hold the charts kates deploy installs, as you would install them, to the cluster's Kyverno policies",
	Long: `Reads the Kyverno policies and policy exceptions the cluster holds, renders a
chart with helm template and the values you pass, and evaluates the render with
the Kyverno CLI (kyverno apply), before anything is installed. Each failed rule
is listed with the chart value most likely to fix it, and the
kyvernoPolicy.exceptions entry that would exempt the release from it.

It checks the kates chart; --chart names others kates deploy installs
(strimzi-operator, monitoring, kafka-cluster, connect-cluster, mirror-maker2,
kates), and --stack checks each chart deploy would install with the same
--with-* flags, each in the namespace deploy would put it (the --*-ns flags;
-n puts every chart in one). A -f or --set entry applies to every chart, or
to one when prefixed with its name: -f kafka-cluster=kafka.yaml.

--write-values writes <chart>-kyverno.yaml into a directory, for each chart
with a rule that failed: a missing label is set to the value --label gives,
under every value that labels the chart's pods; any other rule is a commented
kyvernoPolicy.exceptions entry, or a note where the chart has none. Pass the
files back with -f <chart>=<file>. Kafka's broker pods come from the Kafka
CR's templates, which Strimzi renders: the kafka-cluster chart's podLabels
reach them, but the check sees only the CR.

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
  kates kyverno check --stack --with-kafka-connect
  kates kyverno check --stack --label team=payments --write-values kyverno/
  kates kyverno check --stack -f kafka-cluster=kyverno/kafka-cluster-kyverno.yaml -f kates=kyverno/kates-kyverno.yaml
  kates kyverno check --chart kafka-cluster -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		charts, err := kyvernoCheckedCharts()
		if err != nil {
			return couldNotTell(err.Error())
		}
		labels := map[string]string{}
		for _, l := range kyvernoCheckLabels {
			k, v, ok := strings.Cut(l, "=")
			if !ok || k == "" {
				return couldNotTell(fmt.Sprintf("--label %s: expected key=value", l))
			}
			labels[k] = v
		}
		kyvernoBin, err := kyvernoPathFn()
		if err != nil {
			return couldNotTell("the kyverno CLI is not on PATH: install it (brew install kyverno, or https://kyverno.io/docs/kyverno-cli/install/)")
		}
		names := make([]string, len(charts))
		for i, c := range charts {
			names[i] = c.name
		}
		if err := resolveChartRoot(ctx, "", false, names...); err != nil {
			return couldNotTell(err.Error())
		}

		resolved := resolveNamespaces()
		reads := map[string]kyvernoRead{}
		var reports []kyvernoCheckReport
		for _, c := range charts {
			ns := kyvernoCheckNamespace
			if ns == "" {
				ns = c.ns(resolved)
			}
			read, ok := reads[ns]
			if !ok {
				if read, err = readKyvernoPolicies(ctx, ns); err != nil {
					return couldNotTell(err.Error())
				}
				reads[ns] = read
			}
			report, err := checkKyvernoChart(ctx, kyvernoBin, c, ns, read)
			if err != nil {
				return couldNotTell(c.name + ": " + err.Error())
			}
			if kyvernoCheckWriteTo != "" {
				if report.ValuesFile, err = writeKyvernoValues(kyvernoCheckWriteTo, c, report, labels); err != nil {
					return couldNotTell(err.Error())
				}
			}
			reports = append(reports, report)
		}
		return finishKyvernoCheck(reports)
	},
}

// checkKyvernoChart renders one chart in ns and holds it to the policies read.
func checkKyvernoChart(ctx context.Context, kyvernoBin string, c kyvernoStackChart, ns string, read kyvernoRead) (kyvernoCheckReport, error) {
	report := kyvernoCheckReport{Chart: c.name, Namespace: ns, Unread: read.unread}
	if len(read.policies) == 0 {
		return report, nil
	}
	render, err := renderChart(ctx, c, ns)
	if err != nil {
		return report, err
	}
	dir, err := os.MkdirTemp("", "kates-kyverno-check-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(dir)
	files := map[string][]byte{
		"policies.yaml":   joinDocuments(read.policies),
		"exceptions.yaml": joinDocuments(read.exceptions),
		"render.yaml":     render,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			return report, err
		}
	}
	kyvernoArgs := []string{"apply", filepath.Join(dir, "policies.yaml"),
		"--resource", filepath.Join(dir, "render.yaml"),
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
		return report, fmt.Errorf("kyverno apply: %w", err)
	}
	report.add(results)
	return report, nil
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

// renderChart renders a chart as deploy would install it in ns, after the
// helm dependency command deploy runs on it. kyverno.io/v2 lets the chart
// render its own PolicyException, as on a cluster that has Kyverno.
func renderChart(ctx context.Context, c kyvernoStackChart, ns string) ([]byte, error) {
	dir := chartRootPath("charts/" + c.name)
	if len(c.deps) > 0 && !chartsPublished {
		if _, err := commandRunner.Output(ctx, "helm", append(append([]string{"dependency"}, c.deps...), dir)...); err != nil {
			return nil, fmt.Errorf("helm dependency %s: %s", c.deps[0], strings.TrimSpace(proc.Stderr(err)+" "+err.Error()))
		}
	}
	release := c.release
	if release == "" {
		release = deployKafkaName
	}
	args := []string{"template", release, dir, "-n", ns, "--api-versions", "kyverno.io/v2"}
	for _, f := range c.values {
		args = append(args, "-f", chartRootPath(f))
	}
	for _, f := range forChart(kyvernoCheckValues, c.name) {
		args = append(args, "-f", f)
	}
	for _, s := range forChart(kyvernoCheckSet, c.name) {
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
// that matches by namespace sees nothing else. It edits YAML nodes, not maps:
// a subchart may render a key twice (kube-prometheus-stack's commonLabels and
// a component's podLabels), which helm and kyverno accept and a map refuses.
func inNamespace(render []byte, ns string) ([]byte, error) {
	dec := yaml.NewDecoder(bytes.NewReader(render))
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("helm template output: %w", err)
		}
		if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
			continue
		}
		root := doc.Content[0]
		kind, meta := "", (*yaml.Node)(nil)
		for i := 0; i+1 < len(root.Content); i += 2 {
			switch root.Content[i].Value {
			case "kind":
				kind = root.Content[i+1].Value
			case "metadata":
				meta = root.Content[i+1]
			}
		}
		if meta != nil && meta.Kind == yaml.MappingNode && !clusterScoped(kind) && !hasKey(meta, "namespace") {
			meta.Content = append(meta.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "namespace"}, &yaml.Node{Kind: yaml.ScalarNode, Value: ns})
		}
		if err := enc.Encode(&doc); err != nil {
			return nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func hasKey(m *yaml.Node, key string) bool {
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return true
		}
	}
	return false
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
	Chart      string               `json:"chart"`
	Namespace  string               `json:"namespace"`
	Unread     []string             `json:"unread,omitempty"`
	Pass       int                  `json:"pass"`
	Skip       int                  `json:"skip"`
	Findings   []kyvernoFinding     `json:"findings"`
	Exceptions []kyvernoChartExcept `json:"exceptions,omitempty"`
	ValuesFile string               `json:"valuesFile,omitempty"`
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

// finishKyvernoCheck prints the reports and ends with the exit code they
// call for: 1 when a rule fails, else 2 when a rule errored or a kind could
// not be read, else 0.
func finishKyvernoCheck(reports []kyvernoCheckReport) error {
	failed, errored := 0, 0
	var unread []string
	for i, r := range reports {
		for _, f := range r.Findings {
			switch f.Result {
			case "fail":
				failed++
			case "error":
				errored++
			}
		}
		for _, u := range r.Unread {
			if !slices.Contains(unread, u) {
				unread = append(unread, u)
			}
		}
		if r.Findings == nil {
			reports[i].Findings = []kyvernoFinding{}
		}
	}
	output.Render(outputMode == "json", reports, func() { renderKyvernoCheck(reports) })
	switch {
	case failed > 0:
		return &exitError{code: failedExitCode}
	case errored > 0:
		return &exitError{code: couldNotTellExitCode, msg: fmt.Sprintf("%d rule(s) errored: kyverno could not evaluate them", errored)}
	case len(unread) > 0:
		return &exitError{code: couldNotTellExitCode, msg: "the account may not read " + strings.Join(unread, ", ") + ": the policies there were not checked"}
	}
	return nil
}

func renderKyvernoCheck(reports []kyvernoCheckReport) {
	output.Banner("Kyverno Check", "the charts kates deploy installs, against the cluster's policies")
	var again []string
	for _, r := range reports {
		fmt.Println()
		output.SubHeader("The " + r.Chart + " chart in " + r.Namespace)
		renderKyvernoChartCheck(r)
		if r.ValuesFile != "" {
			output.Hint("Wrote " + r.ValuesFile)
			again = append(again, "-f "+r.Chart+"="+r.ValuesFile)
		}
	}
	if len(again) > 0 {
		fmt.Println()
		output.Hint("Check again with the values written: kates kyverno check <the same flags> " + strings.Join(again, " "))
	}
}

func renderKyvernoChartCheck(r kyvernoCheckReport) {
	for _, u := range r.Unread {
		output.Warn("Not read (forbidden): " + u)
	}
	if r.Pass+r.Skip+len(r.Findings) == 0 {
		output.Success("No Kyverno policy applies: the cluster has none the account can read, or none matches the chart")
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
		if !kyvernoChartHasExceptions(r.Chart) {
			output.Hint("The " + r.Chart + " chart renders no PolicyException: exempt it from these rules with one of your own, in " + r.Namespace + ":")
		} else {
			output.Hint("Values that render a PolicyException for the release's own pods (Kyverno must accept exceptions in " + r.Namespace + "):")
			fmt.Println("kyvernoPolicy:")
			fmt.Println("  exceptions:")
		}
		for _, e := range r.Exceptions {
			fmt.Printf("    - policyName: %s\n      ruleNames: [%s]\n", e.PolicyName, strings.Join(e.RuleNames, ", "))
		}
		fmt.Println()
	}
	output.Hint(fmt.Sprintf("%d passed, %d skipped, %d did not pass", r.Pass, r.Skip, len(r.Findings)))
}

func kyvernoChartHasExceptions(chart string) bool {
	return slices.ContainsFunc(kyvernoStackCharts, func(c kyvernoStackChart) bool { return c.name == chart && c.exceptions })
}

// podLabelPath is a label path in a pod or a pod template, as Kyverno names
// it in a failed pattern's message.
var podLabelPath = regexp.MustCompile(`/((?:spec/(?:jobTemplate/spec/)?template/)?)metadata/labels/([^/\s]+)/`)

// missingPodLabel is the pod label a finding asks for, when the finding is
// on pods or pod templates only; "" otherwise.
func missingPodLabel(f kyvernoFinding) string {
	m := podLabelPath.FindStringSubmatch(f.Message)
	if m == nil {
		return ""
	}
	if m[1] == "" {
		for _, r := range f.Resources {
			if !strings.HasPrefix(r, "Pod/") {
				return ""
			}
		}
	}
	// JSON Pointer escapes a / in the key as ~1.
	return strings.ReplaceAll(strings.ReplaceAll(m[2], "~1", "/"), "~0", "~")
}

// writeKyvernoValues writes dir/<chart>-kyverno.yaml with what the check can
// fill in exactly, over the values already in that file: each missing pod
// label --label names, under every value that labels the chart's pods. Any
// other rule that failed is a commented exception entry, or a note where the
// chart has no exceptions value. It returns the file, or "" when no rule failed.
func writeKyvernoValues(dir string, c kyvernoStackChart, r kyvernoCheckReport, labels map[string]string) (string, error) {
	set := map[string]string{}
	unfixed := map[string]map[string]bool{} // policy -> base rules
	var notes []string
	for _, f := range r.Findings {
		if f.Result == "warn" {
			continue
		}
		if key := missingPodLabel(f); key != "" {
			if v, ok := labels[key]; ok {
				set[key] = v
				continue
			}
		}
		if unfixed[f.Policy] == nil {
			unfixed[f.Policy] = map[string]bool{}
		}
		unfixed[f.Policy][baseRule(f.Rule)] = true
		note := fmt.Sprintf("%s/%s: %s", f.Policy, f.Rule, f.Message)
		if key := missingPodLabel(f); key != "" {
			note += fmt.Sprintf(" (--label %s=<value> sets it)", key)
		} else if f.Fix != "" {
			note += " (try " + f.Fix + ")"
		}
		notes = append(notes, note)
	}
	if len(set) == 0 && len(unfixed) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, c.name+"-kyverno.yaml")
	values := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(b, &values); err != nil {
			return "", fmt.Errorf("%s: %w", path, err)
		}
		if values == nil {
			values = map[string]any{}
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	for _, key := range c.labelKeys {
		if len(set) == 0 {
			break
		}
		m := values
		for _, part := range strings.Split(key, ".") {
			next, ok := m[part].(map[string]any)
			if !ok {
				next = map[string]any{}
				m[part] = next
			}
			m = next
		}
		for k, v := range set {
			m[k] = v
		}
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "# Written by kates kyverno check for the %s chart in %s.\n# Check again with -f %s=%s\n", c.name, r.Namespace, c.name, path)
	if len(values) > 0 {
		b, err := yaml.Marshal(values)
		if err != nil {
			return "", err
		}
		out.Write(b)
	}
	if len(unfixed) > 0 {
		writeUnfixed(&out, c, r.Namespace, notes, unfixed)
	}
	return path, os.WriteFile(path, out.Bytes(), 0o644)
}

// writeUnfixed comments each rule --write-values could not fix, and the
// exception entries that would exempt the release from them.
func writeUnfixed(out *bytes.Buffer, c kyvernoStackChart, ns string, notes []string, unfixed map[string]map[string]bool) {
	out.WriteString("\n# Not fixed here:\n")
	for _, n := range notes {
		out.WriteString("#   " + strings.ReplaceAll(n, "\n", " ") + "\n")
	}
	policies := make([]string, 0, len(unfixed))
	for p := range unfixed {
		policies = append(policies, p)
	}
	sort.Strings(policies)
	if c.exceptions {
		out.WriteString("# Or exempt the release (Kyverno must accept exceptions in " + ns + "):\n")
		out.WriteString("# kyvernoPolicy:\n#   exceptions:\n")
		for _, p := range policies {
			rules := make([]string, 0, len(unfixed[p]))
			for rule := range unfixed[p] {
				rules = append(rules, rule)
			}
			sort.Strings(rules)
			fmt.Fprintf(out, "#     - policyName: %s\n#       ruleNames: [%s]\n", p, strings.Join(rules, ", "))
		}
	} else {
		out.WriteString("# The " + c.name + " chart has no kyvernoPolicy.exceptions value: exempt it with a\n# PolicyException of your own in " + ns + ", or ask the policy's owner.\n")
	}
}

func init() {
	f := kyvernoCheckCmd.Flags()
	f.StringVarP(&kyvernoCheckNamespace, "namespace", "n", "", "Namespace every chart checked would be installed in (default: where kates deploy puts each, from the --*-ns flags)")
	f.StringArrayVarP(&kyvernoCheckValues, "values", "f", nil, "Values file, for every chart checked or, as <chart>=<file>, for one (repeatable)")
	f.StringArrayVar(&kyvernoCheckSet, "set", nil, "Value as helm --set, for every chart checked or, as <chart>=<key>=<value>, for one (repeatable)")
	f.StringArrayVar(&kyvernoCheckCharts, "chart", nil, "Chart to check: "+strings.Join(kyvernoStackChartNames(), ", ")+" (repeatable; default: kates)")
	f.BoolVar(&kyvernoCheckStack, "stack", false, "Check every chart kates deploy would install with the --with-* flags given")
	f.StringVar(&kyvernoCheckWriteTo, "write-values", "", "Write <chart>-kyverno.yaml into this directory for each chart with a failed rule")
	f.StringArrayVar(&kyvernoCheckLabels, "label", nil, "Value of a pod label a policy requires, as key=value, for --write-values (repeatable)")
	// deploy's own flags, bound to its variables, so --stack sees what deploy would install, where.
	for _, name := range []string{"with-strimzi", "with-monitoring", "with-kafka-connect", "with-mirror-maker2",
		"kafka-ns", "connect-ns", "app-ns", "monitoring-ns", "mm2-ns", "kafka-name"} {
		f.AddFlag(deployCmd.Flags().Lookup(name))
	}
	kyvernoCmd.AddCommand(kyvernoCheckCmd)
}
