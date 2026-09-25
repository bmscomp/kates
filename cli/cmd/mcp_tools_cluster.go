package cmd

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"

	"github.com/bmscomp/kates/cli/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"
)

// registerMCPClusterTools registers the cluster tools and the cluster prompt.
// cluster_overview is the reference tool for the others: static description,
// typed output, third-party text fenced, lists capped, caveats named.
// cluster_topology and consumer_group_lag live in their own files, and so
// does the did_kates_cause_this prompt.
func registerMCPClusterTools(s *mcp.Server, deps *mcpDeps) {
	registerMCPClusterOverview(s, deps)
	registerMCPClusterTopology(s, deps)
	registerMCPConsumerGroupLag(s, deps)
	registerMCPClusterPrompts(s)
}

func registerMCPClusterOverview(s *mcp.Server, deps *mcpDeps) {
	addReadTool(s, deps, &mcp.Tool{
		Name:  "cluster_overview",
		Title: "Cluster overview",
		Description: "Start here. Reports the Kafka cluster this server is pinned to and how it is doing: " +
			"clusterId, controller and brokers from the Kafka admin API; a partition health check (status, " +
			"under-replicated and offline partitions, the KRaft quorum leader); and the Kafka alert rules defined " +
			"in PrometheusRule resources, with their severity. Alert rules are definitions, not alerts that are " +
			"firing, and their text is third-party and fenced. Figures can be up to 30 seconds old. Counts are " +
			"always whole; on a large or degraded cluster the lists are cut and long rule text is dropped to fit one " +
			"result, and truncated says so. Takes no arguments and only reads.",
	}, mcpClusterOverview, mcpCaveatAlertRulesNotFiring, mcpCaveatClusterDataCached)
}

// Caps on the lists cluster_overview returns. A lab cluster has a handful of
// brokers and alert rules; the caps keep a large or unhealthy cluster (every
// partition of every topic under-replicated) inside one result. An overview
// needs the worst partitions, not all of them: the counts stay whole, and
// offline partitions come first.
const (
	mcpOverviewMaxBrokers  = 100
	mcpOverviewMaxProblems = 20
	mcpOverviewMaxRules    = 50
)

type mcpNoInput struct{}

type mcpClusterOverviewOut struct {
	ClusterID   string           `json:"clusterId" jsonschema:"the Kafka clusterId, the same as cluster.id"`
	Controller  *mcpBroker       `json:"controller,omitempty" jsonschema:"the broker the Kafka admin API reports as controller"`
	BrokerCount int              `json:"brokerCount"`
	Brokers     []mcpBroker      `json:"brokers" jsonschema:"brokers from the Kafka admin API, by id"`
	Health      mcpClusterHealth `json:"health"`
	AlertRules  mcpAlertRules    `json:"alertRules"`
}

type mcpBroker struct {
	ID   int    `json:"id"`
	Host string `json:"host"`
	Port int    `json:"port"`
	Rack string `json:"rack,omitempty"`
}

type mcpClusterHealth struct {
	Status          string                `json:"status" jsonschema:"HEALTHY; WARNING when partitions are under-replicated; CRITICAL when partitions are offline or the KRaft quorum has no leader"`
	Brokers         int                   `json:"brokers"`
	ControllerID    int                   `json:"controllerId"`
	Topics          int                   `json:"topics"`
	Partitions      int                   `json:"partitions"`
	ConsumerGroups  int                   `json:"consumerGroups"`
	UnderReplicated int                   `json:"underReplicated"`
	Offline         int                   `json:"offline"`
	Problems        []mcpPartitionProblem `json:"problems" jsonschema:"the partitions counted in underReplicated and offline"`
	KraftQuorum     *mcpQuorum            `json:"kraftQuorum,omitempty" jsonschema:"absent when the admin API cannot describe the metadata quorum"`
}

