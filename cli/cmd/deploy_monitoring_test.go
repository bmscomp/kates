package cmd

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/detect"
)

// deployForScrapeTest runs the whole pipeline with every scrape-bearing
// component selected and returns the helm calls in order.
func deployForScrapeTest(t *testing.T, monitoring bool) []string {
	t.Helper()
	defer func() {
		deployTopology = "isolated"
		deployKafkaNS = "kafka"
		deployMM2NS = "kafka"
		deployKafkaName = "krafter"
		deployWithSchemaRegistry = "none"
		deployWithChaos = false
		deployWithMonitoring = false
		deployWithCertManager = false
		deployWithKyverno = false
		deployWithStrimzi = false
		deployWithKafkaConnect = false
		deployWithKafkaUI = false
		deployWithMirrorMaker2 = false
	}()

	deployTopology = "isolated"
	deployKafkaNS = "kafka"
	deployMM2NS = "kafka"
	deployKafkaName = "krafter"
	deployWithSchemaRegistry = "none"
	deployWithChaos = true
	deployWithMonitoring = monitoring
	deployWithCertManager = false
	deployWithKyverno = true
	deployWithStrimzi = false
	deployWithKafkaConnect = true
	deployWithKafkaUI = false
	deployWithMirrorMaker2 = true

	var helmCommands []string
	var mu sync.Mutex
	runExecFn = func(ctx context.Context, name string, args ...string) error { return nil }
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error { return nil }
	runHelmFn = func(ctx context.Context, args ...string) error {
		mu.Lock()
		helmCommands = append(helmCommands, "helm "+strings.Join(args, " "))
		mu.Unlock()
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool { return false }
	defaultExecutor = &MockExecutor{}

	if err := runDeploy(deployCmd, []string{}); err != nil {
		t.Fatalf("runDeploy failed: %v", err)
	}
	return helmCommands
}

func findHelm(cmds []string, prefix string) (int, string) {
	for i, c := range cmds {
		if strings.HasPrefix(c, prefix) {
			return i, c
		}
	}
	return -1, ""
}

// TestDeployCommand_MonitoringWiresEveryScrape is the contract behind the
// boards: with --with-monitoring, every component this deploy installs is
// told to create its scrape and its rules, and the monitoring chart (which
// carries the CRDs those render against) is installed before any of them.
func TestDeployCommand_MonitoringWiresEveryScrape(t *testing.T) {
	cmds := deployForScrapeTest(t, true)

	monIdx, mon := findHelm(cmds, "helm upgrade --install monitoring charts/monitoring")
	if monIdx < 0 {
		t.Fatalf("monitoring was not installed; helm calls:\n%s", strings.Join(cmds, "\n"))
	}
	if !strings.Contains(mon, "--set chaosAlerts.enabled=true") {
		t.Errorf("the chaos recording rules were not switched on: %s", mon)
	}

	want := map[string][]string{
		"helm upgrade --install krafter charts/kafka-cluster":           {"--set monitoring.podMonitor.enabled=true", "--set alerts.enabled=true"},
		"helm upgrade --install connect-cluster charts/connect-cluster": {"--set monitoring.podMonitor.enabled=true", "--set alerts.enabled=true"},
		"helm upgrade --install mm2 charts/mirror-maker2":               {"--set metrics.enabled=true", "--set podMonitors.enabled=true", "--set alerts.enabled=true"},
		"helm upgrade --install kates charts/kates":                     {"--set metrics.serviceMonitor.enabled=true", "--set metrics.prometheusRule.enabled=true", "--set prometheus.url=http://monitoring-kube-prometheus-prometheus.monitoring.svc.", "--set networkPolicy.prometheus.namespace=monitoring"},
		"helm upgrade --install chaos charts/kates-chaos":               {"--set litmus-core.exporter.enabled=true", "--set monitoring.serviceMonitor.enabled=true"},
	}
	for prefix, flags := range want {
		idx, cmd := findHelm(cmds, prefix)
		if idx < 0 {
			t.Errorf("%q was not run; helm calls:\n%s", prefix, strings.Join(cmds, "\n"))
			continue
		}
		if idx < monIdx {
			t.Errorf("%q rendered before the monitoring CRDs were installed (call %d, monitoring is call %d)", prefix, idx, monIdx)
		}
		for _, f := range flags {
			if !strings.Contains(cmd, f) {
				t.Errorf("%q is missing %q:\n%s", prefix, f, cmd)
			}
		}
	}

	// Kyverno installs in Group A, ahead of the CRDs, so its scrape is a
	// second Helm call once monitoring is in.
	kyIdx, ky := findHelm(cmds, "helm upgrade kyverno kyverno/kyverno")
	if kyIdx < 0 {
		t.Fatalf("Kyverno was never wired into Prometheus; helm calls:\n%s", strings.Join(cmds, "\n"))
	}
	if kyIdx < monIdx {
		t.Errorf("Kyverno's ServiceMonitors were enabled before monitoring was installed (call %d vs %d)", kyIdx, monIdx)
	}
	for _, f := range []string{
		"--version " + kyvernoChartVersion,
		"--reuse-values",
		"--set admissionController.serviceMonitor.enabled=true",
		"--set backgroundController.serviceMonitor.enabled=true",
	} {
		if !strings.Contains(ky, f) {
			t.Errorf("the Kyverno upgrade is missing %q: %s", f, ky)
		}
	}
}

// Without --with-monitoring nothing is switched on: the charts' own API
// check is what keeps a bare cluster installable, and a foreign Prometheus
// keeps whatever the chart defaults say.
func TestDeployCommand_NoMonitoringPassesNoScrapeFlags(t *testing.T) {
	cmds := deployForScrapeTest(t, false)
	for _, c := range cmds {
		for _, banned := range []string{"Monitor.enabled=", "Monitors.enabled=", "alerts.enabled=", "chaosAlerts", "exporter.enabled=", "prometheus.url=", "networkPolicy.prometheus"} {
			if strings.Contains(c, banned) {
				t.Errorf("a scrape flag was passed without monitoring: %s", c)
			}
		}
	}
	if idx, _ := findHelm(cmds, "helm upgrade kyverno kyverno/kyverno"); idx >= 0 {
		t.Error("the Kyverno ServiceMonitor upgrade ran without monitoring")
	}
}

// The backend reads Prometheus from the Service kube-prometheus-stack creates
// for the monitoring release, in whichever namespace this deploy put it. Its
// built-in default named prometheus.monitoring.svc, which nothing creates.
func TestPrometheusArgsFollowTheMonitoringNamespace(t *testing.T) {
	defer func() { deployWithMonitoring = false }()
	deployWithMonitoring = true
	dc := &deployContext{
		ns:     resolvedNamespaces{jaeger: "kates-stack"},
		report: &detect.DetectReport{Network: detect.NetworkInfo{ClusterDomain: "corp.local"}},
	}
	got := strings.Join(dc.prometheusArgs(), " ")
	want := "--set prometheus.url=http://monitoring-kube-prometheus-prometheus.kates-stack.svc.corp.local:9090 " +
		"--set networkPolicy.prometheus.namespace=kates-stack"
	if got != want {
		t.Errorf("prometheusArgs = %q, want %q", got, want)
	}

	deployWithMonitoring = false
	if args := dc.prometheusArgs(); args != nil {
		t.Errorf("without --with-monitoring the chart default stands, got %q", args)
	}
}
