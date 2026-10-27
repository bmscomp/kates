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

// ChartRef names a chart of this repository (its charts/ directory) in a
// printed command. The CLI sets it to the chart's published reference.
var ChartRef = func(dir string) string { return "charts/" + dir }

// remediationRules are the rules GenerateRemediation applies, in the order
// their hints are listed. Each returns nil when its check needs no hint.
var remediationRules = []func(*DetectReport) *Remediation{
	strimziRemediation,
	zonesRemediation,
	storageClassRemediation,
	resourcesRemediation,
	dnsRemediation,
	rbacRemediation,
	prometheusRemediation,
	kyvernoSelectorRemediation,
	kyvernoLimitsRemediation,
	existingDeploymentRemediation,
	sizingRemediation,
	psaRemediation,
	wildcardRBACRemediation,
	certExpiryRemediation,
}

// GenerateRemediation inspects the report and produces actionable hints.
func GenerateRemediation(report *DetectReport) []Remediation {
	var hints []Remediation
	for _, rule := range remediationRules {
		if h := rule(report); h != nil {
			hints = append(hints, *h)
		}
	}
	return hints
}

// failedCheck reports whether the verdict has a failing row of that name.
func failedCheck(report *DetectReport, desc string) bool {
	for _, c := range report.Verdict.Checks {
		if c.Description == desc && !c.Status {
			return true
		}
	}
	return false
}

func strimziRemediation(report *DetectReport) *Remediation {
	if !report.Strimzi.CRDsPresent {
		return &Remediation{
			Check:    "Strimzi CRDs installed",
			Severity: "critical",
			Summary:  "Strimzi Custom Resource Definitions are not installed",
			// Must match the release name and namespace the deploy paths use.
			// The operator is a cluster singleton — its RBAC and lease names are
			// hardcoded upstream — so a differently-named release does not
			// install a second operator, it collides with the first.
			Commands: []string{
				"helm upgrade --install strimzi-operator " + ChartRef("strimzi-operator") + " --namespace strimzi-operator --create-namespace",
			},
			DocURL: "https://strimzi.io/docs/operators/latest/deploying",
		}
	}
	if report.Strimzi.Running {
		return nil
	}
	var cmds []string
	if report.Strimzi.Namespace == "" {
		cmds = []string{
			"kates deploy          # deploy the operator",
			"make kafka-deploy     # alternative helm deployment",
		}
	} else {
		cmds = []string{
			"kubectl get deployment -A | grep strimzi  # check operator status",
			"kubectl rollout restart deployment/strimzi-cluster-operator -n " + report.Strimzi.Namespace,
		}
	}
	return &Remediation{
		Check:    "Strimzi operator running",
		Severity: "warning",
		Summary:  "Strimzi CRDs are present but the operator is not running",
		Commands: cmds,
		DocURL:   "https://strimzi.io/docs/operators/latest/deploying",
	}
}

func zonesRemediation(report *DetectReport) *Remediation {
	if len(report.Zones) >= 3 {
		return nil
	}
	return &Remediation{
		Check:    "≥ 3 availability zones",
		Severity: "critical",
		Summary:  fmt.Sprintf("Only %d zone(s) found — 3-AZ Kafka requires at least 3 zones", len(report.Zones)),
		Commands: []string{
			"kubectl get nodes --show-labels | grep topology.kubernetes.io/zone",
			"kubectl label node <node-name> topology.kubernetes.io/zone=<zone>  # if labels are missing",
		},
	}
}

func storageClassRemediation(report *DetectReport) *Remediation {
	if len(report.Storage) > 0 {
		return nil
	}
	return &Remediation{
		Check:    "StorageClass available",
		Severity: "critical",
		Summary:  "No StorageClass found — Kafka PVCs will fail to bind",
		Commands: []string{
			"kubectl apply -f config/storage/  # apply zone-specific storage classes",
			"kubectl get sc  # verify",
		},
	}
}

func resourcesRemediation(report *DetectReport) *Remediation {
	if report.Budget.Sufficient {
		return nil
	}
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
	return &Remediation{
		Check:    "Broker resources fit (all zones)",
		Severity: "critical",
		Summary:  "Cluster does not have enough CPU/memory for the requested Kafka deployment",
		Commands: cmds,
	}
}

func dnsRemediation(report *DetectReport) *Remediation {
	if report.Network.CoreDNSRunning != 0 {
		return nil
	}
	return &Remediation{
		Check:    "DNS resolution",
		Severity: "critical",
		Summary:  "CoreDNS not detected — Kafka brokers require DNS for discovery",
		Commands: []string{
			"kubectl get deployment -n kube-system | grep -i dns",
			"kubectl rollout restart deployment/coredns -n kube-system",
		},
	}
}

func rbacRemediation(report *DetectReport) *Remediation {
	if !failedCheck(report, "RBAC permissions") {
		return nil
	}
	return &Remediation{
		Check:    "RBAC permissions",
		Severity: "critical",
		Summary:  "Insufficient permissions to create resources in the kafka namespace",
		Commands: []string{
			"kubectl auth can-i create deployments -n kafka  # test current permissions",
			"kubectl create namespace kafka --dry-run=client -o yaml | kubectl apply -f -",
			"kubectl create rolebinding kafka-admin --clusterrole=admin --user=$(kubectl config current-context) -n kafka",
		},
	}
}

