package detect

import "fmt"

// Remediation represents an actionable fix for a failing or warning verdict check.
type Remediation struct {
	Check    string   // maps to CheckResult.Description
	Severity string   // "critical", "warning", "info"
	Summary  string   // one-line explanation
	Commands []string // exact commands to run
	DocURL   string   // link to relevant docs
}

// GenerateRemediation inspects the report and produces actionable hints.
func GenerateRemediation(report *DetectReport) []Remediation {
	var hints []Remediation

	// Strimzi CRDs
	if !report.Strimzi.CRDsPresent {
		hints = append(hints, Remediation{
			Check:    "Strimzi CRDs installed",
			Severity: "critical",
			Summary:  "Strimzi Custom Resource Definitions are not installed",
			Commands: []string{
				"helm install strimzi oci://quay.io/strimzi-helm/strimzi-kafka-operator --namespace kafka --create-namespace",
			},
			DocURL: "https://strimzi.io/docs/operators/latest/deploying",
		})
	} else if !report.Strimzi.Running {
		hints = append(hints, Remediation{
			Check:    "Strimzi operator running",
			Severity: "warning",
			Summary:  "Strimzi CRDs are present but the operator is not running",
			Commands: []string{
				"kubectl get deployment -A | grep strimzi  # check operator status",
				"kubectl rollout restart deployment/strimzi-cluster-operator -n " + report.Strimzi.Namespace,
			},
			DocURL: "https://strimzi.io/docs/operators/latest/deploying",
		})
	}

	// Availability zones
	if len(report.Zones) < 3 {
		hints = append(hints, Remediation{
			Check:    "≥ 3 availability zones",
			Severity: "critical",
			Summary:  fmt.Sprintf("Only %d zone(s) found — 3-AZ Kafka requires at least 3 zones", len(report.Zones)),
			Commands: []string{
				"kubectl get nodes --show-labels | grep topology.kubernetes.io/zone",
				"kubectl label node <node-name> topology.kubernetes.io/zone=<zone>  # if labels are missing",
			},
		})
	}

	// StorageClass
	if len(report.Storage) == 0 {
		hints = append(hints, Remediation{
			Check:    "StorageClass available",
			Severity: "critical",
			Summary:  "No StorageClass found — Kafka PVCs will fail to bind",
			Commands: []string{
				"kubectl apply -f config/storage/  # apply zone-specific storage classes",
				"kubectl get sc  # verify",
			},
		})
	}

	// Insufficient resources
	if !report.Budget.Sufficient {
		cpuGap := report.Budget.NeedCPU - report.Budget.TotalCPU
		memGap := report.Budget.NeedMem - report.Budget.TotalMem
		cmds := []string{}
		if cpuGap > 0 || memGap > 0 {
			cmds = append(cmds, fmt.Sprintf("# Short by %dm CPU, %dGi memory", max(cpuGap, 0), max(memGap, 0)))
		}
		cmds = append(cmds,
			"# Option 1: Add nodes to the cluster",
			"# Option 2: Reduce broker resources in values.yaml:",
			"#   brokerDefaults.resources.requests.cpu: 500m",
			"#   brokerDefaults.resources.requests.memory: 1Gi",
		)
		hints = append(hints, Remediation{
			Check:    "Broker resources fit (all zones)",
			Severity: "critical",
			Summary:  "Cluster does not have enough CPU/memory for the requested Kafka deployment",
			Commands: cmds,
		})
	}

	// DNS
	if report.Network.CoreDNSRunning == 0 {
		hints = append(hints, Remediation{
			Check:    "DNS resolution",
			Severity: "critical",
			Summary:  "CoreDNS not detected — Kafka brokers require DNS for discovery",
			Commands: []string{
				"kubectl get deployment -n kube-system | grep -i dns",
				"kubectl rollout restart deployment/coredns -n kube-system",
			},
		})
	}

	// RBAC
	for _, c := range report.Verdict.Checks {
		if c.Description == "RBAC permissions" && !c.Status {
			hints = append(hints, Remediation{
				Check:    "RBAC permissions",
				Severity: "critical",
				Summary:  "Insufficient permissions to create resources in the kafka namespace",
				Commands: []string{
					"kubectl auth can-i create deployments -n kafka  # test current permissions",
					"kubectl create namespace kafka --dry-run=client -o yaml | kubectl apply -f -",
					"kubectl create rolebinding kafka-admin --clusterrole=admin --user=$(kubectl config current-context) -n kafka",
				},
			})
			break
		}
	}

	// Prometheus
	if !report.Monitoring.PodMonitorCRD {
		hints = append(hints, Remediation{
			Check:    "Prometheus monitoring",
			Severity: "warning",
			Summary:  "PodMonitor CRD not found — Kafka metrics will not be scraped",
			Commands: []string{
				"helm install monitoring prometheus-community/kube-prometheus-stack -n monitoring --create-namespace",
			},
			DocURL: "https://prometheus-operator.dev/docs/getting-started/introduction/",
		})
	}

	// Kyverno empty-selector warning
	if report.Admission.Kyverno.Installed && report.Admission.Kyverno.Constraints.EmptyPodSelectorBlocked {
		hints = append(hints, Remediation{
			Check:    "Kyverno NetworkPolicy safe",
			Severity: "info",
			Summary:  "Kyverno blocks empty podSelector — chart uses explicit selectors (already safe)",
			Commands: []string{
				"# No action required — the Helm chart generates Kyverno-compliant NetworkPolicies",
				"# The default-deny and allow-dns policies use app.kubernetes.io/part-of=strimzi-krafter",
			},
		})
	}

	// Resource limits required by Kyverno
	if report.Admission.Kyverno.Installed && report.Admission.Kyverno.Constraints.ResourceLimitsRequired {
		hints = append(hints, Remediation{
			Check:    "Resource limits required",
			Severity: "warning",
			Summary:  "Kyverno enforces resource limits — ensure all components have limits in values.yaml",
			Commands: []string{
				"# Verify limits are set for all components in your values.yaml:",
				"grep -A 4 'limits:' charts/kafka-cluster/values.yaml",
			},
		})
	}

	// Existing Kafka deployment
	if report.ExistingKafka.KafkaClusters > 0 {
		hints = append(hints, Remediation{
			Check:    "Existing deployment",
			Severity: "warning",
			Summary:  "An existing Kafka cluster is running — use helm upgrade instead of install",
			Commands: []string{
				"helm upgrade kafka-cluster charts/kafka-cluster -n kafka -f values.yaml --timeout 10m --wait",
			},
		})
	}

	return hints
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
