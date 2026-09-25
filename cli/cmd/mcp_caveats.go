package cmd

import (
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpCaveatID names one entry of the caveat catalogue. Results carry the ids
// and texts of the caveats that apply to them; kates://caveats lists them all
// with the source files each was checked against.
type mcpCaveatID string

const (
	mcpCaveatLoadSingleProducer    mcpCaveatID = "load-single-producer"
	mcpCaveatReaper30Minutes       mcpCaveatID = "reaper-30-minutes"
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
		Refs: []string{mcpJava + "engine/TestOrchestrator.java:904-913"},
	},
	{
		ID: mcpCaveatReaper30Minutes,
		Text: "A run still RUNNING 30 minutes after it was created is marked FAILED with a timeout error by a reaper " +
			"that checks every 60 seconds. The limit is kates.engine.max-duration-ms (default 1800000) and counts " +
			"time spent waiting to start.",
		Refs: []string{
			"kates/src/main/resources/application.properties:277-278",
			mcpJava + "engine/TestTimeoutReaper.java:32-56",
		},
	},
	{
		ID: mcpCaveatMergedSpecOnly,
		Text: "The backend stores only the spec merged with the test type's defaults. applyTypeDefaults copies 14 fields " +
			"and drops targetThroughput, consumerGroup, fetchMinBytes, fetchMaxWaitMs, enableIdempotence, " +
			"enableTransactions and enableCrc, so a run shows those unset or at their Java defaults whatever the " +
			"request said, and the run executes that way: an INTEGRITY run always uses consumer group integrity-cg " +
			"and runs without idempotence or transactions.",
		Refs: []string{
			mcpJava + "engine/TestOrchestrator.java:145,156,197",
			mcpJava + "engine/TestOrchestrator.java:845-880",
			mcpJava + "engine/TestOrchestrator.java:958-972",
			mcpJava + "domain/TestSpec.java:71-85",
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
			mcpJava + "service/SecurityService.java:1373-1464",
			mcpJava + "service/SecurityService.java:1562-1576",
			mcpJava + "service/ClusterHealthService.java:72-93",
		},
	},
	{
		ID: mcpCaveatSecurityTrendInMemory,
		Text: "The security grade trend is an in-memory list of the last 100 audits in the backend pod, lost on " +
			"restart. Every audit call appends to it, including the audits that compliance, baseline, drift and gate " +
			"run internally and the calls agents make, so it mostly reflects API traffic.",
		Refs: []string{
			mcpJava + "service/SecurityService.java:42,766-773",
			mcpJava + "service/SecurityService.java:779,842,904,969",
			mcpJava + "service/SecurityService.java:1639-1660",
		},
	},
	{
		ID: mcpCaveatTuningOneMeasurement,
		Text: "A TUNE_* run executes one produce task with the spec's single configuration. The tuning report copies " +
			"that one summary into every step, so all steps show the same numbers and the best step is always step 0.",
		Refs: []string{
			mcpJava + "engine/TestOrchestrator.java:973-974",
			mcpJava + "trogdor/SpecFactory.java:38-39",
			mcpJava + "engine/TuningTestRunner.java:101-139",
		},
	},
	{
		ID: mcpCaveatTrendsMixSpecs,
		Text: "GET /api/trends selects runs by test type and date only, so a trend mixes every run of that type " +
			"whatever its spec (partitions, record size, throughput).",
		Refs: []string{
			mcpJava + "trend/TrendResource.java:28-55",
			mcpJava + "trend/TrendService.java:45-46,175-179",
		},
	},
	{
		ID: mcpCaveatMinISRBrokerLevel,
		Text: "Topic detail keeps only topic-level and default config entries, so min.insync.replicas set at broker " +
			"level, which is where the kafka-cluster chart sets it (2), is missing from a topic's configs. Its absence " +
			"does not mean 1.",
		Refs: []string{
			mcpJava + "service/TopicService.java:170-184",
			"charts/kafka-cluster/values.yaml:137",
		},
	},
	{
		ID: mcpCaveatPrometheusUnreachable,
		Text: "Kafka metrics in disruption reports come from Prometheus at kates.prometheus.url, which defaults to " +
			"http://prometheus.monitoring.svc:9090. The monitoring chart installs kube-prometheus-stack, which does " +
			"not create that Service, and no chart sets the URL, so on a default install the metrics can be missing: " +
			"missing means not measured, not zero. An SLA verdict leaves a check it could not measure out of its grade " +
			"and lists it, with the reason, under unevaluated (with nothing measured the grade is \"-\"), so read a grade " +
			"that has unevaluated checks as partial.",
		Refs: []string{
			"kates/src/main/resources/application.properties:274-275",
			mcpJava + "disruption/PrometheusMetricsCapture.java:31-32",
			mcpJava + "disruption/SlaGrader.java:26,140,153,170-178,183,191-192",
			"charts/monitoring/Chart.yaml:18",
		},
	},
	{
		ID: mcpCaveatChaosTimesApproximate,
		Text: "On the default litmus-crd chaos provider the start time is taken before the ChaosEngine is created and " +
			"the end is found by polling every 5 seconds, so fault start, end and duration are approximate by several seconds.",
		Refs: []string{
			"kates/src/main/resources/application.properties:219-220",
			mcpJava + "chaos/LitmusChaosProvider.java:65,88",
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
