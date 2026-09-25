package cmd

import (
	"fmt"

	"github.com/bmscomp/kates/cli/output"
)

// Wiring the components into the Prometheus this deploy installs.
//
// charts/monitoring provisions every board in dashboards/, and a board reads
// series that exist only if something scrapes the component emitting them:
// the kafka-cluster, connect-cluster and mirror-maker2 PodMonitors, the kates
// and chaos-exporter ServiceMonitors, Kyverno's, and the recording rules the
// SLO and chaos panels are built on. Each of those is a switch on the
// component's own chart, off in every kind overlay or chart default, and a
// deploy that turned none of them on ended with twelve boards and nothing
// for them to show — which looks exactly like a healthy idle cluster.
//
// The rule: with --with-monitoring, this deploy owns that wiring and states it
// with --set on every component it installs. Without it nothing is passed;
// the charts render their monitors only where the monitoring.coreos.com API
// exists, so their defaults are safe on a bare cluster either way.
//
// Order matters as much as the flags. Helm decides at render time whether the
// API exists and never revisits a release when a CRD turns up later, so the
// monitoring chart (which carries the CRDs) is installed before anything that
// renders against them: Kafka in deployGroupB, everything in Group C after
// it. Kyverno is the exception — it is an operator and installs in Group A —
// and gets its ServiceMonitors switched on once monitoring is in.

// scrapeValues lists, per chart, the values that connect it to Prometheus:
// the scrape itself (a PodMonitor or ServiceMonitor, plus for MirrorMaker 2
// and the chaos plane the exporter the scrape needs) and the rules whose
// recording series the boards read.
var scrapeValues = map[string][]string{
	"charts/kafka-cluster":   {"monitoring.podMonitor.enabled", "alerts.enabled"},
	"charts/connect-cluster": {"monitoring.podMonitor.enabled", "alerts.enabled"},
	"charts/mirror-maker2":   {"metrics.enabled", "podMonitors.enabled", "alerts.enabled"},
	"charts/kates":           {"metrics.serviceMonitor.enabled", "metrics.prometheusRule.enabled"},
	"charts/kates-chaos":     {"litmus-core.exporter.enabled", "monitoring.serviceMonitor.enabled"},
}

// scrapeArgs is the --set list that switches a chart's scrape and rules on,
// or nothing when this deploy is not installing monitoring.
func (dc *deployContext) scrapeArgs(chart string) []string {
	if !deployWithMonitoring {
		return nil
	}
	keys, ok := scrapeValues[chart]
	if !ok {
		panic("scrapeArgs: no scrape values for " + chart)
	}
	args := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		args = append(args, "--set", k+"=true")
	}
	return args
}

// monitoringPrometheusService is the Service kube-prometheus-stack creates for
// Prometheus when charts/monitoring is installed as release "monitoring", as
// this deploy and `make monitoring` both do. There is no Service named
// prometheus, which the backend's default used to name.
const monitoringPrometheusService = "monitoring-kube-prometheus-prometheus"

// prometheusArgs points the backend at the Prometheus this deploy installs,
// in the namespace it installs it into (--topology single moves it), and
// lets the chart's NetworkPolicy reach it there. Nothing without
// --with-monitoring: the chart's default, namespace monitoring, stands.
func (dc *deployContext) prometheusArgs() []string {
	if !deployWithMonitoring {
		return nil
	}
	ns := dc.ns.jaeger
	url := fmt.Sprintf("http://%s.%s.svc.%s:9090", monitoringPrometheusService, ns, dc.resolveClusterDomain())
	return []string{
		"--set", "prometheus.url=" + url,
		"--set", "networkPolicy.prometheus.namespace=" + ns,
	}
}

// wireKyvernoScrape switches on the ServiceMonitors of the two Kyverno
// controllers the kyverno-security board reads (admission requests, review
// latency, policy results). Kyverno is installed in Group A, before the
// monitoring CRDs exist, so its own install cannot render them; this is the
// upgrade that does, once they do. Only for a Kyverno this run installed: an
// existing release is left as it is, like every other one, and
// dashboards/USING.md has the upgrade line for retrofitting.
func wireKyvernoScrape(dc *deployContext) error {
	if !dc.kyvernoInstalled || !deployWithMonitoring {
		return nil
	}
	ctx := dc.ctx
	dl.Println("    - Wiring Kyverno into Prometheus (ServiceMonitors)...")
	if err := runHelmFn(ctx, "upgrade", "kyverno", "kyverno/kyverno",
		"--version", kyvernoChartVersion,
		"-n", "kyverno", "--reuse-values",
		"--set", "admissionController.serviceMonitor.enabled=true",
		"--set", "backgroundController.serviceMonitor.enabled=true",
		"--timeout", "5m"); err != nil {
		return fmt.Errorf("enabling the Kyverno ServiceMonitors: %w", err)
	}
	dl.Printf("    %s Kyverno metrics scraped\n", output.AccentStyle.Render("✔"))
	return nil
}
