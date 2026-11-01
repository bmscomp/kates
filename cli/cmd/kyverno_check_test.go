package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/internal/proc"
	"github.com/bmscomp/kates/cli/output"
	"gopkg.in/yaml.v3"
)

const (
	kyvernoTeamPolicy = `{"apiVersion":"kyverno.io/v1","kind":"ClusterPolicy","metadata":{"name":"require-team"}}`
	kyvernoException  = `{"apiVersion":"kyverno.io/v2","kind":"PolicyException","metadata":{"name":"kates","namespace":"team"}}`
	// What kyverno apply prints: deprecation warnings, then the report on one line.
	kyvernoReport = `Warning: policies.yaml: kyverno.io/v1 ClusterPolicy is deprecated
{"kind":"ClusterReport","apiVersion":"openreports.io/v1alpha1","summary":{"pass":2,"fail":3},"results":[
` + `{"policy":"require-team","rule":"autogen-check-team","result":"fail","message":"validation error: label team is required. rule autogen-check-team failed at path /spec/template/metadata/labels/team/","resources":[{"kind":"Deployment","namespace":"team","name":"kates"}]},` +
		`{"policy":"require-team","rule":"autogen-check-team","result":"fail","message":"validation error: label team is required. rule autogen-check-team failed at path /spec/template/metadata/labels/team/","resources":[{"kind":"Job","namespace":"team","name":"kates-migrate"}]},` +
		`{"policy":"allowed-registries","rule":"registries","result":"fail","message":"images must come from the allowed registry","resources":[{"kind":"Pod","namespace":"team","name":"kates-test"}]},` +
		`{"policy":"audit-host","rule":"no-host","result":"warn","message":"use of hostPath","resources":[{"kind":"Pod","namespace":"team","name":"kates-test"}]},` +
		`{"policy":"require-team","rule":"autogen-no-latest","result":"pass","resources":[]},` +
		`{"policy":"require-team","rule":"check-team","result":"pass","resources":[]}` + `]}`
)

