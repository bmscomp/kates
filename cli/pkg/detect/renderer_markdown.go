package detect

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// RenderMarkdown writes the full detect report as a markdown document.
func RenderMarkdown(report *DetectReport, w io.Writer) {
	p := func(format string, args ...interface{}) {
		fmt.Fprintf(w, format+"\n", args...)
	}

	p("# Kafka Cluster Compatibility Report")
	p("")
	p("**Date:** %s", time.Now().UTC().Format(time.RFC3339))
	p("**Context:** %s | **Provider:** %s | **K8s:** %s | **Helm:** %s",
		report.Context, report.Provider, report.K8sVersion, report.HelmVersion)
	p("")

	// Nodes
	p("## Nodes (%d total)", len(report.Nodes))
	p("")
	if len(report.Nodes) > 0 {
		p("| Name | Zone | Roles | CPU | Memory | Kubelet |")
		p("|------|------|-------|-----|--------|---------|")
		for _, n := range report.Nodes {
			p("| %s | %s | %s | %dm | %dGi | %s |", n.Name, n.Zone, n.Roles, n.CPU, n.MemoryGi, n.Kubelet)
		}
	}
	p("")

	// Zones
	if len(report.Zones) > 0 {
		p("## Zones (%d)", len(report.Zones))
		p("")
		p("| Zone | Nodes | CPU | Memory |")
		p("|------|-------|-----|--------|")
		for _, z := range report.Zones {
			p("| %s | %d | %dm | %dGi |", z.Name, z.Nodes, z.CPUAllocatable, z.MemAllocatableGi)
		}
		p("")
	}

	// Storage
	p("## Storage")
	p("")
	if len(report.Storage) > 0 {
		p("| Name | Provisioner | Binding | Reclaim | Default | Expand |")
		p("|------|-------------|---------|---------|---------|--------|")
		for _, sc := range report.Storage {
			def := "✗"
			if sc.IsDefault {
				def = "✓"
			}
			expand := "✗"
			if sc.AllowExpansion {
				expand = "✓"
			}
			p("| %s | %s | %s | %s | %s | %s |", sc.Name, sc.Provisioner, sc.BindingMode, sc.ReclaimPolicy, def, expand)
		}
		if report.StorageAudit.PVCount > 0 {
			p("")
			p("**PVs:** %d total (%d bound, %s capacity)", report.StorageAudit.PVCount, report.StorageAudit.PVBoundCount, report.StorageAudit.PVTotalCapacity)
		}
		if len(report.StorageAudit.CSIDrivers) > 0 {
			p("**CSI Drivers:** %s", strings.Join(report.StorageAudit.CSIDrivers, ", "))
		}
	} else {
		p("⚠️ No StorageClasses found")
	}
	p("")

	// Strimzi
	p("## Strimzi Operator")
	p("")
	if report.Strimzi.Running {
		p("- **Status:** ✅ Running in `%s`", report.Strimzi.Namespace)
		p("- **Image:** `%s`", report.Strimzi.Image)
		p("- **Replicas:** %d/%d ready", report.Strimzi.ReadyReplicas, report.Strimzi.TotalReplicas)
		p("- **Health Verdict:** %s", report.Strimzi.Health.Status)
		if len(report.Strimzi.Health.WarningLogs) > 0 {
			p("- **Warning Logs:**")
			p("```")
			for _, log := range report.Strimzi.Health.WarningLogs {
				p("%s", log)
			}
			p("```")
		}
	} else if report.Strimzi.CRDsPresent {
		p("- **Status:** ⚠️ CRDs present but operator not running")
	} else {
		p("- **Status:** ❌ Not installed")
	}
	if len(report.Strimzi.Health.MissingCRDs) > 0 {
		p("- **Missing CRDs:** %s", strings.Join(report.Strimzi.Health.MissingCRDs, ", "))
	}
	if report.Strimzi.CapacityStatus != "" {
		p("- **Kafka Capacity:** %s", report.Strimzi.CapacityStatus)
	}
	p("")

	// Kafka Health
	if report.ExistingKafka.KafkaClusters > 0 {
		p("## Kafka Cluster Health")
		p("")
		h := report.ExistingKafka.Health
		if h.Name != "" {
			p("- **Cluster:** %s (Kafka %s)", h.Name, h.Version)
			p("- **Replicas:** %d/%d ready", h.ReadyReplicas, h.Replicas)
			if len(h.Listeners) > 0 {
				p("")
				p("| Listener | Type | Port | TLS |")
				p("|----------|------|------|-----|")
				for _, l := range h.Listeners {
					tls := "✗"
					if l.TLS {
						tls = "✓"
					}
					p("| %s | %s | %d | %s |", l.Name, l.Type, l.Port, tls)
				}
			}
		}
		p("")
	}

	// Admission
	p("## Admission Controllers")
	p("")
	if report.Admission.Kyverno.Installed {
		p("- **Kyverno:** ✅ Running in `%s` (v%s)", report.Admission.Kyverno.Namespace, report.Admission.Kyverno.Version)
		p("- **Cluster Policies:** %d total, %d kafka-relevant",
			len(report.Admission.Kyverno.ClusterPolicies), len(report.Admission.Kyverno.KafkaRelevant))
		if len(report.Admission.Kyverno.KafkaRelevant) > 0 {
			p("")
			p("| Policy | Action | Category |")
			p("|--------|--------|----------|")
			for _, pol := range report.Admission.Kyverno.KafkaRelevant {
				cat := pol.Category
				if cat == "" {
					cat = "—"
				}
				p("| %s | %s | %s |", pol.Name, strings.ToUpper(pol.Action), cat)
			}
		}
	} else {
		p("- **Kyverno:** not installed")
	}
	if report.Admission.Gatekeeper.Installed {
		p("- **OPA Gatekeeper:** ✅ Running in `%s` (%d constraints)",
			report.Admission.Gatekeeper.Namespace, len(report.Admission.Gatekeeper.Constraints))
	}
	// Active AZ Network Latency Matrix
	if len(report.Network.LatencyMatrix) > 0 {
		p("## Active Zone Network Latency Matrix")
		p("")
		zoneMap := make(map[string]bool)
		for _, r := range report.Network.LatencyMatrix {
			zoneMap[r.SourceZone] = true
			zoneMap[r.TargetZone] = true
		}
		var zones []string
		for z := range zoneMap {
			zones = append(zones, z)
		}

		header := "| Source \\ Target | "
		align := "|---| "
		for _, z := range zones {
			header += z + " | "
			align += "---| "
		}
		p(header)
		p(align)

		for _, src := range zones {
			row := "| **" + src + "** | "
			for _, dst := range zones {
				found := false
				for _, r := range report.Network.LatencyMatrix {
					if r.SourceZone == src && r.TargetZone == dst {
						if r.Success {
							row += fmt.Sprintf("%.2fms | ", r.AvgMs)
						} else {
							row += "❌ FAIL | "
						}
						found = true
						break
					}
				}
				if !found {
					row += "— | "
				}
			}
			p(row)
		}
		p("")
	}

	// Secrets Audit
	p("## Kubernetes Secrets Audit")
	p("")
	if report.SecretAudit.NamespaceCreated {
		if report.SecretAudit.SecretCreated {
			p("- **Namespace creation:** ✅ Succeeded (`kates-detect-secrets-*`)")
			p("- **Secret provisioning:** ✅ Functional")
		} else {
			p("- **Namespace creation:** ✅ Succeeded")
			p("- **Secret provisioning:** ❌ Failed")
			if report.SecretAudit.BlockedByPolicy {
				p("- **Policy blocker:** 🚫 Admission controller policy `%s` blocked secret creation", report.SecretAudit.PolicyName)
			} else {
				p("- **Error:** `%s`", report.SecretAudit.ErrorMsg)
			}
		}
	} else if report.SecretAudit.ErrorMsg != "" {
		p("- **Status:** ❌ Namespace creation failed")
		p("- **Error:** `%s`", report.SecretAudit.ErrorMsg)
	} else {
		p("- **Status:** ℹ️ Secret capability audit not executed")
	}
	p("")

	// NetworkPolicies
	if report.NetPolAudit.TotalCount > 0 {
		p("## NetworkPolicies (kafka namespace)")
		p("")
		p("| Name | Selector | Types | In/Out | Managed By |")
		p("|------|----------|-------|--------|------------|")
		for _, np := range report.NetPolAudit.Existing {
			types := strings.Join(np.PolicyTypes, ",")
			p("| %s | `%s` | %s | %d/%d | %s |", np.Name, np.PodSelector, types, np.IngressRules, np.EgressRules, np.ManagedBy)
		}
		p("")
	}

	// Verdict
	p("## Compatibility Verdict")
	p("")
	p("| Check | Status | Detail |")
	p("|-------|--------|--------|")
	for _, c := range report.Verdict.Checks {
		status := "✅ PASS"
		if !c.Status {
			status = "❌ FAIL"
		}
		p("| %s | %s | %s |", c.Description, status, c.Detail)
	}
	p("")

	if report.Verdict.Fails == 0 && report.Verdict.Warns == 0 {
		p("### ✅ RESULT: COMPATIBLE")
		p("Cluster can run a 3-AZ Kafka deployment.")
	} else if report.Verdict.Fails == 0 {
		p("### ⚠️ RESULT: PARTIAL")
		p("Compatible with %d warning(s).", report.Verdict.Warns)
	} else {
		p("### ❌ RESULT: INCOMPATIBLE")
		p("%d check(s) failed.", report.Verdict.Fails)
	}

	// Remediation
	hints := GenerateRemediation(report)
	if len(hints) > 0 {
		p("")
		p("## Remediation Hints")
		p("")
		for _, h := range hints {
			icon := "ℹ️"
			if h.Severity == "critical" {
				icon = "❌"
			} else if h.Severity == "warning" {
				icon = "⚠️"
			}
			p("### %s %s", icon, h.Summary)
			if len(h.Commands) > 0 {
				p("```bash")
				for _, cmd := range h.Commands {
					p("%s", cmd)
				}
				p("```")
			}
			if h.DocURL != "" {
				p("📖 [Documentation](%s)", h.DocURL)
			}
			p("")
		}
	}

	// Resource Budget
	p("## Resource Budget")
	p("")
	p("| Component | CPU | Memory |")
	p("|-----------|-----|--------|")
	p("| Controllers | %dm | %dGi |", report.Budget.CtrlCPU, report.Budget.CtrlMem)
	p("| Brokers (×3 zones) | %dm | %dGi |", report.Budget.BrokerCPU*3, report.Budget.BrokerMem*3)
	p("| Operators + Exporter | %dm | %dGi |", report.Budget.OtherCPU, report.Budget.OtherMem)
	p("| **TOTAL REQUIRED** | **%dm** | **%dGi** |", report.Budget.NeedCPU, report.Budget.NeedMem)
	p("| **CLUSTER AVAILABLE** | **%dm** | **%dGi** |", report.Budget.TotalCPU, report.Budget.TotalMem)
	p("")

	sufficient := "✅ Sufficient"
	if !report.Budget.Sufficient {
		sufficient = "❌ Insufficient"
	}
	p("**Status:** %s", sufficient)
	p("")
	p("---")
	p("*Generated by kates detect v" + strconv.Itoa(report.HelmMajor) + "*")
}