type mcpPartitionProblem struct {
	Topic     string `json:"topic"`
	Partition int    `json:"partition"`
	Issue     string `json:"issue" jsonschema:"OFFLINE or UNDER_REPLICATED"`
	ISR       int    `json:"isr,omitempty" jsonschema:"in-sync replicas, for UNDER_REPLICATED"`
	Replicas  int    `json:"replicas,omitempty" jsonschema:"assigned replicas, for UNDER_REPLICATED"`
}

type mcpQuorum struct {
	LeaderID  int  `json:"leaderId"`
	Voters    int  `json:"voters"`
	Observers int  `json:"observers"`
	HasLeader bool `json:"hasLeader"`
}

type mcpAlertRules struct {
	Available    bool           `json:"available" jsonschema:"false when the backend failed to answer; the other fields are then empty and say nothing about the cluster"`
	ErrorCode    mcpErrorCode   `json:"errorCode,omitempty" jsonschema:"why the rules are not available"`
	RulesScanned int            `json:"rulesScanned" jsonschema:"rules read from PrometheusRule resources before filtering"`
	Critical     int            `json:"critical"`
	Warning      int            `json:"warning"`
	Rules        []mcpAlertRule `json:"rules" jsonschema:"rule definitions, critical first; not alerts that are firing"`
}

type mcpAlertRule struct {
	Name        string       `json:"name"`
	Severity    string       `json:"severity" jsonschema:"critical or warning"`
	Source      string       `json:"source" jsonschema:"the PrometheusRule resource that defines the rule"`
	Group       mcpUntrusted `json:"group,omitempty" jsonschema:"the rule group"`
	For         mcpUntrusted `json:"for,omitempty" jsonschema:"how long the condition must hold before the rule fires"`
	Expr        mcpUntrusted `json:"expr,omitempty" jsonschema:"the PromQL condition"`
	Summary     mcpUntrusted `json:"summary,omitempty" jsonschema:"the rule's summary annotation"`
	Description mcpUntrusted `json:"description,omitempty" jsonschema:"the rule's description annotation"`
}

func mcpClusterOverview(ctx context.Context, call *mcpCall, _ mcpNoInput) (mcpClusterOverviewOut, error) {
	info := call.ClusterInfo()
	out := mcpClusterOverviewOut{
		ClusterID:   info.ClusterID,
		BrokerCount: mcpInt(info.BrokerCount),
	}
	if info.Controller != nil {
		b := mcpBrokerFrom(*info.Controller)
		out.Controller = &b
	}
	brokers := make([]mcpBroker, 0, len(info.Brokers))
	for _, n := range info.Brokers {
		brokers = append(brokers, mcpBrokerFrom(n))
	}
	sort.Slice(brokers, func(i, j int) bool { return brokers[i].ID < brokers[j].ID })
	out.Brokers = mcpCap(call, brokers, mcpOverviewMaxBrokers)

	// The two reads are independent. An alerts failure must not cost the
	// health check (the alert rules come from the Kubernetes API, the check
	// from Kafka), so it is recorded rather than returned. call.Go, not g.Go,
	// so that a panic in either read ends this call, not the server.
	var (
		check     *client.ClusterHealthReport
		alerts    *client.ClusterAlertsResponse
		alertsErr error
	)
	g, gctx := errgroup.WithContext(ctx)
	call.Go(g, func() error {
		var err error
		check, err = call.Client().ClusterCheck(gctx)
		return err
	})
	call.Go(g, func() error {
		alerts, alertsErr = call.Client().ClusterAlerts(gctx)
		return nil
	})
	if err := g.Wait(); err != nil {
		return out, err
	}
	if check == nil {
		return out, &mcpToolError{Code: mcpErrBackend, Message: "The Kates API returned an empty cluster check.", Retryable: true}
	}
	if err := call.CheckCluster(check.ClusterID); err != nil {
		return out, err
	}
	out.Health = mcpHealthFrom(call, check)
	out.AlertRules = mcpAlertRulesFrom(call, alerts, alertsErr)
	mcpFitOverview(call, &out)
	return out, nil
}