// stubKyvernoCheck scripts a cluster with one ClusterPolicy and one
// exception the account may read only in team, and no CEL policy kinds.
func stubKyvernoCheck(t *testing.T, report string) *proc.Fake {
	t.Helper()
	origPath := kyvernoPathFn
	kyvernoPathFn = func() (string, error) { return "kyverno", nil }
	origNS, origValues, origSet, origMode := kyvernoCheckNamespace, kyvernoCheckValues, kyvernoCheckSet, outputMode
	kyvernoCheckNamespace, kyvernoCheckValues, kyvernoCheckSet, outputMode = "team", []string{"team.yaml"}, []string{"podLabels.a=b"}, "json"
	t.Cleanup(func() {
		kyvernoPathFn = origPath
		kyvernoCheckNamespace, kyvernoCheckValues, kyvernoCheckSet, outputMode = origNS, origValues, origSet, origMode
	})
	f := stubRunner(t).
		On("kubectl get clusterpolicies.kyverno.io", `{"items":[`+kyvernoTeamPolicy+`]}`).
		Fail("kubectl get validatingpolicies.policies.kyverno.io", `error: the server doesn't have a resource type "validatingpolicies"`).
		Fail("kubectl get imagevalidatingpolicies.policies.kyverno.io", `error: the server doesn't have a resource type "imagevalidatingpolicies"`).
		On("kubectl get policies.kyverno.io -o json -n team", `{"items":[]}`).
		Fail("kubectl get policyexceptions.kyverno.io -o json -A", `Error from server (Forbidden): policyexceptions.kyverno.io is forbidden`).
		On("kubectl get policyexceptions.kyverno.io -o json -n team", `{"items":[`+kyvernoException+`]}`).
		Fail("kubectl get policyexceptions.policies.kyverno.io", `error: the server doesn't have a resource type "policyexceptions"`).
		On("helm template kates", "kind: Deployment\n")
	f.Handle("kyverno apply", func(c proc.Call) ([]byte, error) {
		read := func(name string) string {
			b, err := os.ReadFile(filepath.Join(filepath.Dir(c.Args[1]), name))
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
		if got := read("policies.yaml"); got != kyvernoTeamPolicy {
			t.Errorf("policies.yaml = %q", got)
		}
		if got := read("exceptions.yaml"); got != kyvernoException {
			t.Errorf("exceptions.yaml = %q", got)
		}
		// The chart's render, then the namespace deploy would create for it.
		if got := read("render.yaml"); !strings.HasPrefix(got, "kind: Deployment\n\n---\napiVersion: v1\nkind: Namespace\nmetadata:\n  name: team\n") {
			t.Errorf("render.yaml = %q", got)
		}
		for _, want := range []string{"--exception", "--exceptions-within-resources", "--audit-warn", "--policy-report"} {
			if !slices.Contains(c.Args, want) {
				t.Errorf("kyverno args %q lack %s", c.Args, want)
			}
		}
		return []byte(report), errors.New("exit status 1")
	})
	return f
}

func runKyvernoCheck(t *testing.T) (kyvernoCheckReport, error) {
	t.Helper()
	reports, err := runKyvernoChecks(t)
	if len(reports) != 1 {
		t.Fatalf("%d reports, want 1: %+v", len(reports), reports)
	}
	return reports[0], err
}

func runKyvernoChecks(t *testing.T) ([]kyvernoCheckReport, error) {
	t.Helper()
	buf := output.ResetForTesting()
	var err error
	out := captureStdout(t, func() { err = kyvernoCheckCmd.RunE(kyvernoCheckCmd, nil) })
	var reports []kyvernoCheckReport
	if jerr := json.Unmarshal([]byte(out+buf.String()), &reports); jerr != nil {
		t.Fatalf("not a JSON report (err %v): %v\n%s%s", err, jerr, out, buf.String())
	}
	return reports, err
}

// A failed rule is one finding naming every resource it failed on, with the
// chart value that fixes it; the exception names the rule as declared.
func TestKyvernoCheckMapsEachFailureToAChartValue(t *testing.T) {
	f := stubKyvernoCheck(t, kyvernoReport)

	report, err := runKyvernoCheck(t)

	if code := exitCode(err); code != failedExitCode {
		t.Errorf("exit %d, want 1", code)
	}
	if report.Pass != 2 || len(report.Findings) != 3 {
		t.Fatalf("pass %d, findings %+v", report.Pass, report.Findings)
	}
	team := report.Findings[0]
	if team.Fix != "podLabels.team=<value>" || !slices.Equal(team.Resources, []string{"Deployment/kates", "Job/kates-migrate"}) {
		t.Errorf("team finding = %+v", team)
	}
	if fix := report.Findings[1].Fix; fix != "global.imageRegistry=<the allowed registry>" {
		t.Errorf("registry fix = %q", fix)
	}
	if report.Findings[2].Result != "warn" {
		t.Errorf("audit finding = %+v", report.Findings[2])
	}
	want := []kyvernoChartExcept{
		{PolicyName: "allowed-registries", RuleNames: []string{"registries"}},
		{PolicyName: "require-team", RuleNames: []string{"check-team"}},
	}
	if !slices.EqualFunc(report.Exceptions, want, func(a, b kyvernoChartExcept) bool {
		return a.PolicyName == b.PolicyName && slices.Equal(a.RuleNames, b.RuleNames)
	}) {
		t.Errorf("exceptions = %+v, want %+v", report.Exceptions, want)
	}
	var helm string
	for _, line := range f.Lines() {
		if strings.HasPrefix(line, "helm template") {
			helm = line
		}
	}
	for _, want := range []string{"-n team", "--api-versions kyverno.io/v2", "-f team.yaml", "--set podLabels.a=b"} {
		if !strings.Contains(helm, want) {
			t.Errorf("helm call %q lacks %q", helm, want)
		}
	}
}

// Only audit warnings: the install would go through.
func TestKyvernoCheckPassesWithWarningsOnly(t *testing.T) {
	stubKyvernoCheck(t, `{"results":[{"policy":"audit-host","rule":"no-host","result":"warn","message":"m","resources":[{"kind":"Pod","name":"p"}]}]}`)

	report, err := runKyvernoCheck(t)

	if err != nil || len(report.Exceptions) != 0 {
		t.Errorf("err %v, exceptions %+v", err, report.Exceptions)
	}
}

// A kind the account may not read leaves the check unable to pass.
func TestKyvernoCheckCannotPassWhatItCouldNotRead(t *testing.T) {
	stubKyvernoCheck(t, `{"results":[]}`)
	g := stubRunner(t).
		Fail("kubectl get clusterpolicies.kyverno.io", `Error from server (Forbidden): clusterpolicies.kyverno.io is forbidden`).
		On("kubectl get", `{"items":[]}`)

	report, err := runKyvernoCheck(t)

	if code := exitCode(err); code != couldNotTellExitCode {
		t.Errorf("exit %d, want 2", code)
	}
	if !slices.Equal(report.Unread, []string{"clusterpolicies.kyverno.io"}) {
		t.Errorf("unread = %q", report.Unread)
	}
	for _, line := range g.Lines() {
		if strings.HasPrefix(line, "helm") || strings.HasPrefix(line, "kyverno") {
			t.Errorf("with no policy read, nothing renders: %s", line)
		}
	}
}

// An exception Kyverno would ignore, the cluster's or the chart's, does not
// count: the check would otherwise pass what admission refuses.
func TestKyvernoCheckCountsOnlyExceptionsKyvernoAccepts(t *testing.T) {
	for _, tc := range []struct{ args, note string }{
		{`"--enablePolicyException=false"`, "accepts no PolicyException"},
		{`"--enablePolicyException=true","--exceptionNamespace=kyverno"`, "only in kyverno"},
	} {
		stubKyvernoCheck(t, `{"results":[]}`)
		f := stubRunner(t).
			On("kubectl get deploy -A", `{"items":[{"spec":{"template":{"spec":{"containers":[{"name":"kyverno","args":[`+tc.args+`]}]}}}}]}`).
			On("kubectl get clusterpolicies.kyverno.io", `{"items":[`+kyvernoTeamPolicy+`]}`).
			On("kubectl get policyexceptions.kyverno.io", `{"items":[`+kyvernoException+`]}`).
			On("kubectl get", `{"items":[]}`).
			On("helm template kates", "kind: PolicyException\n").
			On("kyverno apply", `{"results":[]}`)

		report, _ := runKyvernoCheck(t)

		if !strings.Contains(report.ExceptionNote, tc.note) {
			t.Errorf("%s: note = %q, want %q", tc.args, report.ExceptionNote, tc.note)
		}
		for _, c := range f.Calls() {
			if c.Name == "kyverno" && (slices.Contains(c.Args, "--exception") || slices.Contains(c.Args, "--exceptions-within-resources")) {
				t.Errorf("%s: kyverno counted an ignored exception: %q", tc.args, c.Args)
			}
		}
	}
}

func TestKyvernoCheckNeedsTheKyvernoCLI(t *testing.T) {
	stubKyvernoCheck(t, "")
	kyvernoPathFn = func() (string, error) { return "", os.ErrNotExist }

	err := kyvernoCheckCmd.RunE(kyvernoCheckCmd, nil)

	if code := exitCode(err); code != couldNotTellExitCode || !strings.Contains(err.Error(), "kyverno CLI") {
		t.Errorf("err %v (exit %d)", err, code)
	}
}

func TestKyvernoFix(t *testing.T) {
	for _, c := range []struct{ policy, rule, message, want string }{
		{"disallow-latest-tag", "validate-image-tag", "Using a mutable image tag e.g. 'latest' is not allowed.", "image.tag=<a pinned tag> (and the tag of the image the rule names)"},
		{"require-run-as-nonroot", "run-as-non-root", "Running as root is not allowed.", "securityContext and containerSecurityContext (the chart's are restricted by default)"},
		{"require-requests-limits", "validate-resources", "CPU and memory resource requests and limits are required.", "resources.requests and resources.limits (and the failing component's own resources values)"},
		{"require-labels", "check-for-labels", "The label `app.kubernetes.io/name` is required.", "podLabels.<key>=<value>"},
		{"disallow-host-path", "host-path", "HostPath volumes are forbidden.", ""},
	} {
		if got := kyvernoFix(c.policy, c.rule, c.message); got != c.want {
			t.Errorf("%s: %q, want %q", c.policy, got, c.want)
		}
	}
}

// helm template leaves the namespace to the install; the check sets it, so
// an exception matching namespaces: [team] matches offline too.
func TestInNamespaceSetsTheReleaseNamespace(t *testing.T) {
	// A label rendered twice, as kube-prometheus-stack does, passes through.
	render := "---\n# Source: x\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: namespace\n  labels:\n    team: a\n    team: a\n" +
		"---\napiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: kates\n" +
		"---\napiVersion: rbac.authorization.k8s.io/v1\nkind: Role\nmetadata:\n  name: r\n  namespace: kafka\n"

	out, err := inNamespace([]byte(render), "team")

	if err != nil {
		t.Fatal(err)
	}
	var namespaces []string
	dec := yaml.NewDecoder(strings.NewReader(string(out)))
	for {
		var doc struct {
			Metadata struct{ Namespace string } `yaml:"metadata"`
		}
		if dec.Decode(&doc) != nil {
			break
		}
		namespaces = append(namespaces, doc.Metadata.Namespace)
	}
	if want := []string{"team", "", "kafka"}; !slices.Equal(namespaces, want) {
		t.Errorf("namespaces = %q, want %q", namespaces, want)
	}
}

// --stack checks each chart deploy would install with the --with-* flags,
// in the namespace deploy would put it, with the values meant for it.
func TestKyvernoCheckStackChecksWhatDeployInstalls(t *testing.T) {
	stubKyvernoCheck(t, "")
	vars := []*bool{&kyvernoCheckStack, &deployWithStrimzi, &deployWithMonitoring, &deployWithKafkaConnect, &deployWithMirrorMaker2}
	orig := make([]bool, len(vars))
	for i, v := range vars {
		orig[i] = *v
	}
	origNS := []string{deployTopology, deployKafkaNS, deployConnectNS, deployAppNS, deployKafkaName}
	t.Cleanup(func() {
		for i, v := range vars {
			*v = orig[i]
		}
		deployTopology, deployKafkaNS, deployConnectNS, deployAppNS, deployKafkaName = origNS[0], origNS[1], origNS[2], origNS[3], origNS[4]
	})
	kyvernoCheckStack, deployWithStrimzi, deployWithMonitoring, deployWithKafkaConnect, deployWithMirrorMaker2 = true, true, false, true, false
	deployTopology, deployKafkaNS, deployConnectNS, deployAppNS, deployKafkaName = "isolated", "kafka", "connect", "team", "krafter"
	kyvernoCheckNamespace = ""
	kyvernoCheckValues = []string{"kafka-cluster=kafka.yaml", "all.yaml"}
	kyvernoCheckSet = []string{"kates=image.tag=1.0"}
	f := stubRunner(t).
		On("kubectl get", `{"items":[`+kyvernoTeamPolicy+`]}`).
		On("helm template", "kind: Pod\nmetadata:\n  name: p\n").
		On("kyverno apply", `{"results":[{"policy":"require-team","rule":"check-team","result":"pass"}]}`)

	reports, err := runKyvernoChecks(t)

	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range reports {
		got = append(got, r.Chart+"@"+r.Namespace)
	}
	if want := []string{"strimzi-operator@strimzi-operator", "kafka-cluster@kafka", "connect-cluster@connect", "kates@team"}; !slices.Equal(got, want) {
		t.Errorf("charts = %q, want %q", got, want)
	}
	var helm []string
	for _, line := range f.Lines() {
		if strings.HasPrefix(line, "helm") {
			helm = append(helm, line)
		}
	}
	want := []string{
		"helm dependency build charts/strimzi-operator",
		"helm template strimzi-operator charts/strimzi-operator -n strimzi-operator --api-versions kyverno.io/v2 -f all.yaml",
		"helm dependency update charts/kafka-cluster",
		"helm template krafter charts/kafka-cluster -n kafka --api-versions kyverno.io/v2 -f charts/kafka-cluster/values-platform.yaml -f kafka.yaml -f all.yaml",
		"helm dependency update --skip-refresh charts/connect-cluster",
		"helm template connect-cluster charts/connect-cluster -n connect --api-versions kyverno.io/v2 -f all.yaml",
		"helm template kates charts/kates -n team --api-versions kyverno.io/v2 -f all.yaml --set image.tag=1.0",
	}
	if !slices.Equal(helm, want) {
		t.Errorf("helm calls:\n got %q\nwant %q", helm, want)
	}
	// Each namespace's Policies are read once, the cluster's once per namespace.
	var policies []string
	for _, line := range f.Lines() {
		if strings.HasPrefix(line, "kubectl get policies.kyverno.io") {
			policies = append(policies, line)
		}
	}
	if len(policies) != 4 || !slices.Contains(policies, "kubectl get policies.kyverno.io -o json -n kafka") {
		t.Errorf("Policies read: %q", policies)
	}
}

func TestKyvernoCheckRefusesAChartDeployDoesNotInstall(t *testing.T) {
	stubKyvernoCheck(t, "")
	t.Cleanup(func() { kyvernoCheckCharts = nil })
	kyvernoCheckCharts = []string{"velero"}

	err := kyvernoCheckCmd.RunE(kyvernoCheckCmd, nil)

	if code := exitCode(err); code != couldNotTellExitCode || !strings.Contains(err.Error(), "kafka-cluster") {
		t.Errorf("err %v (exit %d)", err, code)
	}
}

func TestMissingPodLabel(t *testing.T) {
	for _, c := range []struct {
		message   string
		resources []string
		want      string
	}{
		{"rule failed at path /spec/template/metadata/labels/team/", []string{"Deployment/a"}, "team"},
		{"rule failed at path /spec/jobTemplate/spec/template/metadata/labels/team/", []string{"CronJob/a"}, "team"},
		{"rule failed at path /metadata/labels/team/", []string{"Pod/a", "Pod/b"}, "team"},
		{"rule failed at path /metadata/labels/example.com~1team/", []string{"Pod/a"}, "example.com/team"},
		// The Deployment's own labels: podLabels does not set them.
		{"rule failed at path /metadata/labels/team/", []string{"Deployment/a"}, ""},
		{"HostPath volumes are forbidden.", []string{"Pod/a"}, ""},
	} {
		if got := missingPodLabel(kyvernoFinding{Message: c.message, Resources: c.resources}); got != c.want {
			t.Errorf("%s %q: %q, want %q", c.message, c.resources, got, c.want)
		}
	}
}

func kyvernoStackChartNamed(t *testing.T, name string) kyvernoStackChart {
	t.Helper()
	i := slices.IndexFunc(kyvernoStackCharts, func(c kyvernoStackChart) bool { return c.name == name })
	if i < 0 {
		t.Fatalf("no stack chart %s", name)
	}
	return kyvernoStackCharts[i]
}

// A missing label --label gives goes under every value that labels the
// chart's pods; a rule it cannot fix is a note where the chart renders no
// exception; audit warnings are left alone.
func TestWriteKyvernoValuesSetsTheLabelEverywhere(t *testing.T) {
	dir := t.TempDir()
	report := kyvernoCheckReport{Chart: "monitoring", Namespace: "monitoring", Findings: []kyvernoFinding{
		{Result: "fail", Policy: "require-team", Rule: "autogen-check-team", Message: "failed at path /spec/template/metadata/labels/team/", Resources: []string{"Deployment/grafana"}},
		{Result: "fail", Policy: "no-host-path", Rule: "host-path", Message: "HostPath volumes are forbidden.", Resources: []string{"DaemonSet/node-exporter"}},
		{Result: "warn", Policy: "audit-owner", Rule: "owner", Message: "failed at path /metadata/labels/owner/", Resources: []string{"Pod/p"}},
	}}

	path, err := writeKyvernoValues(dir, kyvernoStackChartNamed(t, "monitoring"), report, map[string]string{"team": "payments", "owner": "x"})

	if err != nil || path != filepath.Join(dir, "monitoring-kyverno.yaml") {
		t.Fatalf("path %q, err %v", path, err)
	}
	b, _ := os.ReadFile(path)
	var values map[string]any
	if err := yaml.Unmarshal(b, &values); err != nil {
		t.Fatal(err)
	}
	for _, key := range chartPodLabelKeys("monitoring") {
		m := values
		for _, part := range strings.Split(key, ".") {
			m, _ = m[part].(map[string]any)
		}
		if m["team"] != "payments" || m["owner"] != nil {
			t.Errorf("%s = %v, want only team=payments", key, m)
		}
	}
	text := string(b)
	for _, want := range []string{"#   no-host-path/host-path: HostPath volumes are forbidden.", "no kyvernoPolicy.exceptions value"} {
		if !strings.Contains(text, want) {
			t.Errorf("file lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "audit-owner") || strings.Contains(text, "require-team") {
		t.Errorf("file names a warning or a fixed rule:\n%s", text)
	}
}

// Without the label's value the rule is a commented exception of a chart
// that has them; values already in the file stay.
func TestWriteKyvernoValuesCommentsAnExceptionAndKeepsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kates-kyverno.yaml")
	if err := os.WriteFile(path, []byte("image:\n  tag: \"1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report := kyvernoCheckReport{Chart: "kates", Namespace: "team", Findings: []kyvernoFinding{
		{Result: "fail", Policy: "require-team", Rule: "autogen-check-team", Message: "failed at path /spec/template/metadata/labels/team/", Resources: []string{"Deployment/kates"}},
	}}

	if _, err := writeKyvernoValues(dir, kyvernoStackChartNamed(t, "kates"), report, nil); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(path)
	text := string(b)
	for _, want := range []string{"tag: \"1.0\"", "(--label team=<value> sets it)", "# kyvernoPolicy:\n#   exceptions:\n#     - policyName: require-team\n#       ruleNames: [check-team]\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("file lacks %q:\n%s", want, text)
		}
	}
}

func TestWriteKyvernoValuesWritesNothingWhenEveryRulePasses(t *testing.T) {
	dir := t.TempDir()
	path, err := writeKyvernoValues(dir, kyvernoStackChartNamed(t, "kates"), kyvernoCheckReport{Chart: "kates"}, map[string]string{"team": "x"})
	if path != "" || err != nil {
		t.Errorf("path %q, err %v", path, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("wrote %v", entries)
	}
}

// The label values of each stack chart are the ones the CI policy check
// proves reach every pod: scripts/check-chart-policies.sh's label_args.
func TestKyvernoStackLabelKeysMatchTheChartPolicyCheck(t *testing.T) {
	b, err := os.ReadFile("../../scripts/check-chart-policies.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	body := script[strings.Index(script, "label_args() {"):]
	body = body[:strings.Index(body, "\n}\n")]
	body = strings.ReplaceAll(body, "$kps", "kube-prometheus-stack")
	keysOf := func(chart string) []string {
		i := strings.Index(body, "\n    "+chart+")")
		if i < 0 {
			i = strings.Index(body, "\n    *)")
		}
		section := body[i:]
		section = section[strings.Index(section, "--set-string ")+len("--set-string "):]
		section = section[:strings.IndexAny(section, "\" ")]
		var keys []string
		for _, kv := range strings.Split(section, ",") {
			keys = append(keys, strings.TrimSuffix(kv, ".team=ci"))
		}
		return keys
	}
	for _, chart := range deployCharts {
		if got := keysOf(chart); !slices.Equal(chartPodLabelKeys(chart), got) {
			t.Errorf("%s: chartPodLabelKeys %q, label_args %q", chart, chartPodLabelKeys(chart), got)
		}
	}
}

// A policy's message can't break out of its comment into values.
func TestWriteKyvernoValuesKeepsAMessageInItsComment(t *testing.T) {
	dir := t.TempDir()
	report := kyvernoCheckReport{Chart: "kafka-cluster", Namespace: "kafka", Findings: []kyvernoFinding{
		{Result: "fail", Policy: "p", Rule: "r", Message: "no\rinjected: true also: true\u0085and: true\xffbad: true", Resources: []string{"Pod/a"}},
	}}
	path, err := writeKyvernoValues(dir, kyvernoStackChartNamed(t, "kafka-cluster"), report, nil)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	b, _ := os.ReadFile(path)
	if err := yaml.Unmarshal(b, &values); err != nil || len(values) != 0 {
		t.Errorf("values %v, err %v, from:\n%q", values, err, b)
	}
}
