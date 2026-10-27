package detect

import (
	"fmt"
	"strconv"
	"strings"
)

func securitySection(r *DetectReport) Section {
	sec := r.Security
	s := Section{ID: "security", Title: "Security"}
	rbac := Fact{Label: "Wildcard RBAC", Value: "no wildcard rules in the kafka namespace Roles", Status: StatusOK}
	if sec.HasExcessivePrivileges {
		rbac = Fact{Label: "Wildcard RBAC", Value: "wildcard rules found in the kafka namespace Roles", Status: StatusWarn}
	}
	soon := expiringCerts(sec.ExpiringCerts)
	certs := Fact{Label: "TLS certificates", Value: "none expire within 30 days", Status: StatusOK}
	if len(soon) > 0 {
		certs = Fact{Label: "TLS certificates", Value: fmt.Sprintf("%d expire within 30 days", len(soon)), Status: StatusWarn}
	}
	s.Facts = []Fact{
		{Label: "Pod Security admission", Value: orNone(sec.PSALabelEnforced)},
		{Label: "Kyverno enforcing", Value: yesNo(sec.KyvernoEnforced)},
		{Label: "Namespace permissions", Value: map[bool]string{true: "sufficient", false: "missing"}[sec.PermissionsOk], Status: okOr(sec.PermissionsOk, StatusFail)},
		rbac,
		certs,
		secretAuditFact(r.SecretAudit),
	}
	if len(soon) > 0 {
		t := Table{Headers: []string{"Secret", "Subject", "Expires", "Days left"}}
		for _, c := range soon {
			t.Rows = append(t.Rows, []Cell{cell(c.SecretName), cell(c.Subject), cell(c.ExpiryDate),
				{Text: strconv.Itoa(c.DaysLeft), Status: StatusWarn}})
		}
		s.Tables = []Table{t}
	}
	for _, f := range s.Facts {
		s.Status = worse(s.Status, f.Status)
	}
	return s
}

// secretAuditFact is what the Secret-creation probe found. A run that did
// not probe (read-only) is "skipped": the reports used to say "skipped",
// "not executed" and "Not audited" for the same thing, and skipped is the
// word the other probes use.
func secretAuditFact(a SecretCreationAudit) Fact {
	f := Fact{Label: "Secret creation"}
	switch {
	case a.NamespaceCreated && a.SecretCreated:
		f.Value, f.Status = "works (verified in a temporary namespace)", StatusOK
	case a.NamespaceCreated && a.BlockedByPolicy:
		f.Value, f.Status = "blocked by admission policy "+orUnknown(a.PolicyName), StatusFail
	case a.NamespaceCreated:
		f.Value, f.Status = "failed: "+a.ErrorMsg, StatusFail
	case a.ErrorMsg != "":
		f.Value, f.Status = "temporary namespace creation failed: "+a.ErrorMsg, StatusFail
	default:
		f.Value, f.Status = "skipped (the probe did not run)", StatusSkipped
	}
	return f
}

func admissionSection(r *DetectReport) Section {
	k, gk := r.Admission.Kyverno, r.Admission.Gatekeeper
	s := Section{ID: "admission", Title: "Admission Controllers"}
	if k.Installed {
		s.Facts = append(s.Facts,
			Fact{Label: "Kyverno", Value: fmt.Sprintf("running in %s (v%s)", k.Namespace, orUnknown(k.Version)), Status: StatusOK},
			Fact{Label: "Cluster policies", Value: fmt.Sprintf("%d total, %d kafka-relevant", len(k.ClusterPolicies), len(k.KafkaRelevant))})
	} else {
		s.Facts = append(s.Facts, Fact{Label: "Kyverno", Value: "not installed"})
	}
	if gk.Installed {
		s.Facts = append(s.Facts, Fact{Label: "OPA Gatekeeper", Value: fmt.Sprintf("running in %s (%d constraints)", gk.Namespace, len(gk.Constraints)), Status: StatusOK})
	} else {
		s.Facts = append(s.Facts, Fact{Label: "OPA Gatekeeper", Value: "not installed"})
	}
	if !k.Installed || len(k.KafkaRelevant) == 0 {
		return s
	}
	t := Table{Headers: []string{"Policy", "Action", "Category", "Rules"}}
	for _, p := range k.KafkaRelevant {
		cat := p.Category
		if cat == "" {
			cat = "—"
		}
		t.Rows = append(t.Rows, cells(p.Name, strings.ToUpper(p.Action), cat, strconv.Itoa(len(p.Rules))))
	}
	s.Tables = []Table{t}
	s.Notes = policyConstraintNotes(k.Constraints)
	return s
}