// mcpFitOverview leaves detail out of an overview too large for one result.
// cluster_overview takes no arguments, so its caller cannot page it, and
// failing with KATES_RESULT_TOO_LARGE would leave an agent without an
// overview on the large or degraded cluster it most needs one for. The alert
// rules' PromQL goes first, then their descriptions, then their summaries and
// groups; after that the longest list is halved until the result fits. Counts
// are never cut.
func mcpFitOverview(call *mcpCall, out *mcpClusterOverviewOut) {
	drops := []func(r *mcpAlertRule){
		func(r *mcpAlertRule) { r.Expr = "" },
		func(r *mcpAlertRule) { r.Description = "" },
		func(r *mcpAlertRule) { r.Summary, r.Group = "", "" },
	}
	for step := 0; !call.Fits(out); step++ {
		call.MarkTruncated()
		if step < len(drops) {
			for i := range out.AlertRules.Rules {
				drops[step](&out.AlertRules.Rules[i])
			}
			continue
		}
		b, p, r := len(out.Brokers), len(out.Health.Problems), len(out.AlertRules.Rules)
		switch {
		case b == 0 && p == 0 && r == 0:
			return // nothing left to leave out; the guard refuses the result
		case b >= p && b >= r:
			out.Brokers = out.Brokers[:b/2]
		case p >= r:
			out.Health.Problems = out.Health.Problems[:p/2]
		default:
			out.AlertRules.Rules = out.AlertRules.Rules[:r/2]
		}
	}
}

func mcpBrokerFrom(n client.BrokerNode) mcpBroker {
	return mcpBroker{
		ID:   mcpInt(n.ID),
		Host: mcpSanitizeLine(n.Host, 255),
		Port: mcpInt(n.Port),
		Rack: mcpSanitizeLine(n.Rack, 64),
	}
}

func mcpHealthFrom(call *mcpCall, r *client.ClusterHealthReport) mcpClusterHealth {
	h := mcpClusterHealth{
		Status:          mcpSanitizeLine(r.Status, 32),
		Brokers:         r.Brokers,
		ControllerID:    r.ControllerID,
		Topics:          r.Topics,
		Partitions:      r.Partitions,
		ConsumerGroups:  r.ConsumerGroups,
		UnderReplicated: r.PartitionHealth.UnderReplicated,
		Offline:         r.PartitionHealth.Offline,
	}
	problems := make([]mcpPartitionProblem, 0, len(r.PartitionHealth.Problems))
	for _, p := range r.PartitionHealth.Problems {
		topic, _ := p["topic"].(string)
		issue, _ := p["issue"].(string)
		problems = append(problems, mcpPartitionProblem{
			// Kafka limits topic names to letters, digits, '.', '_' and '-',
			// so a topic name cannot carry prose; it is cleaned, not fenced,
			// so an agent can pass it back as an argument.
			Topic:     mcpSanitizeLine(topic, 249),
			Partition: mcpInt(p["partition"]),
			Issue:     mcpSanitizeLine(issue, 32),
			ISR:       mcpInt(p["isr"]),
			Replicas:  mcpInt(p["replicas"]),
		})
	}
	// The backend lists problems in the order it walks a map of topics, with
	// OFFLINE and UNDER_REPLICATED mixed (ClusterHealthService.java:308-329).
	// Offline partitions first, so the cap never drops the ones that make the
	// status CRITICAL; then by topic and partition, so a result is stable.
	sort.SliceStable(problems, func(i, j int) bool {
		a, b := problems[i], problems[j]
		if ra, rb := mcpIssueRank(a.Issue), mcpIssueRank(b.Issue); ra != rb {
			return ra < rb
		}
		if a.Topic != b.Topic {
			return a.Topic < b.Topic
		}
		return a.Partition < b.Partition
	})
	h.Problems = mcpCap(call, problems, mcpOverviewMaxProblems)
	if q := r.KraftQuorum; q != nil {
		h.KraftQuorum = &mcpQuorum{LeaderID: q.LeaderID, Voters: q.Voters, Observers: q.Observers, HasLeader: q.HasLeader}
	}
	return h
}