func prometheusRemediation(report *DetectReport) *Remediation {
	if report.Monitoring.PodMonitorCRD {
		return nil
	}
	return &Remediation{
		Check:    "Prometheus monitoring",
		Severity: "warning",
		Summary:  "PodMonitor CRD not found — Kafka metrics will not be scraped",
		Commands: []string{
			"helm install monitoring prometheus-community/kube-prometheus-stack -n monitoring --create-namespace",
		},
		DocURL: "https://prometheus-operator.dev/docs/getting-started/introduction/",
	}
}

func kyvernoSelectorRemediation(report *DetectReport) *Remediation {
	if !report.Admission.Kyverno.Installed || !report.Admission.Kyverno.Constraints.EmptyPodSelectorBlocked {
		return nil
	}
	return &Remediation{
		Check:    "Kyverno NetworkPolicy safe",
		Severity: "info",
		Summary:  "Kyverno blocks empty podSelector — chart uses explicit selectors (already safe)",
		Commands: []string{
			"# No action required — the Helm chart generates Kyverno-compliant NetworkPolicies",
			"# The default-deny and allow-dns policies use app.kubernetes.io/part-of=strimzi-krafter",
		},
	}
}

func kyvernoLimitsRemediation(report *DetectReport) *Remediation {
	if !report.Admission.Kyverno.Installed || !report.Admission.Kyverno.Constraints.ResourceLimitsRequired {
		return nil
	}
	return &Remediation{
		Check:    "Resource limits required",
		Severity: "warning",
		Summary:  "Kyverno enforces resource limits — ensure all components have limits in values.yaml",
		Commands: []string{
			"# Verify limits are set for all components in your values.yaml:",
			"helm show values " + ChartRef("kafka-cluster") + " | grep -A 4 'limits:'",
		},
	}
}

func existingDeploymentRemediation(report *DetectReport) *Remediation {
	if report.ExistingKafka.KafkaClusters <= 0 {
		return nil
	}
	return &Remediation{
		Check:    "Existing deployment",
		Severity: "warning",
		Summary:  "An existing Kafka cluster is running — use helm upgrade instead of install",
		Commands: []string{
			"helm upgrade kafka-cluster " + ChartRef("kafka-cluster") + " -n kafka -f values.yaml --timeout 10m --wait",
		},
	}
}

func sizingRemediation(report *DetectReport) *Remediation {
	if !failedCheck(report, "Sizing recommendation available") {
		return nil
	}
	return &Remediation{
		Check:    "Sizing recommendation available",
		Severity: "critical",
		Summary:  "Cluster does not meet the minimum sizing profile requirements (Minimal: >= 3 CPU cores, >= 8Gi Memory)",
		Commands: []string{
			"# Check available node resources:",
			"kubectl top nodes",
			"kubectl describe nodes | grep -A 5 -E 'Allocatable'",
		},
	}
}

func psaRemediation(report *DetectReport) *Remediation {
	if report.Security.PSALabelEnforced != "restricted" {
		return nil
	}
	return &Remediation{
		Check:    "Pod Security Standards compatible",
		Severity: "info",
		Summary:  "Restricted Pod Security Standard is enforced on namespace 'kafka' — Kates is fully compatible",
		Commands: []string{
			"# Verify pod security admission label configuration:",
			"kubectl get ns kafka -o jsonpath='{.metadata.labels}'",
		},
	}
}

func wildcardRBACRemediation(report *DetectReport) *Remediation {
	if !report.Security.HasExcessivePrivileges {
		return nil
	}
	return &Remediation{
		Check:    "Wildcard RBAC roles avoided",
		Severity: "warning",
		Summary:  "Namespace Roles contain wildcard '*' resources or verbs which violates least privilege security standards",
		Commands: []string{
			"# Inspect Roles with wildcard permissions in the namespace:",
			"kubectl get roles -n kafka -o json | jq '.items[] | select(.rules[].verbs[] == \"*\" or .rules[].resources[] == \"*\") | .metadata.name'",
			"# Scope down rules to explicit apiGroups, resources, and verbs in values.yaml",
		},
	}
}

func certExpiryRemediation(report *DetectReport) *Remediation {
	soon := expiringCerts(report.Security.ExpiringCerts)
	if len(soon) == 0 {
		return nil
	}
	certCmds := make([]string, 0, len(soon)+2)
	for _, c := range soon {
		certCmds = append(certCmds, fmt.Sprintf("# Cert '%s' (%s) expires in %d days", c.SecretName, c.Subject, c.DaysLeft))
	}
	certCmds = append(certCmds,
		"# Renew expiring TLS secrets or trigger certificate rotation:",
		"kubectl delete secret <secret-name> -n kafka  # if managed by cert-manager to force renewal",
	)
	return &Remediation{
		Check:    "TLS certificate validity",
		Severity: "warning",
		Summary:  fmt.Sprintf("%d TLS certificate(s) in namespace 'kafka' are expiring within 30 days", len(soon)),
		Commands: certCmds,
	}
}
