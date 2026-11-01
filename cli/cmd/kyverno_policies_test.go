package cmd

import (
	"reflect"
	"strings"
	"testing"
)

func TestPolicyLabelRulesReadEveryRequiredLabel(t *testing.T) {
	policies := [][]byte{
		[]byte(`{"metadata":{"name":"labels"},"spec":{"rules":[
			{"name":"ns","match":{"any":[{"resources":{"kinds":["Namespace"]}}]},
			 "validate":{"pattern":{"metadata":{"labels":{"cost-center":"cc-42","owner":"?*"}}}}},
			{"name":"pod","match":{"resources":{"kinds":["v1/Pod"]}},
			 "validate":{"pattern":{"metadata":{"labels":{"team":"payments","tier":"web | data","=(env)":"prod"}}}}},
			{"name":"deploy","match":{"all":[{"resources":{"kinds":["Deployment"]}}]},
			 "validate":{"pattern":{"spec":{"template":{"metadata":{"labels":{"zone":"eu"}}}}}}},
			{"name":"deny","match":{"any":[{"resources":{"kinds":["Pod"]}}]},
			 "validate":{"message":"no pattern","deny":{}}}
		]}}`),
		[]byte(`not json`),
	}
	want := policyLabels{"labels": {
		"ns":     {"namespace": {"cost-center": "cc-42", "owner": ""}},
		"pod":    {"pod": {"team": "payments", "tier": ""}},
		"deploy": {"pod": {"zone": "eu"}},
	}}
	if got := policyLabelRules(policies); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestMissingLabelOnANamespace(t *testing.T) {
	for _, c := range []struct {
		resources  []string
		key, scope string
	}{
		{[]string{"Namespace/team"}, "team", "namespace"},
		{[]string{"Pod/a"}, "team", "pod"},
		{[]string{"Namespace/team", "Pod/a"}, "", ""},
		{nil, "", ""},
	} {
		key, scope := missingLabel(kyvernoFinding{Message: "rule failed at path /metadata/labels/team/", Resources: c.resources})
		if key != c.key || scope != c.scope {
			t.Errorf("%q: %q %q, want %q %q", c.resources, key, scope, c.key, c.scope)
		}
	}
}

func TestPoliciesWithFindings(t *testing.T) {
	label := func(policy, key, resource string) kyvernoFinding {
		return kyvernoFinding{Result: "fail", Policy: policy, Rule: "autogen-check",
			Message: "rule failed at path /spec/template/metadata/labels/" + key + "/", Resources: []string{resource}}
	}
	p := deployPolicies{
		PodLabels: map[string]string{"refused": "old"},
		Unresolved: []policyGap{
			{Chart: "kates", Policy: "stale"},
			{Chart: "monitoring", Policy: "kept"},
		},
	}
	required := policyLabels{
		// Kyverno names only team; the policy also wants tier and owner.
		"pod-labels": {"check": {"pod": {"team": "", "tier": "data", "owner": ""}}},
		"ns-labels":  {"check": {"namespace": {"cost-center": "cc-42"}}},
		"refuses":    {"check": {"pod": {"refused": "new"}}},
	}
	reports := []kyvernoCheckReport{{Chart: "kates", Namespace: "kates", Findings: []kyvernoFinding{
		label("pod-labels", "team", "Deployment/a"),
		label("refuses", "refused", "Deployment/a"),
		label("unread", "zone", "Deployment/a"),
		{Result: "fail", Policy: "ns-labels", Rule: "check", Message: "rule failed at path /metadata/labels/cost-center/", Resources: []string{"Namespace/kates"}},
		{Result: "fail", Policy: "no-hostpath", Rule: "check", Message: "HostPath volumes are forbidden."},
		{Result: "fail", Policy: "no-hostpath", Rule: "autogen-check", Message: "HostPath volumes are forbidden."},
		{Result: "warn", Policy: "audit-only", Rule: "check", Message: "advice"},
	}}}

	got := p.withFindings(reports, map[string]string{"team": "payments"}, required)

	if want := map[string]string{"team": "payments", "tier": "data", "refused": "old"}; !reflect.DeepEqual(got.PodLabels, want) {
		t.Errorf("podLabels = %v, want %v", got.PodLabels, want)
	}
	if want := map[string]string{"cost-center": "cc-42"}; !reflect.DeepEqual(got.NamespaceLabels, want) {
		t.Errorf("namespaceLabels = %v, want %v", got.NamespaceLabels, want)
	}
	var gaps []string
	for _, g := range got.Unresolved {
		gaps = append(gaps, g.Policy+":"+g.Fix[:strings.IndexAny(g.Fix+":", ":,")])
	}
	// The kates chart's old gaps go, monitoring's stay; one gap per rule and label.
	want := []string{"kept:", "pod-labels:podLabels.owner", "refuses:podLabels.refused", "unread:podLabels.zone", "no-hostpath:kyvernoPolicy.exceptions under values.kates"}
	if !reflect.DeepEqual(gaps, want) {
		t.Errorf("unresolved = %q\nwant %q", gaps, want)
	}
	if p.PodLabels["team"] != "" || len(p.Unresolved) != 2 {
		t.Error("withFindings changed the policies it was given")
	}
}
