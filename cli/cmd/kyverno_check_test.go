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
		if got := read("kates.yaml"); got != "kind: Deployment\n" {
			t.Errorf("kates.yaml = %q", got)
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
	buf := output.ResetForTesting()
	var err error
	out := captureStdout(t, func() { err = kyvernoCheckCmd.RunE(kyvernoCheckCmd, nil) })
	var report kyvernoCheckReport
	if jerr := json.Unmarshal([]byte(out+buf.String()), &report); jerr != nil {
		t.Fatalf("not a JSON report (err %v): %v\n%s%s", err, jerr, out, buf.String())
	}
	return report, err
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
	render := "---\n# Source: x\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: kates\n" +
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
