package detect

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/klster/kates-cli/output"
)

func RenderTUI(report *DetectReport) {
	output.Header("Cluster Identity")
	output.KeyValue("Context:", report.Context)
	output.KeyValue("Server:", report.Server)
	output.KeyValue("Provider:", report.Provider)
	output.KeyValue("Kubernetes:", report.K8sVersion)
	output.KeyValue("Helm:", report.HelmVersion)

	output.Header("Node Details")
	if len(report.Nodes) > 0 {
		headers := []string{"NAME", "ZONE", "ROLES", "CPU", "MEMORY", "RUNTIME", "KUBELET"}
		var rows [][]string
		for _, n := range report.Nodes {
			rows = append(rows, []string{
				n.Name, n.Zone, n.Roles, fmt.Sprintf("%dm", n.CPU), fmt.Sprintf("%dGi", n.MemoryGi), n.Runtime, n.Kubelet,
			})
		}
		output.Table(headers, rows)
		output.Success(fmt.Sprintf("Nodes: %d total", len(report.Nodes)))
	} else {
		output.Warn("No nodes found")
	}

	output.Header("Per-Zone Capacity")
	if len(report.Zones) > 0 {
		headers := []string{"ZONE", "NODES", "CPU", "MEMORY"}
		var rows [][]string
		for _, z := range report.Zones {
			rows = append(rows, []string{
				z.Name, strconv.Itoa(z.Nodes), fmt.Sprintf("%dm", z.CPUAllocatable), fmt.Sprintf("%dGi", z.MemAllocatableGi),
			})
		}
		output.Table(headers, rows)
		output.Success(fmt.Sprintf("Zones: %d", len(report.Zones)))
	} else {
		output.Warn("No zone labels found on nodes")
	}

	// ── Capacity Analysis ────────────────────────────────────────────────
	cb := report.Capacity
	if cb.TotalCPU > 0 {
		output.Header("Cluster Capacity Analysis")
		utilPct := int(cb.UtilizationPct * 100)
		output.KeyValue("Total:", fmt.Sprintf("%d CPU, %d GiB", cb.TotalCPU/1000, cb.TotalMem))
		output.KeyValue("In use:", fmt.Sprintf("%dm CPU (%d%%), %d GiB", cb.UsedCPU, utilPct, cb.UsedMem))
		output.KeyValue("Available:", fmt.Sprintf("%dm CPU, %d GiB", cb.AvailableCPU, cb.AvailableMem))
		output.KeyValue("Kafka budget:", fmt.Sprintf("%dm CPU (%d%%), %d GiB (%d%%)",
			cb.KafkaCPU, int((1.0-cb.ReservePct)*100),
			cb.KafkaMem, int((1.0-cb.ReservePct)*100)))

		if len(report.Zones) > 0 {
			zHeaders := []string{"ZONE", "NODES", "AVAIL CPU", "AVAIL MEM"}
			var zRows [][]string
			for _, z := range report.Zones {
				zRows = append(zRows, []string{
					z.Name, strconv.Itoa(z.Nodes),
					fmt.Sprintf("%dm", int(float64(z.CPUAllocatable)*(1.0-cb.ReservePct))),
					fmt.Sprintf("%dGi", int(float64(z.MemAllocatableGi)*(1.0-cb.ReservePct))),
				})
			}
			output.Table(zHeaders, zRows)
			if cb.WeakestZone != "" {
				output.Warn(fmt.Sprintf("Bottleneck zone: %s (%dm CPU, %dGi mem)", cb.WeakestZone, cb.WeakestZoneCPU, cb.WeakestZoneMem))
			}
		}

		output.Success(fmt.Sprintf("Profile: %s (per-broker: %dm CPU, %s mem, %s storage)",
			cb.Profile, cb.BrokerCPU, formatMem(cb.BrokerMem), cb.BrokerStorage))
	}

	output.Header("Resource Budget")
	bHeaders := []string{"COMPONENT", "PODS", "CPU", "MEMORY"}
	bRows := [][]string{
		{"Controllers", fmt.Sprintf("%d", cb.ControllerReplicas), fmt.Sprintf("%dm", report.Budget.CtrlCPU), fmt.Sprintf("%dGi", report.Budget.CtrlMem)},
		{"Brokers (×3 zones)", fmt.Sprintf("%d", cb.BrokerReplicas*max(len(report.Zones), 1)), fmt.Sprintf("%dm", report.Budget.BrokerCPU), fmt.Sprintf("%dGi", report.Budget.BrokerMem)},
		{"Operators + Exporter", "3", fmt.Sprintf("%dm", report.Budget.OtherCPU), fmt.Sprintf("%dGi", report.Budget.OtherMem)},
		{"TOTAL REQUIRED", "", fmt.Sprintf("%dm", report.Budget.NeedCPU), fmt.Sprintf("%dGi", report.Budget.NeedMem)},
		{"CLUSTER AVAILABLE", strconv.Itoa(len(report.Nodes)), fmt.Sprintf("%dm", report.Budget.TotalCPU), fmt.Sprintf("%dGi", report.Budget.TotalMem)},
	}
	output.Table(bHeaders, bRows)

	if report.Budget.Sufficient {
		output.Success(fmt.Sprintf("Resources sufficient (%dm CPU / %dGi available)", report.Budget.TotalCPU, report.Budget.TotalMem))
	} else {
		output.Error(fmt.Sprintf("Insufficient resources (need %dm CPU, %dGi memory)", report.Budget.NeedCPU, report.Budget.NeedMem))
	}

	output.Header("Storage Compatibility")
	if len(report.Storage) > 0 {
		sHeaders := []string{"NAME", "PROVISIONER", "BINDING", "RECLAIM", "DEFAULT", "EXPAND"}
		var sRows [][]string
		for _, sc := range report.Storage {
			def := "✗"
			if sc.IsDefault {
				def = "PASS"
			}
			expand := "✗"
			if sc.AllowExpansion {
				expand = "✓"
			}
			sRows = append(sRows, []string{
				sc.Name, sc.Provisioner, sc.BindingMode, sc.ReclaimPolicy, def, expand,
			})
		}
		output.Table(sHeaders, sRows)
		output.Success(fmt.Sprintf("StorageClasses: %d available", len(report.Storage)))

		// PV summary
		if report.StorageAudit.PVCount > 0 {
			output.Success(fmt.Sprintf("PersistentVolumes: %d total (%d bound, %s capacity)",
				report.StorageAudit.PVCount, report.StorageAudit.PVBoundCount, report.StorageAudit.PVTotalCapacity))
		}
		if len(report.StorageAudit.CSIDrivers) > 0 {
			output.Success(fmt.Sprintf("CSI Drivers: %s", strings.Join(report.StorageAudit.CSIDrivers, ", ")))
		}
	} else {
		output.Error("No StorageClasses found")
	}

	output.Header("Existing Kafka Resources")
	output.KeyValue("Kafka clusters:", strconv.Itoa(report.ExistingKafka.KafkaClusters))
	output.KeyValue("KafkaNodePools:", strconv.Itoa(report.ExistingKafka.KafkaNodePools))
	output.KeyValue("KafkaTopics:", strconv.Itoa(report.ExistingKafka.KafkaTopics))
	output.KeyValue("KafkaUsers:", strconv.Itoa(report.ExistingKafka.KafkaUsers))
	output.KeyValue("PVCs:", fmt.Sprintf("%d (%d bound)", report.ExistingKafka.PVCs, report.ExistingKafka.BoundPVCs))
	output.KeyValue("Helm release:", report.ExistingKafka.HelmRelease)

	if report.ExistingKafka.KafkaClusters > 0 {
		output.Warn("Existing Kafka deployment detected — upgrade mode recommended")

		// Kafka Cluster Health
		h := report.ExistingKafka.Health
		if h.Name != "" {
			output.Header("Kafka Cluster Health")
			version := h.Version
			if version == "" {
				version = "unknown"
			}
			output.KeyValue("Cluster:", fmt.Sprintf("%s (Kafka %s)", h.Name, version))
			output.KeyValue("Replicas:", fmt.Sprintf("%d/%d ready", h.ReadyReplicas, h.Replicas))

			// Conditions
			for _, c := range h.Conditions {
				if c.Type == "Ready" && c.Status == "True" {
					output.Success(fmt.Sprintf("Condition: %s=%s", c.Type, c.Status))
				} else if c.Type == "Ready" {
					output.Error(fmt.Sprintf("Condition: %s=%s (%s)", c.Type, c.Status, c.Reason))
				}
			}

			// Listeners table
			if len(h.Listeners) > 0 {
				lHeaders := []string{"LISTENER", "TYPE", "PORT", "TLS"}
				var lRows [][]string
				for _, l := range h.Listeners {
					tls := "✗"
					if l.TLS {
						tls = "✓"
					}
					lRows = append(lRows, []string{l.Name, l.Type, strconv.Itoa(l.Port), tls})
				}
				output.Table(lHeaders, lRows)
			}
		}
	} else {
		output.Success("No existing Kafka deployment — clean install")
	}

	output.Header("Strimzi Operator")
	if report.Strimzi.Running {
		output.KeyValue("Namespace:", report.Strimzi.Namespace)
		output.KeyValue("Image:", report.Strimzi.Image)
		output.KeyValue("Replicas:", fmt.Sprintf("%d/%d ready", report.Strimzi.ReadyReplicas, report.Strimzi.TotalReplicas))
		output.Success("Strimzi operator: running")
	} else {
		if report.Strimzi.CRDsPresent {
			output.Warn("Strimzi CRDs present but operator not running")
		} else {
			output.Warn("Strimzi not installed — chart will install operator subchart")
		}
	}

	output.Header("Monitoring Stack")
	if report.Monitoring.PodMonitorCRD {
		output.Success("PodMonitor CRD: present")
	} else {
		output.Warn("PodMonitor CRD: not found")
	}
	if report.Monitoring.PrometheusRuleCRD {
		output.Success("PrometheusRule CRD: present")
	} else {
		output.Warn("PrometheusRule CRD: not found")
	}
	if report.Monitoring.GrafanaDeployed {
		output.Success("Grafana: deployed in monitoring")
	} else {
		output.Warn("Grafana: not found")
	}
	output.Success(fmt.Sprintf("Release label: %s", report.Monitoring.ReleaseLabel))

	output.Header("Network & Connectivity")
	output.Success(fmt.Sprintf("CNI: %s", report.Network.CNI))
	if report.Network.CoreDNSRunning > 0 {
		output.Success(fmt.Sprintf("CoreDNS: %d replica(s) running", report.Network.CoreDNSRunning))
	} else {
		output.Warn("CoreDNS: not detected")
	}
	output.Success(fmt.Sprintf("Cluster domain: %s", report.Network.ClusterDomain))
	output.Success(fmt.Sprintf("Local DNS suffix: svc.%s", report.Network.ClusterDomain))
	output.Success(fmt.Sprintf("Pod CIDR: %s", report.Network.PodCIDR))
	output.Success(fmt.Sprintf("Service CIDR: %s", report.Network.ServiceCIDR))

	// ── Admission Controllers ────────────────────────────────────────────────
	output.Header("Admission Controllers")
	if report.Admission.Kyverno.Installed {
		version := report.Admission.Kyverno.Version
		if version == "" {
			version = "unknown"
		}
		output.Success(fmt.Sprintf("Kyverno: running in %s (v%s)", report.Admission.Kyverno.Namespace, version))
		output.KeyValue("Cluster policies:", fmt.Sprintf("%d total, %d kafka-relevant",
			len(report.Admission.Kyverno.ClusterPolicies),
			len(report.Admission.Kyverno.KafkaRelevant)))
	} else {
		output.Hint("Kyverno: not installed")
	}
	if report.Admission.Gatekeeper.Installed {
		output.Success(fmt.Sprintf("OPA Gatekeeper: running in %s (%d constraints)",
			report.Admission.Gatekeeper.Namespace,
			len(report.Admission.Gatekeeper.Constraints)))
	} else {
		output.Hint("OPA Gatekeeper: not installed")
	}

	// ── Kyverno Policies (kafka-relevant) ────────────────────────────────────
	if report.Admission.Kyverno.Installed && len(report.Admission.Kyverno.KafkaRelevant) > 0 {
		output.Header("Kyverno Policies (kafka namespace)")
		pHeaders := []string{"POLICY", "ACTION", "CATEGORY", "RULES"}
		var pRows [][]string
		for _, p := range report.Admission.Kyverno.KafkaRelevant {
			cat := p.Category
			if cat == "" {
				cat = "—"
			}
			pRows = append(pRows, []string{
				p.Name,
				strings.ToUpper(p.Action),
				cat,
				strconv.Itoa(len(p.Rules)),
			})
		}
		output.Table(pHeaders, pRows)

		// Constraint impact analysis
		c := report.Admission.Kyverno.Constraints
		if c.EmptyPodSelectorBlocked {
			output.Warn("Empty podSelector blocked — chart uses explicit selectors (safe)")
		}
		if c.ResourceLimitsRequired {
			output.Warn("Resource limits required — verify values.yaml has limits set for all components")
		}
		if c.RunAsRootBlocked {
			output.Success("Run-as-non-root enforced — Strimzi pods comply by default")
		}
		if c.LatestTagBlocked {
			output.Success("Latest tag blocked — chart uses pinned image versions")
		}
		if c.PrivilegedBlocked {
			output.Success("Privileged containers blocked — Kafka does not require privilege")
		}
		if c.HostNetworkBlocked {
			output.Success("Host networking blocked — Kafka uses ClusterIP networking")
		}
	}

	// ── Existing NetworkPolicies ─────────────────────────────────────────────
	if report.NetPolAudit.TotalCount > 0 {
		output.Header("NetworkPolicies (kafka namespace)")
		npHeaders := []string{"NAME", "SELECTOR", "TYPES", "IN/OUT", "MANAGED BY"}
		var npRows [][]string
		for _, np := range report.NetPolAudit.Existing {
			sel := np.PodSelector
			if len(sel) > 45 {
				sel = sel[:42] + "..."
			}
			types := strings.Join(np.PolicyTypes, ",")
			rules := fmt.Sprintf("%d/%d", np.IngressRules, np.EgressRules)
			npRows = append(npRows, []string{np.Name, sel, types, rules, np.ManagedBy})
		}
		output.Table(npHeaders, npRows)

		helmCount := 0
		manualCount := 0
		for _, np := range report.NetPolAudit.Existing {
			if np.ManagedBy != "manual" {
				helmCount++
			} else {
				manualCount++
			}
		}
		if helmCount > 0 {
			output.Success(fmt.Sprintf("%d policies managed by Helm", helmCount))
		}
		if manualCount > 0 {
			output.Warn(fmt.Sprintf("%d manually-managed policies detected", manualCount))
		}
		if report.NetPolAudit.HasDefaultDeny {
			output.Success("Default-deny policy: applied")
		}
	} else {
		output.Header("NetworkPolicies (kafka namespace)")
		output.Hint("No NetworkPolicies found in kafka namespace")
	}

	// ── Verdict ──────────────────────────────────────────────────────────────
	output.Header("3-AZ Kafka Compatibility Verdict")
	var cRows [][]string
	for _, c := range report.Verdict.Checks {
		status := "PASS"
		if !c.Status {
			status = "FAIL"
		}
		cRows = append(cRows, []string{c.Description, status, c.Detail})
	}
	output.Table([]string{"CHECK", "STATUS", "DETAIL"}, cRows)

	if report.Verdict.Fails == 0 && report.Verdict.Warns == 0 {
		output.Banner("RESULT: COMPATIBLE", "Cluster can run a 3-AZ Kafka deployment")
	} else if report.Verdict.Fails == 0 {
		output.Banner("RESULT: PARTIAL", fmt.Sprintf("Compatible with %d warning(s)", report.Verdict.Warns))
	} else {
		output.Banner("RESULT: INCOMPATIBLE", fmt.Sprintf("%d check(s) failed", report.Verdict.Fails))
	}

	// ── Remediation Hints ────────────────────────────────────────────────
	hints := GenerateRemediation(report)
	if len(hints) > 0 {
		output.Header("Remediation Hints")
		for _, h := range hints {
			switch h.Severity {
			case "critical":
				output.Error(h.Summary)
			case "warning":
				output.Warn(h.Summary)
			default:
				output.Hint(h.Summary)
			}
			for _, cmd := range h.Commands {
				fmt.Printf("    → %s\n", cmd)
			}
			if h.DocURL != "" {
				fmt.Printf("    📖 %s\n", h.DocURL)
			}
			fmt.Println()
		}
	}
}
