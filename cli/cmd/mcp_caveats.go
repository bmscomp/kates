package cmd

import (
	"fmt"
	"math"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpCaveatID names one entry of the caveat catalogue. Results carry the ids
// and texts of the caveats that apply to them; kates://caveats lists them all
// with the source files each was checked against. Clients may key on an id,
// so an id keeps its meaning: a caveat whose meaning changes gets a new one,
// as reaper-deadline replaced reaper-30-minutes when runs got deadlines of
// their own.
type mcpCaveatID string

const (
	mcpCaveatLoadSingleProducer    mcpCaveatID = "load-single-producer"
	mcpCaveatReaperDeadline        mcpCaveatID = "reaper-deadline"
	mcpCaveatMergedSpecOnly        mcpCaveatID = "merged-spec-only"
	mcpCaveatPentestConfigOnly     mcpCaveatID = "pentest-config-only"
	mcpCaveatCVEFixedList          mcpCaveatID = "cve-fixed-list"
	mcpCaveatSecurityTrendInMemory mcpCaveatID = "security-trend-in-memory"
	mcpCaveatTuningOneMeasurement  mcpCaveatID = "tuning-one-measurement"
	mcpCaveatTrendsMixSpecs        mcpCaveatID = "trends-mix-specs"
	mcpCaveatMinISRBrokerLevel     mcpCaveatID = "min-isr-broker-level"
	mcpCaveatPrometheusUnreachable mcpCaveatID = "prometheus-may-be-unreachable"
	mcpCaveatChaosTimesApproximate mcpCaveatID = "chaos-times-approximate"
	mcpCaveatAlertRulesNotFiring   mcpCaveatID = "alert-rules-not-firing"
	mcpCaveatClusterDataCached     mcpCaveatID = "cluster-data-cached"
)

// mcpCaveat is one thing the data behind a tool cannot show. Refs are paths
// relative to the repository root, with the lines the text was checked
// against; they are for the people maintaining this list, and appear only in
// the kates://caveats resource, not in every result.
type mcpCaveat struct {
	ID   mcpCaveatID
	Text string
	Refs []string
}

const mcpJava = "kates/src/main/java/com/bmscomp/kates/"

// mcpRefAnchors holds the anchors of the refs made with mcpAnchoredRef.
var mcpRefAnchors = map[string][]string{}

// mcpAnchoredRef returns ref, path:lines, and records its anchors: text the
// cited lines hold, at least one in each range. Line numbers alone go stale
// without failing anything when the code above them moves, and then point at
// other code; TestMCPCaveatRefAnchors fails as soon as an anchor is no longer
// in the lines its ref cites. kates://caveats shows the ref alone.
func mcpAnchoredRef(ref string, anchors ...string) string {
	mcpRefAnchors[ref] = append(mcpRefAnchors[ref], anchors...)
	return ref
}

// mcpCaveatsCore holds the caveats more than one tool group uses. Every entry
// was checked against the code, not
// copied from the plan: where the code said more than the plan (the CVE check
// never learns the Kafka version; the alerts endpoint returns rule
// definitions), the text says what the code does.
var mcpCaveatsCore = []mcpCaveat{
	{
		ID: mcpCaveatLoadSingleProducer,
		Text: "A LOAD run is one producer and one consumer, whatever numProducers and numConsumers say, " +
			"so it cannot show how the cluster behaves under parallel clients. STRESS starts one producer per numProducers.",
		Refs: []string{mcpAnchoredRef(mcpJava+"engine/TestOrchestrator.java:1515-1524", "case LOAD ->", "case STRESS ->")},
	},
	{
		ID: mcpCaveatReaperDeadline,
		Text: "A run still RUNNING past its deadline is marked FAILED by a reaper that checks every 60 seconds. The " +
			"deadline counts from the run's creation, so time spent waiting to start counts. A current Kates API sets " +
			"it when it creates the run: how long the run is set to last, at most kates.engine.max-duration-ms " +
			"(default 7200000), plus kates.engine.reaper-grace-ms (default 300000). That length is the spec's " +
			"durationMs, twice that for INTEGRITY, which reads its records back for up to as long again, or a " +
			"scenario's phases added up; an INTEGRATION_CDC run, which no duration bounds, gets the cap. That API " +
			"refuses with 400 a request set to last longer than the cap, and when it fails a run, each task that had " +
			"not finished gets an error that starts \"Timeout:\" and the others keep their results. An older Kates " +
			"API fails every run 30 minutes after its creation (max-duration-ms, default 1800000), whatever the run " +
			"was set to last, and deletes the task results of each run it fails, and of each run a restart left " +
			"RUNNING, so such a run has no tasks. Neither /api/health nor a run says which of the two the API is.",
		Refs: []string{
			"kates/src/main/resources/application.properties:317-331",
			mcpJava + "engine/TestTimeoutReaper.java:39-47,50-136",
			mcpJava + "engine/TestOrchestrator.java:127-149,181-184,342,1057-1111,1177-1219",
			mcpJava + "persistence/EntityMapper.java:144-153",
			"kates/src/main/resources/db/migration/V24__run_planned_duration.sql:1-13",
			mcpJava + "domain/TestRun.java:478-481",
			mcpJava + "api/HealthResource.java:48-69",
		},
	},
	{
		ID: mcpCaveatMergedSpecOnly,
		Text: "A run's spec is the request merged with its test type's defaults. A field the type has a default " +
			"for shows the value the run used; targetThroughput, consumerGroup, fetchMinBytes, fetchMaxWaitMs and " +
			"the enable options appear only when the request set them, so enableIdempotence false there means the " +
			"request turned the producer's idempotence off, and an absent one that the Kafka client decided, which " +
			"turns it on whenever acks is all. Absent, an INTEGRITY run checked CRCs and its consumer joined " +
			"integrity-cg-integrity. requestedSpec holds the request's own fields. A run stored before the backend " +
			"kept the request has no requestedSpec, and its spec shows all seven of those fields at the Java " +
			"defaults whatever the request said: that backend dropped them when it merged the spec and ran without " +
			"them, so its enableIdempotence false means the client decided, and such an INTEGRITY run used " +
			"integrity-cg-integrity, without transactions.",
		Refs: []string{
			mcpJava + "domain/TestSpec.java:14-32,111-113",
			mcpAnchoredRef(mcpJava+"engine/TestOrchestrator.java:166,181-183,226",
				"applyTypeDefaults(type, request.getSpec())", ".withRequestedSpec(", "buildTasks(type, spec, run.getId())"),
			mcpAnchoredRef(mcpJava+"engine/TestOrchestrator.java:977-1022", "TestSpec applyTypeDefaults(", "return merged;"),
			mcpAnchoredRef(mcpJava+"engine/TestOrchestrator.java:1493-1498,1588-1591",
				"Only when asked", `"integrity-cg")`),
			mcpAnchoredRef(mcpJava+"engine/NativeKafkaBackend.java:759", `task.getConsumerGroup() + "-integrity"`),
			mcpAnchoredRef(mcpJava+"persistence/EntityMapper.java:42,70-71,129-131",
				"entity.setRequestedSpecJson(toJson(run.getRequestedSpec()))", "fromJson(entity.getRequestedSpecJson()",
				"if (run.getRequestedSpec() != null)"),
			"kates/src/main/resources/db/migration/V23__requested_spec_fields.sql:1-6",
		},
	},
	{
		ID: mcpCaveatPentestConfigOnly,
		Text: "The security \"pentest\" attacks nothing. Its six checks read the configuration of the first broker " +
			"listed and the ACL list; an ACL list it cannot read is reported as PROTECTED.",
		Refs: []string{mcpJava + "service/SecurityPentestService.java:40-67,102-204"},
	},
	{
		ID: mcpCaveatCVEFixedList,
		Text: "The CVE check compares against a fixed list of seven Apache Kafka CVEs, the newest from 2024. It never " +
			"learns the Kafka version: it reads kafkaVersion from the cluster description, which does not carry one, " +
			"so the version is \"unknown\", the comparison fails, and every CVE is reported PATCHED with grade PASS.",
		Refs: []string{
			mcpJava + "service/SecurityService.java:1383-1474",
			mcpJava + "service/SecurityService.java:1572-1586",
			mcpJava + "service/ClusterHealthService.java:72-93",
		},
	},
	{
		ID: mcpCaveatSecurityTrendInMemory,
		Text: "The security grade trend is an in-memory list of the last 100 audits in the backend pod, lost on " +
			"restart. Each read of GET /api/security/audit appends one entry, an agent's posture read included; the " +
			"audits that compliance, baseline, drift and gate run internally do not (an older backend appended those " +
			"too, so there it mostly reflects API traffic).",
		Refs: []string{
			mcpJava + "service/SecurityService.java:42,58-69",
			mcpJava + "service/SecurityService.java:789,852,914,979",
			mcpJava + "service/SecurityService.java:1649-1689",
		},
	},
	{
		ID: mcpCaveatTuningOneMeasurement,
		Text: "A TUNE_* run executes one produce task with the spec's single configuration. The tuning report copies " +
			"that one summary into every step, so all steps show the same numbers and the best step is always step 0.",
		Refs: []string{
			mcpAnchoredRef(mcpJava+"engine/TestOrchestrator.java:1598-1599", "case TUNE_REPLICATION,", "-tune-0"),
			mcpJava + "trogdor/SpecFactory.java:38-39",
			mcpJava + "engine/TuningTestRunner.java:101-139",
		},
	},
	{
		ID: mcpCaveatTrendsMixSpecs,
		Text: "GET /api/trends selects runs by test type, date and status only: a trend takes every DONE run of " +
			"that type in its window, whatever its spec (partitions, record size, throughput).",
		Refs: []string{
			mcpJava + "trend/TrendResource.java:28-57",
			mcpJava + "trend/TrendService.java:53-54,185-189",
			mcpAnchoredRef(mcpJava+"service/TestRunRepository.java:317-337",
				"List<TestRun> findWithResults(", "WHERE r.testType = :type AND r.status = :status",
				".map(EntityMapper::toDomain)"),
		},
	},
	{
		ID: mcpCaveatMinISRBrokerLevel,
		Text: "This Kates backend's topic detail left min.insync.replicas out: an older backend keeps only topic-level " +
			"and default config entries, so a value set at broker level, which is where the kafka-cluster chart sets " +
			"it (2), is missing from a topic's configs. Its absence does not mean 1. A current backend reports the " +
			"value in force wherever it is set, and what set it.",
		Refs: []string{
			mcpJava + "service/TopicService.java:170-198",
			"charts/kafka-cluster/values.yaml:137",
		},
	},
	{
		ID: mcpCaveatPrometheusUnreachable,
		Text: "Kafka metrics in disruption reports come from Prometheus at kates.prometheus.url. The kates chart sets it " +
			"to the Service of the monitoring stack kates deploy installs (monitoring-kube-prometheus-prometheus in " +
			"namespace monitoring), and kates deploy to the namespace it uses. The metrics are missing when Prometheus " +
			"runs elsewhere (make monitoring installs it in namespace kafka, where the manifests make kates applies " +
			"point, but a chart install is not told) or is down, and on a chart older than 0.10.5, which set no URL: " +
			"missing means not measured, not zero. An SLA verdict leaves a check it could not " +
			"measure out of its grade and lists it, with the reason, under unevaluated (with nothing measured the grade " +
			"is \"-\"), so read a grade that has unevaluated checks as partial.",
		Refs: []string{
			mcpAnchoredRef("kates/src/main/resources/application.properties:310-315",
				"# Prometheus server (for disruption metrics capture)",
				"kates.prometheus.url=http://monitoring-kube-prometheus-prometheus.monitoring.svc:9090"),
			mcpAnchoredRef("charts/kates/values.yaml:395-405",
				"# -- Prometheus the backend queries for a disruption's Kafka metrics",
				`url: "http://monitoring-kube-prometheus-prometheus.monitoring.svc:9090"`),
			mcpAnchoredRef("charts/kates/templates/configmap.yaml:37-39",
				"{{- with .Values.prometheus.url }}", "KATES_PROMETHEUS_URL: {{ . | quote }}"),
			mcpAnchoredRef("kates/k8s/configmap.yaml:29-33",
				"# Prometheus, for a disruption's Kafka metrics",
				`KATES_PROMETHEUS_URL: "http://monitoring-kube-prometheus-prometheus.kafka.svc:9090"`),
			mcpJava + "disruption/PrometheusMetricsCapture.java:31-34",
			mcpJava + "disruption/SlaGrader.java:26,140,153,170-178,183,191-192",
			"charts/monitoring/Chart.yaml:18",
		},
	},
	{
		ID: mcpCaveatChaosTimesApproximate,
		Text: "On the default litmus-crd chaos provider the start time is taken before the ChaosEngine is created and " +
			"the end is found by polling every 5 seconds, so fault start, end and duration are approximate by several seconds.",
		Refs: []string{
			mcpAnchoredRef("kates/src/main/resources/application.properties:255-256",
				"# Chaos coordination (noop | kubernetes | litmus-crd | hybrid)", "kates.chaos.provider=litmus-crd"),
			mcpAnchoredRef(mcpJava+"chaos/LitmusChaosProvider.java:56,94",
				"int resultPollIntervalMs = 5_000;", "Instant start = Instant.now();"),
		},
	},
	{
		ID: mcpCaveatAlertRulesNotFiring,
		Text: "Cluster alerts are the alert rules defined in PrometheusRule resources in the Kafka namespace, limited " +
			"to critical and warning severities and a fixed list of Kafka alert names. They are definitions, not alerts " +
			"that are firing. When the rules cannot be read the list is empty, which looks the same as having none.",
		Refs: []string{mcpJava + "service/ClusterAlertsService.java:22-49,58-109"},
	},
	{
		ID:   mcpCaveatClusterDataCached,
		Text: "The backend caches cluster info and the partition health check for 30 seconds, so those figures can be up to 30 seconds old.",
		Refs: []string{mcpJava + "service/ClusterHealthService.java:45,72-89,233-236,367-368"},
	},
}

// When reaper-deadline applies. These tools cannot tell which reaper the
// Kates API runs, so they hold a run to both: an older API fails every run
// mcpReaperOlderLimitMs after its creation, and a current one refuses a run
// set to last longer than kates.engine.max-duration-ms, mcpReaperMaxDurationMs
// as Kates ships it (application.properties:322).
// TestMCPReaperLimitsMatchTheBackend holds the second to the backend.
const (
	mcpReaperOlderLimitMs  = 30 * 60 * 1000
	mcpReaperMaxDurationMs = 2 * 60 * 60 * 1000
)

// mcpPlannedDurationMs is how long a run of this type with this durationMs is
// set to last, counted from its creation, as a current Kates API works it out
// (TestOrchestrator.plannedDurationMs): the duration, twice that for
// INTEGRITY, which reads its records back for up to as long again. ok is false
// for INTEGRATION_CDC, which no duration bounds. A scenario's stored spec is
// not validated, so the double is held at the largest int64 rather than wrap.
func mcpPlannedDurationMs(testType string, durationMs int64) (ms int64, ok bool) {
	d := max(durationMs, 0)
	switch testType {
	case "INTEGRATION_CDC":
		return 0, false
	case "INTEGRITY":
		if d > math.MaxInt64/2 {
			return math.MaxInt64, true
		}
		return 2 * d, true
	}
	return d, true
}

// mcpCaveats is the catalogue: the shared entries, then each tool group's own
// (mcpCaveatsCluster, mcpCaveatsRuns, mcpCaveatsChaos, mcpCaveatsSecurity,
// each declared in that group's file). A group adds a caveat to its own list,
// so groups written in parallel never edit the same lines.
var mcpCaveats = mcpJoinCaveats(mcpCaveatsCore, mcpCaveatsCluster, mcpCaveatsRuns, mcpCaveatsChaos, mcpCaveatsSecurity)

func mcpJoinCaveats(lists ...[]mcpCaveat) []mcpCaveat {
	var all []mcpCaveat
	for _, l := range lists {
		all = append(all, l...)
	}
	return all
}

var mcpCaveatIndex = func() map[mcpCaveatID]mcpCaveat {
	m := make(map[mcpCaveatID]mcpCaveat, len(mcpCaveats))
	for _, c := range mcpCaveats {
		m[c.ID] = c
	}
	return m
}()

// mcpCaveatRef is how a caveat appears in a result: its id and text. The
// source refs stay in kates://caveats.
type mcpCaveatRef struct {
	ID   string `json:"id" jsonschema:"caveat id; kates://caveats lists every caveat with its sources"`
	Text string `json:"text"`
}

const mcpCaveatsURI = "kates://caveats"

// mcpCaveatsMarkdown renders the catalogue for the kates://caveats resource.
func mcpCaveatsMarkdown() string {
	var b strings.Builder
	b.WriteString("# Kates caveats\n\n")
	b.WriteString("What the data behind the Kates tools cannot show. A tool result lists the ids of the caveats " +
		"that apply to it. Each entry names the source files it was checked against, as paths relative to the " +
		"Kates repository root.\n")
	for _, c := range mcpCaveats {
		fmt.Fprintf(&b, "\n## %s\n\n%s\n\nSources:\n", c.ID, c.Text)
		for _, r := range c.Refs {
			fmt.Fprintf(&b, "- `%s`\n", r)
		}
	}
	return b.String()
}

func registerMCPCaveatsResource(s *mcp.Server, deps *mcpDeps) {
	addStaticResource(s, deps, &mcp.Resource{
		URI:         mcpCaveatsURI,
		Name:        "caveats",
		Title:       "Kates caveats",
		Description: "What the data behind the Kates tools cannot show, with the source files behind each caveat.",
		MIMEType:    "text/markdown",
	}, mcpCaveatsMarkdown())
}