// policyConstraintNotes says what each Kyverno constraint means for the
// chart.
func policyConstraintNotes(c PolicyConstraints) []string {
	var notes []string
	for _, n := range []struct {
		on   bool
		text string
	}{
		{c.EmptyPodSelectorBlocked, "Empty podSelector blocked: the chart uses explicit selectors (safe)."},
		{c.ResourceLimitsRequired, "Resource limits required: check that values.yaml sets limits for every component."},
		{c.RunAsRootBlocked, "Run-as-non-root enforced: Strimzi pods comply by default."},
		{c.LatestTagBlocked, "Latest tag blocked: the chart pins image versions."},
		{c.PrivilegedBlocked, "Privileged containers blocked: Kafka needs no privilege."},
		{c.HostNetworkBlocked, "Host networking blocked: Kafka uses ClusterIP networking."},
	} {
		if n.on {
			notes = append(notes, n.text)
		}
	}
	return notes
}

func netpolSection(r *DetectReport) Section {
	a := r.NetPolAudit
	s := Section{ID: "netpol", Title: "NetworkPolicies (kafka namespace)"}
	if a.TotalCount == 0 {
		s.Paragraphs = []string{"No NetworkPolicies in the kafka namespace."}
		return s
	}
	t := Table{Headers: []string{"Name", "Selector", "Types", "In/Out", "Managed by"}}
	counts := map[string]int{}
	for _, np := range a.Existing {
		t.Rows = append(t.Rows, cells(np.Name, np.PodSelector, strings.Join(np.PolicyTypes, ","),
			fmt.Sprintf("%d/%d", np.IngressRules, np.EgressRules), np.ManagedBy))
		switch np.ManagedBy {
		case "strimzi", "manual":
			counts[np.ManagedBy]++
		default:
			counts["helm"]++
		}
	}
	s.Tables = []Table{t}
	s.Status = okOr(counts["manual"] == 0, StatusWarn)
	s.Facts = []Fact{
		{Label: "Managed by Helm", Value: strconv.Itoa(counts["helm"])},
		{Label: "Managed by Strimzi", Value: strconv.Itoa(counts["strimzi"])},
		{Label: "Managed by hand", Value: strconv.Itoa(counts["manual"]), Status: s.Status},
		{Label: "Default deny", Value: yesNo(a.HasDefaultDeny)},
	}
	return s
}

func checksSection(r *DetectReport) Section {
	s := Section{ID: "checks", Title: "Compatibility Checks", Status: VerdictOf(r.Verdict).Status}
	t := Table{Headers: []string{"Check", "Status", "Detail"}}
	for _, c := range r.Verdict.Checks {
		st := Cell{Text: "PASS", Status: StatusOK}
		if c.Warn {
			st = Cell{Text: "WARN", Status: StatusWarn}
		}
		if !c.Status {
			st = Cell{Text: "FAIL", Status: StatusFail}
		}
		t.Rows = append(t.Rows, []Cell{cell(c.Description), st, cell(c.Detail)})
	}
	s.Tables = []Table{t}
	if len(r.Verdict.Checks) == 0 {
		s.Paragraphs = []string{"No checks ran."}
	}
	return s
}

// remediationSection is GenerateRemediation's hints, worst first as it
// lists them.
func remediationSection(hints []Remediation) Section {
	s := Section{ID: "remediation", Title: "Remediation"}
	if len(hints) == 0 {
		s.Status = StatusOK
		s.Paragraphs = []string{"Nothing to remediate."}
		return s
	}
	for _, h := range hints {
		st := map[string]Status{"critical": StatusFail, "warning": StatusWarn}[h.Severity]
		s.Status = worse(s.Status, st)
		s.Hints = append(s.Hints, Hint{Title: h.Summary, Check: h.Check, Status: st, Commands: h.Commands, DocURL: h.DocURL})
	}
	return s
}
