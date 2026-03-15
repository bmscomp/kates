package cmd

var docEntries = []DocEntry{
	{
		Name:     "health",
		Category: "Core",
		Synopsis: "kates health",
		Short:    "Show Kates system health and Kafka connectivity",
		Description: "Displays the overall health status of the Kates platform,\n" +
			"including the backend engine, Kafka cluster connectivity,\n" +
			"bootstrap servers, and the active test backend.",
		Examples: []string{"kates health", "kates health -o json"},
		SeeAlso:  []string{"status", "doctor"},
	},
	{
		Name:     "status",
		Category: "Core",
		Synopsis: "kates status",
		Short:    "Quick one-line status of Kates and running tests",
		Description: "Prints a compact single-line summary showing system state,\n" +
			"active test count, and last completed test.",
		Examples: []string{"kates status"},
		SeeAlso:  []string{"health", "top"},
	},
	{
		Name:     "version",
		Category: "Core",
		Synopsis: "kates version",
		Short:    "CLI, API, and runtime version info",
		Description: "Displays the CLI version, the connected API server version,\n" +
			"and runtime environment details.",
		Examples: []string{"kates version", "kates version -o json"},
	},
	{
		Name:     "doctor",
		Category: "Core",
		Synopsis: "kates doctor",
		Short:    "Pre-flight cluster readiness checklist",
		Description: "Runs a series of diagnostic checks against the Kafka cluster\n" +
			"and Kates backend to verify everything is properly configured\n" +
			"and ready for testing. Checks connectivity, topic access,\n" +
			"broker health, and engine availability.",
		Examples: []string{"kates doctor"},
		SeeAlso:  []string{"health", "cluster check"},
	},
	{
		Name:     "cluster info",
		Category: "Cluster",
		Synopsis: "kates cluster info",
		Short:    "Show Kafka cluster metadata",
		Description: "Displays cluster ID, broker count, controller node,\n" +
			"and per-broker details including host, port, and rack/AZ.",
		Examples: []string{"kates cluster info", "kates cluster info -o json"},
		SeeAlso:  []string{"kafka brokers", "cluster check"},
	},
	{
		Name:        "cluster topics",
		Category:    "Cluster",
		Synopsis:    "kates cluster topics",
		Short:       "List all Kafka topics",
		Description: "Lists all topics in the Kafka cluster with basic metadata.",
		Examples:    []string{"kates cluster topics"},
		SeeAlso:     []string{"kafka topics", "cluster topics describe"},
	},
	{
		Name:     "cluster topics describe",
		Category: "Cluster",
		Synopsis: "kates cluster topics describe <topic-name>",
		Short:    "Show detailed topic metadata, configs, and partition health",
		Description: "Displays comprehensive topic information including partition\n" +
			"assignments, leader/ISR status, replication factor, and\n" +
			"non-default topic configurations.",
		Examples: []string{
			"kates cluster topics describe my-topic",
			"kates cluster topics describe my-topic -o json",
		},
		SeeAlso: []string{"kafka topic", "cluster topics"},
	},
	{
		Name:     "cluster broker configs",
		Category: "Cluster",
		Synopsis: "kates cluster broker configs <broker-id>",
		Short:    "Show non-default configuration for a broker",
		Description: "Displays broker-level configuration entries grouped by source\n" +
			"(STATIC_BROKER_CONFIG, DYNAMIC_BROKER_CONFIG, etc.),\n" +
			"highlighting read-only settings.",
		Examples: []string{"kates cluster broker configs 0"},
		SeeAlso:  []string{"cluster info", "kafka brokers"},
	},
	{
		Name:     "cluster check",
		Category: "Cluster",
		Synopsis: "kates cluster check",
		Short:    "Comprehensive cluster health check",
		Description: "Runs a full health assessment of the Kafka cluster including\n" +
			"broker availability, under-replicated partitions, offline\n" +
			"partitions, consumer group states, and ISR health.",
		Examples: []string{"kates cluster check", "kates cluster check -o json"},
		SeeAlso:  []string{"doctor", "cluster info", "cluster topology"},
	},
	{
		Name:     "cluster topology",
		Category: "Cluster",
		Synopsis: "kates cluster topology [flags]",
		Short:    "Full Strimzi/Kafka cluster topology with 26 sections",
		Description: "Displays a comprehensive view of the entire Kafka cluster\n" +
			"including Kubernetes platform, Strimzi operator, KRaft\n" +
			"controllers, broker node pools, entity operator, Cruise\n" +
			"Control, Kafka Exporter, TLS certificates, metrics,\n" +
			"managed topics, users, consumer groups, ACLs, log\n" +
			"directories, feature flags, KafkaRebalances, Drain\n" +
			"Cleaner, StrimziPodSets, NetworkPolicies, PVCs,\n" +
			"Services, Endpoints, Connect, and MirrorMaker2.\n\n" +
			"Requires the Kates backend to be deployed on Kubernetes\n" +
			"with access to Strimzi CRDs and AdminClient APIs.",
		Examples: []string{
			"kates cluster topology",
			"kates cluster topology -o json",
		},
		SeeAlso: []string{"cluster info", "cluster check", "cluster alerts"},
	},
	{
		Name:     "cluster alerts",
		Category: "Cluster",
		Synopsis: "kates cluster alerts [flags]",
		Short:    "Show critical Kafka health alerts from PrometheusRules",
		Description: "Reads PrometheusRule CRDs from the cluster and displays\n" +
			"critical and warning alerts that affect Kafka health.\n" +
			"Alerts are sorted by severity (critical first), showing\n" +
			"name, group, firing threshold, PromQL expression, and\n" +
			"description.\n\n" +
			"Returns exit code 2 when critical alerts are configured,\n" +
			"making it suitable for CI/CD health gates.\n\n" +
			"Covers 16 alert rules across 8 groups: cluster health,\n" +
			"consumer lag, KRaft stability, network latency, operator\n" +
			"availability, replication, performance, Cruise Control,\n" +
			"and certificate expiry.",
		Flags: []DocFlag{
			{Name: "--severity", Type: "string", Desc: "Filter by severity: critical or warning"},
			{Name: "--group", Type: "string", Desc: "Filter by alert group (e.g. kafka.cluster, kafka.kraft)"},
		},
		Examples: []string{
			"kates cluster alerts",
			"kates cluster alerts --severity critical",
			"kates cluster alerts --group kafka.kraft",
			"kates cluster alerts -o json",
			"kates cluster alerts --severity critical && echo 'safe'",
		},
		SeeAlso: []string{"cluster topology", "cluster check"},
	},
	{
		Name:     "cluster groups",
		Category: "Cluster",
		Synopsis: "kates cluster groups",
		Short:    "List consumer groups with state and members",
		Description: "Lists all Kafka consumer groups with their current state\n" +
			"(STABLE, EMPTY, DEAD) and active member count.",
		Examples: []string{"kates cluster groups"},
		SeeAlso:  []string{"kafka groups", "cluster groups describe"},
	},
	{
		Name:     "cluster groups describe",
		Category: "Cluster",
		Synopsis: "kates cluster groups describe <group-id>",
		Short:    "Show consumer group detail with per-partition lag",
		Description: "Displays detailed consumer group information including\n" +
			"per-partition current offset, end offset, and lag.",
		Examples: []string{"kates cluster groups describe my-consumer-group"},
		SeeAlso:  []string{"kafka group", "cluster groups"},
	},
	{
		Name:     "cluster watch",
		Category: "Cluster",
		Synopsis: "kates cluster watch [flags]",
		Short:    "Live-watch Kafka cluster health (refreshing dashboard)",
		Flags: []DocFlag{
			{Name: "--interval", Type: "int", Default: "5", Desc: "Refresh interval in seconds"},
		},
		Examples: []string{"kates cluster watch", "kates cluster watch --interval 10"},
		SeeAlso:  []string{"cluster check", "dashboard"},
	},
	{
		Name:     "kafka brokers",
		Category: "Kafka",
		Synopsis: "kates kafka brokers",
		Short:    "List brokers with ID, host, port, rack, and controller status",
		Description: "Displays a table of all Kafka brokers with their network\n" +
			"endpoints, rack/AZ assignments, and controller designation.",
		Examples: []string{"kates kafka brokers", "kates kafka brokers -o json"},
		SeeAlso:  []string{"cluster info", "kafka topics"},
	},
	{
		Name:     "kafka topics",
		Category: "Kafka",
		Synopsis: "kates kafka topics [flags]",
		Short:    "List all topics with partition, replication, and ISR health",
		Description: "Lists all Kafka topics with colour-coded ISR health badges,\n" +
			"partition counts, and replication factors. Supports substring\n" +
			"filtering.",
		Flags: []DocFlag{
			{Name: "--filter", Type: "string", Desc: "Filter topics by substring match"},
		},
		Examples: []string{
			"kates kafka topics",
			"kates kafka topics --filter perf",
			"kates kafka topics -o json",
		},
		SeeAlso: []string{"kafka topic", "kafka create-topic"},
	},
	{
		Name:     "kafka topic",
		Category: "Kafka",
		Synopsis: "kates kafka topic <name>",
		Short:    "Describe a topic — partitions, ISR, offsets, and configuration",
		Description: "Shows detailed topic information including per-partition\n" +
			"leader assignments, ISR membership, replica lists, and\n" +
			"all topic-level configuration entries.",
		Examples: []string{
			"kates kafka topic my-topic",
			"kates kafka topic __consumer_offsets -o json",
		},
		SeeAlso: []string{"kafka topics", "kafka alter-topic", "kafka consume"},
	},
	{
		Name:     "kafka groups",
		Category: "Kafka",
		Synopsis: "kates kafka groups",
		Short:    "List consumer groups with state, members, and lag summary",
		Description: "Lists all consumer groups with colour-coded state badges\n" +
			"(STABLE, EMPTY, DEAD) and member counts.",
		Examples: []string{"kates kafka groups", "kates kafka groups -o json"},
		SeeAlso:  []string{"kafka group", "cluster groups"},
	},
	{
		Name:     "kafka group",
		Category: "Kafka",
		Synopsis: "kates kafka group <id>",
		Short:    "Describe a consumer group with per-partition offsets and lag",
		Description: "Displays detailed consumer group information including state,\n" +
			"member count, total lag, and per-partition offset breakdown\n" +
			"with colour-coded lag highlights.",
		Examples: []string{"kates kafka group my-consumer-group"},
		SeeAlso:  []string{"kafka groups", "cluster groups describe"},
	},
	{
		Name:     "kafka consume",
		Category: "Kafka",
		Synopsis: "kates kafka consume <topic> [flags]",
		Short:    "Fetch records from a topic (latest N or tail with --follow)",
		Description: "Fetches the latest N records from a Kafka topic and displays\n" +
			"them in a table with partition, offset, timestamp, key, and\n" +
			"value columns. Use --follow to continuously tail new records.",
		Flags: []DocFlag{
			{Name: "--offset", Type: "string", Default: "latest", Desc: "Offset reset: latest or earliest"},
			{Name: "--limit", Type: "int", Default: "20", Desc: "Maximum number of records to fetch"},
			{Name: "--follow", Short: "-f", Type: "bool", Desc: "Tail the topic continuously (like tail -f)"},
		},
		Examples: []string{
			"kates kafka consume my-topic",
			"kates kafka consume my-topic --offset earliest --limit 100",
			"kates kafka consume my-topic -f",
			"kates kafka consume my-topic -o json",
		},
		SeeAlso: []string{"kafka produce", "kafka topic"},
	},
	{
		Name:     "kafka produce",
		Category: "Kafka",
		Synopsis: "kates kafka produce <topic> [flags]",
		Short:    "Produce a record to a topic (from flag or stdin)",
		Description: "Produces a single record to a Kafka topic. The value can be\n" +
			"provided via the --value flag or interactively from stdin.\n" +
			"Returns partition and offset of the produced record.",
		Flags: []DocFlag{
			{Name: "--key", Type: "string", Desc: "Record key (optional)"},
			{Name: "--value", Short: "-v", Type: "string", Desc: "Record value to produce"},
		},
		Examples: []string{
			`kates kafka produce my-topic --value '{"event":"test"}'`,
			"kates kafka produce my-topic --key user-123 --value hello",
			"echo 'payload' | kates kafka produce my-topic",
		},
		SeeAlso: []string{"kafka consume", "kafka topic"},
	},
	{
		Name:     "kafka create-topic",
		Category: "Kafka",
		Synopsis: "kates kafka create-topic <name> [flags]",
		Short:    "Create a new topic with partition, replication, and config options",
		Description: "Creates a new Kafka topic with the specified number of\n" +
			"partitions and replication factor. Supports setting arbitrary\n" +
			"topic-level configuration entries via repeatable --config flags.",
		Flags: []DocFlag{
			{Name: "--partitions", Type: "int", Default: "1", Desc: "Number of partitions"},
			{Name: "--replication-factor", Type: "int", Default: "1", Desc: "Replication factor"},
			{Name: "--config", Type: "string", Desc: "Topic config entry as key=value (repeatable)"},
		},
		Examples: []string{
			"kates kafka create-topic events --partitions 6 --replication-factor 3",
			"kates kafka create-topic logs --partitions 3 --config retention.ms=86400000",
			"kates kafka create-topic compact-topic --config cleanup.policy=compact",
		},
		SeeAlso: []string{"kafka alter-topic", "kafka delete-topic", "kafka topics"},
	},
	{
		Name:     "kafka alter-topic",
		Category: "Kafka",
		Synopsis: "kates kafka alter-topic <name> [flags]",
		Short:    "Alter topic configuration entries",
		Description: "Modifies topic-level configuration using incremental alter.\n" +
			"Specify one or more --config key=value pairs. Only the\n" +
			"specified configs are changed; others remain untouched.",
		Flags: []DocFlag{
			{Name: "--config", Type: "string", Desc: "Config entry to set as key=value (repeatable)"},
		},
		Examples: []string{
			"kates kafka alter-topic my-topic --config retention.ms=172800000",
			"kates kafka alter-topic my-topic --config cleanup.policy=compact --config max.message.bytes=2097152",
		},
		SeeAlso: []string{"kafka create-topic", "kafka topic"},
	},
	{
		Name:     "kafka delete-topic",
		Category: "Kafka",
		Synopsis: "kates kafka delete-topic <name> [flags]",
		Short:    "Delete a topic (with confirmation prompt)",
		Description: "Permanently deletes a Kafka topic and all its data.\n" +
			"Prompts for confirmation unless --yes is specified.",
		Flags: []DocFlag{
			{Name: "--yes", Type: "bool", Desc: "Skip confirmation prompt"},
		},
		Examples: []string{
			"kates kafka delete-topic old-events",
			"kates kafka delete-topic old-events --yes",
		},
		SeeAlso: []string{"kafka create-topic", "kafka topics"},
	},
	{
		Name:     "kafka tui",
		Category: "Kafka",
		Synopsis: "kates kafka tui",
		Short:    "Launch interactive Kafka explorer (full-screen TUI)",
		Description: "Opens a full-screen interactive TUI with three tabs:\n" +
			"Brokers, Topics, and Groups. Navigate with arrow keys,\n" +
			"search with /, consume records with c, and quit with q.",
		Examples: []string{"kates kafka tui"},
		SeeAlso:  []string{"kafka topics", "kafka brokers", "kafka groups"},
	},
	{
		Name:     "test list",
		Category: "Test",
		Synopsis: "kates test list [flags]",
		Short:    "List test runs with optional filters",
		Description: "Lists all performance test runs with type, status, duration,\n" +
			"and key metrics. Supports filtering by test type and status.",
		Flags: []DocFlag{
			{Name: "--type", Type: "string", Desc: "Filter by test type (LOAD, STRESS, SPIKE, etc.)"},
			{Name: "--status", Type: "string", Desc: "Filter by status (PENDING, RUNNING, DONE, FAILED)"},
			{Name: "--page", Type: "int", Default: "0", Desc: "Page number"},
			{Name: "--size", Type: "int", Default: "20", Desc: "Page size"},
		},
		Examples: []string{
			"kates test list",
			"kates test list --type LOAD --status DONE",
			"kates test list --size 50",
		},
		SeeAlso: []string{"test get", "test create"},
	},
	{
		Name:     "test get",
		Category: "Test",
		Synopsis: "kates test get <id>",
		Short:    "Show details of a specific test run",
		Description: "Displays full details of a test run including configuration,\n" +
			"phase results, metrics, and SLA verdict.",
		Examples: []string{"kates test get abc123", "kates test get abc123 -o json"},
		SeeAlso:  []string{"test list", "report show"},
	},
	{
		Name:     "test create",
		Category: "Test",
		Synopsis: "kates test create [flags]",
		Short:    "Start a new performance test",
		Description: "Creates and starts a new performance test with the specified\n" +
			"type, record count, and configuration. Supports all 8 test\n" +
			"types: LOAD, STRESS, SPIKE, ENDURANCE, VOLUME, CAPACITY,\n" +
			"ROUND_TRIP, and INTEGRITY.",
		Flags: []DocFlag{
			{Name: "--type", Type: "string", Default: "LOAD", Desc: "Test type"},
			{Name: "--records", Type: "int", Default: "50000", Desc: "Number of records to produce"},
			{Name: "--producers", Type: "int", Desc: "Number of parallel producers"},
			{Name: "--consumers", Type: "int", Desc: "Number of consumers"},
			{Name: "--partitions", Type: "int", Desc: "Topic partition count"},
			{Name: "--replication-factor", Type: "int", Desc: "Topic replication factor"},
			{Name: "--record-size", Type: "int", Desc: "Record size in bytes"},
			{Name: "--acks", Type: "string", Desc: "Producer acks setting (all, 1, 0)"},
			{Name: "--backend", Type: "string", Desc: "Test backend to use"},
		},
		Examples: []string{
			"kates test create --type LOAD --records 100000",
			"kates test create --type STRESS --producers 8 --records 200000",
			"kates test create --type SPIKE --records 50000 --acks all",
		},
		SeeAlso: []string{"test list", "test get", "scaffold"},
	},
	{
		Name:     "test delete",
		Category: "Test",
		Synopsis: "kates test delete <id>",
		Short:    "Delete a test run",
		Examples: []string{"kates test delete abc123"},
		SeeAlso:  []string{"test list", "test cleanup"},
	},
	{
		Name:     "test apply",
		Category: "Test",
		Synopsis: "kates test apply [flags]",
		Short:    "Apply a YAML test scenario definition",
		Description: "Creates and runs a test from a YAML scenario file. Supports\n" +
			"multi-phase scenarios with SLA gates for CI/CD pipelines.\n" +
			"Use --wait to block until completion.",
		Flags: []DocFlag{
			{Name: "--file", Short: "-f", Type: "string", Desc: "Path to scenario YAML file (required)"},
			{Name: "--wait", Type: "bool", Desc: "Wait for test to complete before returning"},
		},
		Examples: []string{
			"kates test apply -f scenario.yaml",
			"kates test apply -f scenario.yaml --wait",
		},
		SeeAlso: []string{"scaffold", "test create"},
	},
	{
		Name:     "test cleanup",
		Category: "Test",
		Synopsis: "kates test cleanup [flags]",
		Short:    "Delete orphaned tests stuck in RUNNING state",
		Flags: []DocFlag{
			{Name: "--dry-run", Type: "bool", Desc: "Preview what would be deleted without actually deleting"},
		},
		Examples: []string{"kates test cleanup", "kates test cleanup --dry-run"},
		SeeAlso:  []string{"test list", "test delete"},
	},
	{
		Name:     "test compare",
		Category: "Test",
		Synopsis: "kates test compare <id1> <id2>",
		Short:    "Side-by-side comparison of two test runs",
		Description: "Displays a side-by-side metric diff of two test runs with\n" +
			"percentage change indicators and sparkline trends.",
		Examples: []string{"kates test compare abc123 def456"},
		SeeAlso:  []string{"report compare", "diff"},
	},
	{
		Name:     "test summary",
		Category: "Test",
		Synopsis: "kates test summary",
		Short:    "Aggregate statistics across all completed tests",
		Description: "Shows aggregate stats across all completed test runs including\n" +
			"total count by type, average metrics, and overall pass rate.",
		Examples: []string{"kates test summary"},
		SeeAlso:  []string{"test list", "trend"},
	},
	{
		Name:     "test export",
		Category: "Test",
		Synopsis: "kates test export <id> [flags]",
		Short:    "Export test results to a file (CSV or JSON)",
		Flags: []DocFlag{
			{Name: "--format", Type: "string", Default: "csv", Desc: "Export format: csv or json"},
			{Name: "--file", Short: "-f", Type: "string", Desc: "Output file path (default: <id>.<format>)"},
		},
		Examples: []string{
			"kates test export abc123",
			"kates test export abc123 --format json -f results.json",
		},
		SeeAlso: []string{"report export"},
	},
	{
		Name:     "test flame",
		Category: "Test",
		Synopsis: "kates test flame <id>",
		Short:    "ASCII latency distribution chart for a test run",
		Description: "Renders an ASCII histogram showing the latency distribution\n" +
			"of a test run, highlighting p50, p95, p99, and max values.",
		Examples: []string{"kates test flame abc123"},
		SeeAlso:  []string{"report show", "test get"},
	},
	{
		Name:     "test baseline set",
		Category: "Baseline",
		Synopsis: "kates test baseline set <run-id>",
		Short:    "Mark a test run as the baseline for its type",
		Description: "Designates the specified test run as the baseline for its\n" +
			"test type. Future regression checks compare against this run.",
		Examples: []string{"kates test baseline set abc123"},
		SeeAlso:  []string{"test baseline show", "test baseline list", "report regression"},
	},
	{
		Name:     "test baseline unset",
		Category: "Baseline",
		Synopsis: "kates test baseline unset <type>",
		Short:    "Remove the baseline for a test type",
		Examples: []string{"kates test baseline unset LOAD"},
		SeeAlso:  []string{"test baseline set", "test baseline list"},
	},
	{
		Name:     "test baseline show",
		Category: "Baseline",
		Synopsis: "kates test baseline show <type>",
		Short:    "Show the current baseline for a test type",
		Description: "Displays the run ID, test type, and timestamp of the\n" +
			"currently configured baseline.",
		Examples: []string{"kates test baseline show LOAD", "kates test baseline show STRESS -o json"},
		SeeAlso:  []string{"test baseline set", "test baseline list"},
	},
	{
		Name:     "test baseline list",
		Category: "Baseline",
		Synopsis: "kates test baseline list",
		Short:    "List all configured baselines",
		Description: "Displays a table of all test types that have a baseline\n" +
			"configured, with run IDs and timestamps.",
		Examples: []string{"kates test baseline list", "kates test baseline list -o json"},
		SeeAlso:  []string{"test baseline set", "test baseline show"},
	},
	{
		Name:     "report regression",
		Category: "Baseline",
		Synopsis: "kates report regression <run-id>",
		Short:    "Compare a test run against its type's baseline",
		Description: "Performs a regression check comparing the specified run's\n" +
			"metrics against the baseline. Reports throughput, latency,\n" +
			"and error rate deltas with colour-coded ▲/▼ indicators.\n" +
			"Flags regression when throughput drops >10%, P99 rises >20%,\n" +
			"or error rate increases.",
		Examples: []string{"kates report regression abc123", "kates report regression abc123 -o json"},
		SeeAlso:  []string{"test baseline set", "test baseline list", "report show"},
	},
	{
		Name:     "tune run",
		Category: "Tuning",
		Synopsis: "kates tune run <type>",
		Short:    "Run a parameter sweep tuning test",
		Description: "Executes a tuning test that sweeps a single configuration\n" +
			"parameter across multiple values. Types: TUNE_REPLICATION,\n" +
			"TUNE_ACKS, TUNE_BATCHING, TUNE_COMPRESSION, TUNE_PARTITIONS.",
		Examples: []string{"kates tune run compression", "kates tune run TUNE_BATCHING"},
		SeeAlso:  []string{"tune report", "tune types"},
	},
	{
		Name:     "tune report",
		Category: "Tuning",
		Synopsis: "kates tune report <run-id>",
		Short:    "Show tuning comparison report",
		Description: "Displays a ranked comparison table of each parameter value\n" +
			"tested, with throughput, latency, and error rate columns.\n" +
			"Marks the best (★) and worst (▼) configurations.",
		Examples: []string{"kates tune report abc123"},
		SeeAlso:  []string{"tune run", "tune types"},
	},
	{
		Name:     "tune types",
		Category: "Tuning",
		Synopsis: "kates tune types",
		Short:    "List available tuning tests",
		Description: "Shows all available tuning test types with their target\n" +
			"parameter, number of sweep steps, and description.",
		Examples: []string{"kates tune types"},
		SeeAlso:  []string{"tune run", "tune report"},
	},
	{
		Name:     "report show",
		Category: "Report",
		Synopsis: "kates report show <id>",
		Short:    "Show the full report for a test run",
		Description: "Displays the complete performance report with summary metrics,\n" +
			"per-phase breakdowns, SLA verdict, and violation details.",
		Examples: []string{"kates report show abc123", "kates report show abc123 -o json"},
		SeeAlso:  []string{"report summary", "report export"},
	},
	{
		Name:     "report summary",
		Category: "Report",
		Synopsis: "kates report summary <id>",
		Short:    "Show compact summary metrics for a test run",
		Description: "Displays a one-screen summary of the most important metrics:\n" +
			"throughput, latency percentiles, error rate, and SLA status.",
		Examples: []string{"kates report summary abc123"},
		SeeAlso:  []string{"report show", "test get"},
	},
	{
		Name:     "report compare",
		Category: "Report",
		Synopsis: "kates report compare <id1,id2,...>",
		Short:    "Compare metrics across multiple test runs",
		Examples: []string{"kates report compare abc123,def456"},
		SeeAlso:  []string{"test compare", "diff"},
	},
	{
		Name:     "report export",
		Category: "Report",
		Synopsis: "kates report export <id> [flags]",
		Short:    "Export report as CSV, JUnit XML, Heatmap, Markdown, or HTML",
		Description: "Exports a test report in the specified format. CSV and JUnit\n" +
			"are ideal for CI integration. Markdown embeds well in GitHub PRs.\n" +
			"HTML generates a self-contained dark-mode dashboard with\n" +
			"throughput grids, latency bars, SLA badges, and phase breakdowns.",
		Flags: []DocFlag{
			{Name: "--format", Type: "string", Default: "csv", Desc: "Export format: csv, junit, heatmap, heatmap-csv, md, or html"},
		},
		Examples: []string{
			"kates report export abc123",
			"kates report export abc123 --format junit",
			"kates report export abc123 --format md",
			"kates report export abc123 --format html",
			"kates report export abc123 --format heatmap",
		},
		SeeAlso: []string{"report show"},
	},
	{
		Name:     "report brokers",
		Category: "Report",
		Synopsis: "kates report brokers <run-id>",
		Short:    "Show per-broker metric breakdown for a test run",
		Description: "Displays throughput, latency, and partition metrics for each\n" +
			"broker during a test run, including leader share and skew.",
		Examples: []string{"kates report brokers abc123"},
		SeeAlso:  []string{"report snapshot", "report show"},
	},
	{
		Name:     "report snapshot",
		Category: "Report",
		Synopsis: "kates report snapshot <run-id>",
		Short:    "Show cluster topology captured at test time",
		Description: "Displays the cluster snapshot taken at the start of a test,\n" +
			"including broker layout, partition assignments, and ISR state.",
		Examples: []string{"kates report snapshot abc123"},
		SeeAlso:  []string{"report brokers"},
	},
	{
		Name:     "trend",
		Category: "Analysis",
		Synopsis: "kates trend [flags]",
		Short:    "Historical performance trends with sparkline charts",
		Description: "Analyzes performance trends over time for a specific test type\n" +
			"and metric. Shows baseline, data points with sparklines, and\n" +
			"highlights any regressions.",
		Flags: []DocFlag{
			{Name: "--type", Type: "string", Default: "LOAD", Desc: "Test type to analyze"},
			{Name: "--metric", Type: "string", Default: "p99LatencyMs", Desc: "Metric to track"},
			{Name: "--days", Type: "int", Default: "30", Desc: "Lookback period in days"},
			{Name: "--baseline-window", Type: "int", Default: "5", Desc: "Number of runs for baseline calculation"},
			{Name: "--phase", Type: "string", Desc: "Filter to a specific test phase"},
		},
		Examples: []string{
			"kates trend",
			"kates trend --type STRESS --metric avgThroughputRecPerSec",
			"kates trend --days 14 --phase spike",
		},
		SeeAlso: []string{"benchmark", "test summary"},
	},
	{
		Name:     "benchmark",
		Category: "Analysis",
		Synopsis: "kates benchmark [flags]",
		Short:    "Run a full test battery with letter-grade scorecard",
		Description: "Executes LOAD → STRESS → SPIKE tests in sequence and produces\n" +
			"a letter-grade scorecard (A through F) based on throughput,\n" +
			"latency, and error rate thresholds.",
		Flags: []DocFlag{
			{Name: "--records", Type: "int", Default: "50000", Desc: "Records per test"},
			{Name: "--backend", Type: "string", Desc: "Test backend to use"},
		},
		Examples: []string{
			"kates benchmark",
			"kates benchmark --records 100000",
		},
		SeeAlso: []string{"gate", "test create"},
	},
	{
		Name:     "gate",
		Category: "Analysis",
		Synopsis: "kates gate [flags]",
		Short:    "CI quality gate — exit non-zero if grade is below threshold",
		Description: "Runs a test and assigns a letter grade. Exits with code 1 if\n" +
			"the grade is below the specified minimum. Designed for CI/CD\n" +
			"pipeline integration.",
		Flags: []DocFlag{
			{Name: "--min-grade", Type: "string", Default: "C", Desc: "Minimum passing grade (A, B, C, D, F)"},
			{Name: "--type", Type: "string", Default: "LOAD", Desc: "Test type to run"},
			{Name: "--records", Type: "int", Default: "50000", Desc: "Number of records"},
			{Name: "--backend", Type: "string", Desc: "Benchmark backend"},
			{Name: "--timeout", Type: "int", Default: "180", Desc: "Timeout in seconds"},
		},
		Examples: []string{
			"kates gate --min-grade B --type STRESS",
			"kates gate --min-grade A --records 100000 --timeout 300",
		},
		SeeAlso: []string{"benchmark", "test create"},
	},
	{
		Name:     "explain",
		Category: "Analysis",
		Synopsis: "kates explain <id>",
		Short:    "Plain-English summary and verdict for a test run",
		Description: "Generates a human-readable narrative explaining what happened\n" +
			"during a test run, including performance highlights, anomalies,\n" +
			"and a final verdict.",
		Examples: []string{"kates explain abc123"},
		SeeAlso:  []string{"report show", "test get"},
	},
	{
		Name:     "replay",
		Category: "Analysis",
		Synopsis: "kates replay <id>",
		Short:    "Re-run a previous test with the same parameters",
		Description: "Creates a new test run using the exact same configuration\n" +
			"as a previous run. Useful for reproducibility testing.",
		Examples: []string{"kates replay abc123"},
		SeeAlso:  []string{"test create", "test get"},
	},
	{
		Name:     "diff",
		Category: "Analysis",
		Synopsis: "kates diff <id1> <id2>",
		Short:    "Side-by-side comparison of two test run reports",
		Examples: []string{"kates diff abc123 def456"},
		SeeAlso:  []string{"test compare", "report compare"},
	},
	{
		Name:     "scenario-diff",
		Category: "Analysis",
		Synopsis: "kates scenario-diff <id> [flags]",
		Short:    "Compare a scenario YAML against actual test results",
		Description: "Loads a YAML scenario and compares its expected SLA thresholds\n" +
			"against the actual results of a completed test run.",
		Examples: []string{"kates scenario-diff abc123 -f scenario.yaml"},
		SeeAlso:  []string{"test apply", "diff"},
	},
	{
		Name:     "disruption run",
		Category: "Disruption",
		Synopsis: "kates disruption run [flags]",
		Short:    "Execute a disruption plan from a JSON config file",
		Description: "Runs chaos engineering experiments against the Kafka cluster\n" +
			"using LitmusChaos. Supports dry-run mode, SLA breach detection,\n" +
			"and JUnit output for CI/CD integration.",
		Flags: []DocFlag{
			{Name: "--config", Type: "string", Desc: "Path to disruption plan JSON config (required)"},
			{Name: "--dry-run", Type: "bool", Desc: "Validate plan without executing"},
			{Name: "--fail-on-sla-breach", Type: "bool", Desc: "Exit code 1 if SLA is violated"},
			{Name: "--output-junit", Type: "string", Desc: "Write JUnit XML report to file"},
		},
		Examples: []string{
			"kates disruption run --config plan.json",
			"kates disruption run --config plan.json --dry-run",
			"kates disruption run --config plan.json --fail-on-sla-breach --output-junit report.xml",
		},
		SeeAlso: []string{"disruption types", "disruption playbook list"},
	},
	{
		Name:     "disruption list",
		Category: "Disruption",
		Synopsis: "kates disruption list",
		Short:    "List recent disruption test reports",
		Examples: []string{"kates disruption list"},
		SeeAlso:  []string{"disruption status"},
	},
	{
		Name:     "disruption status",
		Category: "Disruption",
		Synopsis: "kates disruption status <id>",
		Short:    "Show disruption test report",
		Examples: []string{"kates disruption status abc123"},
		SeeAlso:  []string{"disruption list", "disruption timeline"},
	},
	{
		Name:     "disruption timeline",
		Category: "Disruption",
		Synopsis: "kates disruption timeline <id>",
		Short:    "Show pod event timeline for a disruption test",
		Examples: []string{"kates disruption timeline abc123"},
		SeeAlso:  []string{"disruption status"},
	},
	{
		Name:     "disruption types",
		Category: "Disruption",
		Synopsis: "kates disruption types",
		Short:    "List available disruption types",
		Examples: []string{"kates disruption types"},
		SeeAlso:  []string{"disruption run"},
	},
	{
		Name:     "disruption kafka-metrics",
		Category: "Disruption",
		Synopsis: "kates disruption kafka-metrics <id>",
		Short:    "Show Kafka intelligence metrics for a disruption test",
		Description: "Displays ISR tracking, consumer lag, and leader targeting\n" +
			"metrics collected during a disruption experiment.",
		Examples: []string{"kates disruption kafka-metrics abc123"},
		SeeAlso:  []string{"disruption status"},
	},
	{
		Name:     "disruption watch",
		Category: "Disruption",
		Synopsis: "kates disruption watch <id>",
		Short:    "Stream real-time progress events from a running disruption",
		Examples: []string{"kates disruption watch abc123"},
		SeeAlso:  []string{"disruption status"},
	},
	{
		Name:     "disruption playbook list",
		Category: "Disruption",
		Synopsis: "kates disruption playbook list",
		Short:    "List available disruption playbooks",
		Examples: []string{"kates disruption playbook list"},
		SeeAlso:  []string{"disruption playbook run"},
	},
	{
		Name:     "disruption playbook run",
		Category: "Disruption",
		Synopsis: "kates disruption playbook run <name>",
		Short:    "Execute a pre-built disruption playbook",
		Examples: []string{"kates disruption playbook run broker-kill"},
		SeeAlso:  []string{"disruption playbook list", "disruption run"},
	},
	{
		Name:     "disruption schedule list",
		Category: "Disruption",
		Synopsis: "kates disruption schedule list",
		Short:    "List disruption schedules",
		Examples: []string{"kates disruption schedule list"},
		SeeAlso:  []string{"disruption schedule create"},
	},
	{
		Name:     "disruption schedule create",
		Category: "Disruption",
		Synopsis: "kates disruption schedule create <name> [flags]",
		Short:    "Create a new disruption schedule",
		Flags: []DocFlag{
			{Name: "--playbook", Type: "string", Desc: "Playbook name"},
			{Name: "--cron", Type: "string", Desc: "Cron expression (5-field)"},
		},
		Examples: []string{
			"kates disruption schedule create nightly-chaos --playbook broker-kill --cron '0 3 * * *'",
		},
		SeeAlso: []string{"disruption schedule list"},
	},
	{
		Name:     "disruption schedule delete",
		Category: "Disruption",
		Synopsis: "kates disruption schedule delete <id>",
		Short:    "Delete a disruption schedule",
		Examples: []string{"kates disruption schedule delete abc123"},
		SeeAlso:  []string{"disruption schedule list"},
	},
	{
		Name:     "schedule list",
		Category: "Scheduling",
		Synopsis: "kates schedule list",
		Short:    "List all scheduled tests",
		Examples: []string{"kates schedule list"},
		SeeAlso:  []string{"schedule get", "schedule create"},
	},
	{
		Name:     "schedule get",
		Category: "Scheduling",
		Synopsis: "kates schedule get <id>",
		Short:    "Show details of a scheduled test",
		Examples: []string{"kates schedule get s1"},
		SeeAlso:  []string{"schedule list"},
	},
	{
		Name:     "schedule create",
		Category: "Scheduling",
		Synopsis: "kates schedule create [flags]",
		Short:    "Create a new scheduled test",
		Description: "Creates a recurring test schedule using a cron expression.\n" +
			"The scheduled test runs automatically at the specified interval.",
		Flags: []DocFlag{
			{Name: "--name", Type: "string", Desc: "Schedule name (required)"},
			{Name: "--cron", Type: "string", Desc: "Cron expression (required)"},
			{Name: "--type", Type: "string", Desc: "Test type"},
			{Name: "--enabled", Type: "bool", Default: "true", Desc: "Whether the schedule is active"},
		},
		Examples: []string{
			"kates schedule create --name nightly-load --cron '0 0 * * *' --type LOAD",
		},
		SeeAlso: []string{"schedule list", "schedule delete"},
	},
	{
		Name:     "schedule delete",
		Category: "Scheduling",
		Synopsis: "kates schedule delete <id>",
		Short:    "Delete a scheduled test",
		Examples: []string{"kates schedule delete s1"},
		SeeAlso:  []string{"schedule list"},
	},
	{
		Name:     "resilience run",
		Category: "Resilience",
		Synopsis: "kates resilience run [flags]",
		Short:    "Run combined performance + chaos resilience tests",
		Description: "Executes a combined performance and chaos resilience test\n" +
			"using a JSON configuration file. Measures impact deltas\n" +
			"between baseline and chaos states.",
		Flags: []DocFlag{
			{Name: "--config", Type: "string", Desc: "Path to resilience test config JSON (required)"},
		},
		Examples: []string{"kates resilience run --config resilience.json"},
		SeeAlso:  []string{"disruption run", "benchmark"},
	},
	{
		Name:     "ctx show",
		Category: "Config",
		Synopsis: "kates ctx show",
		Short:    "Show all contexts and current selection",
		Examples: []string{"kates ctx show"},
		SeeAlso:  []string{"ctx set", "ctx use"},
	},
	{
		Name:     "ctx set",
		Category: "Config",
		Synopsis: "kates ctx set <name> [flags]",
		Short:    "Create or update a context",
		Flags: []DocFlag{
			{Name: "--url", Type: "string", Desc: "Kates API base URL (required)"},
			{Name: "--output", Type: "string", Desc: "Default output format for this context"},
		},
		Examples: []string{
			"kates ctx set local --url http://localhost:30083",
			"kates ctx set staging --url https://kates.staging.internal --output json",
		},
		SeeAlso: []string{"ctx use", "ctx show"},
	},
	{
		Name:     "ctx use",
		Category: "Config",
		Synopsis: "kates ctx use <name>",
		Short:    "Switch to a context",
		Examples: []string{"kates ctx use local", "kates ctx use staging"},
		SeeAlso:  []string{"ctx set", "ctx show"},
	},
	{
		Name:     "ctx delete",
		Category: "Config",
		Synopsis: "kates ctx delete <name>",
		Short:    "Remove a context",
		Examples: []string{"kates ctx delete old-server"},
		SeeAlso:  []string{"ctx show"},
	},
	{
		Name:     "ctx current",
		Category: "Config",
		Synopsis: "kates ctx current",
		Short:    "Print the active context name and URL",
		Examples: []string{"kates ctx current"},
		SeeAlso:  []string{"ctx show", "ctx use"},
	},
	{
		Name:     "dashboard",
		Category: "Observability",
		Synopsis: "kates dashboard [flags]",
		Short:    "Full-screen monitoring dashboard with live metrics",
		Description: "Launches a full-screen terminal dashboard showing live\n" +
			"system health, running tests, recent results, and cluster\n" +
			"metrics. Refreshes automatically.",
		Flags: []DocFlag{
			{Name: "--interval", Type: "int", Default: "3", Desc: "Refresh interval in seconds"},
		},
		Examples: []string{"kates dashboard", "kates dash", "kates dashboard --interval 10"},
		SeeAlso:  []string{"top", "cluster watch"},
	},
	{
		Name:     "top",
		Category: "Observability",
		Synopsis: "kates top",
		Short:    "Live view of running tests (like kubectl top)",
		Description: "Displays a continuously refreshing view of active test runs\n" +
			"with real-time progress and metrics.",
		Examples: []string{"kates top"},
		SeeAlso:  []string{"dashboard", "test list"},
	},
	{
		Name:     "webhook list",
		Category: "Observability",
		Synopsis: "kates webhook list",
		Short:    "List registered webhooks",
		Examples: []string{"kates webhook list"},
		SeeAlso:  []string{"webhook register", "webhook delete"},
	},
	{
		Name:     "webhook register",
		Category: "Observability",
		Synopsis: "kates webhook register <name> --url <url>",
		Short:    "Register a webhook for test completion events",
		Flags: []DocFlag{
			{Name: "--url", Type: "string", Desc: "Webhook endpoint URL (required)"},
		},
		Examples: []string{
			"kates webhook register slack-notify --url https://hooks.slack.com/services/xxx",
		},
		SeeAlso: []string{"webhook list", "webhook delete"},
	},
	{
		Name:     "webhook delete",
		Category: "Observability",
		Synopsis: "kates webhook delete <name>",
		Short:    "Remove a registered webhook",
		Examples: []string{"kates webhook delete slack-notify"},
		SeeAlso:  []string{"webhook list"},
	},
	{
		Name:     "test scaffold",
		Category: "Toolbox",
		Synopsis: "kates test scaffold [subcommand]",
		Short:    "Browse and export built-in test scenario templates",
		Description: "The scenario library ships 7 curated YAML templates embedded\n" +
			"in the CLI binary via embed.FS. Use 'list' to browse, 'show'\n" +
			"for a syntax-highlighted preview, and 'export' to write to disk.",
		Examples: []string{
			"kates test scaffold list",
			"kates test scaffold show quick-load",
			"kates test scaffold export ci-gate",
			"kates test scaffold export --all",
		},
		SeeAlso: []string{"test apply", "init"},
	},
	{
		Name:     "test scaffold list",
		Category: "Toolbox",
		Synopsis: "kates test scaffold list",
		Short:    "List all 7 built-in scenario templates",
		Description: "Displays a table of all embedded scenario templates with\n" +
			"name, test type, and description. Templates cover quick-load,\n" +
			"production-load, stress-test, endurance-soak, exactly-once,\n" +
			"spike-test, and ci-gate scenarios.",
		Examples: []string{"kates test scaffold list"},
		SeeAlso:  []string{"test scaffold show", "test scaffold export"},
	},
	{
		Name:     "test scaffold show",
		Category: "Toolbox",
		Synopsis: "kates test scaffold show <name>",
		Short:    "Preview a scenario template with syntax highlighting",
		Description: "Renders the YAML content of a built-in scenario template\n" +
			"with syntax-highlighted keys. Shows export and run hints.",
		Examples: []string{
			"kates test scaffold show quick-load",
			"kates test scaffold show production-load",
		},
		SeeAlso: []string{"test scaffold list", "test scaffold export"},
	},
	{
		Name:     "test scaffold export",
		Category: "Toolbox",
		Synopsis: "kates test scaffold export [name] [flags]",
		Short:    "Export scenario template(s) to the file system",
		Description: "Writes one or all scenario templates from the embedded\n" +
			"library to the current directory (or --dir). Skips files\n" +
			"that already exist.",
		Flags: []DocFlag{
			{Name: "--all", Type: "bool", Desc: "Export all 7 templates"},
			{Name: "--dir", Short: "-d", Type: "string", Desc: "Output directory (default: current)"},
		},
		Examples: []string{
			"kates test scaffold export quick-load",
			"kates test scaffold export --all",
			"kates test scaffold export production-load --dir ./scenarios/",
		},
		SeeAlso: []string{"test scaffold list", "test apply"},
	},
	{
		Name:     "init",
		Category: "Toolbox",
		Synopsis: "kates init [flags]",
		Short:    "Initialize a new Kates workspace with config, scenarios, and CI gate",
		Description: "Sets up a complete Kates project in the current directory:\n" +
			"1) Creates ~/.kates.yaml with a named context.\n" +
			"2) Exports all 7 built-in scenario templates to scenarios/.\n" +
			"3) Generates kates-ci.sh — a CI pipeline gate script with\n" +
			"   KATES_URL env var support and exit code propagation.",
		Flags: []DocFlag{
			{Name: "--name", Type: "string", Default: "default", Desc: "Context name"},
			{Name: "--url", Type: "string", Default: "http://localhost:8080", Desc: "Kates API URL"},
			{Name: "--dir", Type: "string", Default: ".", Desc: "Project directory to scaffold into"},
			{Name: "--no-scenarios", Type: "bool", Desc: "Skip scenario template export"},
			{Name: "--no-ci", Type: "bool", Desc: "Skip CI gate script generation"},
		},
		Examples: []string{
			"kates init",
			"kates init --url http://kates.internal:8080 --name production",
			"kates init --no-scenarios --no-ci",
		},
		SeeAlso: []string{"test scaffold list", "ctx set"},
	},
	{
		Name:     "audit",
		Category: "Observability",
		Synopsis: "kates audit [flags]",
		Short:    "Show audit log of all mutating operations",
		Description: "Displays a chronological log of all mutations (test creates/deletes,\n" +
			"topic changes, disruption runs) with timestamps, action types,\n" +
			"and details. Supports filtering by event type and time range.\n" +
			"Action badges: green \"+ CREATE\", red \"- DELETE\", cyan \"~ UPDATE\".",
		Flags: []DocFlag{
			{Name: "--limit", Type: "int", Default: "50", Desc: "Maximum number of events to show"},
			{Name: "--type", Type: "string", Desc: "Filter by event type (test, topic, disruption, resilience)"},
			{Name: "--since", Type: "string", Desc: "Show events after this ISO-8601 timestamp"},
		},
		Examples: []string{
			"kates audit",
			"kates audit --limit 20 --type test",
			"kates audit --type topic --since 2024-01-01T00:00:00Z",
			"kates audit -o json",
		},
		SeeAlso: []string{"test list", "dashboard"},
	},
	{
		Name:     "cluster diff",
		Category: "Analysis",
		Synopsis: "kates cluster diff --from <ctx> --to <ctx>",
		Short:    "Compare Kafka cluster state between two contexts",
		Description: "Compares topics, consumer groups, and configurations between\n" +
			"two named contexts. Shows added, removed, and changed resources\n" +
			"in a colour-coded diff table.",
		Flags: []DocFlag{
			{Name: "--from", Type: "string", Desc: "Source context name (required)"},
			{Name: "--to", Type: "string", Desc: "Target context name (required)"},
		},
		Examples: []string{
			"kates cluster diff --from local --to staging",
			"kates cluster diff --from dev --to production",
		},
		SeeAlso: []string{"ctx show", "cluster info"},
	},
	{
		Name:     "ctx export",
		Category: "Config",
		Synopsis: "kates ctx export [name]",
		Short:    "Export context(s) as YAML for team sharing",
		Description: "Marshals the specified context (or all contexts if no name given)\n" +
			"to YAML on stdout. Compatible with 'kates ctx import'.",
		Examples: []string{
			"kates ctx export staging > kates-staging.yaml",
			"kates ctx export > all-contexts.yaml",
		},
		SeeAlso: []string{"ctx import", "ctx show"},
	},
	{
		Name:     "ctx import",
		Category: "Config",
		Synopsis: "kates ctx import [flags]",
		Short:    "Import contexts from YAML file or stdin",
		Description: "Reads context definitions from a YAML file (or stdin) and\n" +
			"merges them into ~/.kates.yaml. Existing contexts with the\n" +
			"same name are overwritten.",
		Flags: []DocFlag{
			{Name: "--file", Short: "-f", Type: "string", Desc: "Path to YAML file (default: stdin)"},
		},
		Examples: []string{
			"kates ctx import -f kates-staging.yaml",
			"cat contexts.yaml | kates ctx import",
		},
		SeeAlso: []string{"ctx export", "ctx show"},
	},
	{
		Name:     "plugin list",
		Category: "Toolbox",
		Synopsis: "kates plugin list",
		Short:    "List discovered plugin commands",
		Description: "Scans ~/.kates/plugins/ and $PATH for executables prefixed\n" +
			"with 'kates-' and displays them with their full paths.\n" +
			"Plugins are invoked as 'kates <name>' (e.g., kates-hello → kates hello).",
		Examples: []string{"kates plugin list"},
		SeeAlso:  []string{"init"},
	},
}