func mcpIssueRank(issue string) int {
	switch issue {
	case "OFFLINE":
		return 0
	case "UNDER_REPLICATED":
		return 1
	}
	return 2
}

func mcpAlertRulesFrom(call *mcpCall, r *client.ClusterAlertsResponse, err error) mcpAlertRules {
	if err != nil || r == nil {
		code := mcpErrBackend
		if err != nil {
			code = call.deps.classify(err).Code
			call.deps.logger.Warn("cluster_overview: alert rules unavailable", "error", err)
		}
		return mcpAlertRules{Available: false, ErrorCode: code, Rules: []mcpAlertRule{}}
	}
	rules := make([]mcpAlertRule, 0, len(r.Alerts))
	for _, a := range r.Alerts {
		rules = append(rules, mcpAlertRule{
			Name:        mcpSanitizeLine(a.Name, 128),
			Severity:    mcpSanitizeLine(a.Severity, 16),
			Source:      mcpSanitizeLine(a.Source, 253),
			Group:       call.FenceN(a.Group, 200),
			For:         call.FenceN(a.For, 32),
			Expr:        call.FenceN(a.Expr, 500),
			Summary:     call.FenceN(a.Summary, 300),
			Description: call.Fence(a.Description),
		})
	}
	return mcpAlertRules{
		Available:    true,
		RulesScanned: r.TotalRulesScanned,
		Critical:     r.CriticalCount,
		Warning:      r.WarningCount,
		Rules:        mcpCap(call, rules, mcpOverviewMaxRules),
	}
}

// mcpInt reads a JSON number the client decoded into interface{} (the
// cluster info types keep ids and ports untyped). Anything else is 0.
func mcpInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

// The caveats only the cluster tools use.
const (
	mcpCaveatTopologyUnreadLooksEmpty mcpCaveatID = "topology-unread-looks-empty"
	mcpCaveatGroupLagCommitted        mcpCaveatID = "group-lag-from-committed-offsets"
)

// mcpCaveatsCluster holds the caveats only this group's tools use (see mcpCaveats in
// mcp_caveats.go). Add an entry here, with its constant in this file and in
// mcpCaveatIDsCluster in the group's test file.
var mcpCaveatsCluster = []mcpCaveat{
	{
		ID: mcpCaveatTopologyUnreadLooksEmpty,
		Text: "Node pools and controllers come from the Strimzi KafkaNodePool resources, and their pods, labelled " +
			"with the cluster name the backend is configured with (kates.topology.kafka-cluster, default krafter) in " +
			"its namespace (kates.topology.kafka-namespace, default kafka). A Kafka cluster with another name matches " +
			"nothing. The backend reads the pods pool by pool and stops at the first read that fails, logging it and " +
			"keeping the pods it had, so a pool can show fewer pods than it has, or none. An empty list, or a pool " +
			"with fewer pods than replicas, is therefore not evidence that controllers or brokers are missing.",
		Refs: []string{mcpJava + "service/ClusterTopologyService.java:108-112,523-596,599-688"},
	},
	{
		ID: mcpCaveatGroupLagCommitted,
		Text: "Consumer group lag is each partition's latest offset minus the group's committed offset, both read " +
			"when asked. Only partitions with a committed offset are listed, so a partition the group reads but has " +
			"never committed is missing; progress a consumer has made but not yet committed counts as lag; and a " +
			"committed offset past the latest one shows as lag 0.",
		Refs: []string{mcpJava + "service/ConsumerGroupService.java:79-111"},
	},
}
