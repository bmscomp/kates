package cmd

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

// policyLabels are the labels policies require, by policy, base rule and
// scope ("namespace" or "pod"): each key with the one exact value its
// pattern names, or "" when it names none (?*, a | b).
type policyLabels map[string]map[string]map[string]map[string]string

// policyLabelRules reads the labels the validate patterns of the policies
// read require: on namespaces, and on pods (a Pod rule, or the pod template
// of a controller). An anchored key, such as an optional =(key), is left out.
func policyLabelRules(policies [][]byte) policyLabels {
	out := policyLabels{}
	type match struct {
		Resources struct {
			Kinds []string `json:"kinds"`
		} `json:"resources"`
	}
	for _, raw := range policies {
		var p struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Rules []struct {
					Name  string `json:"name"`
					Match struct {
						match
						Any []match `json:"any"`
						All []match `json:"all"`
					} `json:"match"`
					Validate struct {
						Pattern map[string]any `json:"pattern"`
					} `json:"validate"`
				} `json:"rules"`
			} `json:"spec"`
		}
		if json.Unmarshal(raw, &p) != nil {
			continue
		}
		for _, rule := range p.Spec.Rules {
			if rule.Validate.Pattern == nil {
				continue
			}
			kinds := rule.Match.Resources.Kinds
			for _, m := range append(rule.Match.Any, rule.Match.All...) {
				kinds = append(kinds, m.Resources.Kinds...)
			}
			add := func(scope string, path ...string) {
				labels := requiredLabels(rule.Validate.Pattern, path...)
				if len(labels) == 0 {
					return
				}
				if out[p.Metadata.Name] == nil {
					out[p.Metadata.Name] = map[string]map[string]map[string]string{}
				}
				if out[p.Metadata.Name][rule.Name] == nil {
					out[p.Metadata.Name][rule.Name] = map[string]map[string]string{}
				}
				if out[p.Metadata.Name][rule.Name][scope] == nil {
					out[p.Metadata.Name][rule.Name][scope] = map[string]string{}
				}
				maps.Copy(out[p.Metadata.Name][rule.Name][scope], labels)
			}
			for _, kind := range kinds {
				switch kind[strings.LastIndex(kind, "/")+1:] { // v1/Pod
				case "Namespace":
					add("namespace", "metadata", "labels")
				case "Pod":
					add("pod", "metadata", "labels")
				case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
					add("pod", "spec", "template", "metadata", "labels")
				case "CronJob":
					add("pod", "spec", "jobTemplate", "spec", "template", "metadata", "labels")
				}
			}
		}
	}
	return out
}

// requiredLabels are the labels at path in pattern: each with its one exact,
// valid value, or "".
func requiredLabels(pattern map[string]any, path ...string) map[string]string {
	m := pattern
	for _, part := range path {
		next, ok := m[part].(map[string]any)
		if !ok {
			return nil
		}
		m = next
	}
	labels := map[string]string{}
	for k, v := range m {
		if strings.ContainsAny(k, "()") || validateNamespaceLabel(k, "") != nil {
			continue
		}
		value, _ := v.(string)
		if validateNamespaceLabel(k, value) != nil {
			value = ""
		}
		labels[k] = value
	}
	return labels
}

// withFindings is p updated from a check's reports. A rule that failed on a
// missing label sets every label its policy requires there: from --label,
// else the value p has, else the one value the pattern names (Kyverno names
// only the first missing label of a rule, so the policy is read for the
// rest). A label p already set that a policy still refuses, a label with no
// known value, and every other failed rule become the unresolved entries of
// the charts checked, replacing their old ones. Labels and values already
// in p are kept.
func (p deployPolicies) withFindings(reports []kyvernoCheckReport, cliLabels map[string]string, required policyLabels) deployPolicies {
	p.NamespaceLabels, p.PodLabels = maps.Clone(p.NamespaceLabels), maps.Clone(p.PodLabels)
	if p.NamespaceLabels == nil {
		p.NamespaceLabels = map[string]string{}
	}
	if p.PodLabels == nil {
		p.PodLabels = map[string]string{}
	}
	checked := map[string]bool{}
	for _, r := range reports {
		checked[r.Chart] = true
	}
	p.Unresolved = slices.DeleteFunc(slices.Clone(p.Unresolved), func(g policyGap) bool { return checked[g.Chart] })
	seen := map[string]bool{}
	gap := func(r kyvernoCheckReport, f kyvernoFinding, id, fix string) {
		if seen[id] {
			return
		}
		seen[id] = true
		p.Unresolved = append(p.Unresolved, policyGap{Chart: r.Chart, Namespace: r.Namespace,
			Policy: f.Policy, Rule: baseRule(f.Rule), Message: f.Message, Fix: fix})
	}
	for _, r := range reports {
		for _, f := range r.Findings {
			if f.Result == "warn" {
				continue
			}
			id := r.Chart + "\x00" + f.Policy + "\x00" + baseRule(f.Rule)
			key, scope := missingLabel(f)
			if scope == "" {
				fix := "a PolicyException of your own"
				switch {
				case f.Fix != "":
					fix = f.Fix + ", under values." + r.Chart
				case kyvernoChartHasExceptions(r.Chart):
					fix = "kyvernoPolicy.exceptions under values." + r.Chart + ", or a PolicyException of your own"
				}
				gap(r, f, id, fix)
				continue
			}
			labels, field := p.PodLabels, "podLabels."
			if scope == "namespace" {
				labels, field = p.NamespaceLabels, "namespaceLabels."
			}
			want := maps.Clone(required[f.Policy][baseRule(f.Rule)][scope])
			if want == nil {
				want = map[string]string{}
			}
			if _, ok := want[key]; !ok {
				want[key] = ""
			}
			for _, k := range slices.Sorted(maps.Keys(want)) {
				have, set := labels[k]
				switch {
				case cliLabels[k] != "":
					labels[k] = cliLabels[k]
				case k == key && set:
					// The render had it: the policy refused its value.
					gap(r, f, id+"\x00"+k, field+k+": the policy refuses "+have+"; set the value it wants (or --label "+k+"=<value>)")
				case set:
				case want[k] != "":
					labels[k] = want[k]
				default:
					gap(r, f, id+"\x00"+k, field+k+": the value the policy wants (or --label "+k+"=<value>)")
				}
			}
		}
	}
	if len(p.NamespaceLabels) == 0 {
		p.NamespaceLabels = nil
	}
	if len(p.PodLabels) == 0 {
		p.PodLabels = nil
	}
	return p
}
