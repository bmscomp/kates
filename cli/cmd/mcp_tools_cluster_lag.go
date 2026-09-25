package cmd

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bmscomp/kates/cli/client"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"
)

// consumer_group_lag: how far one consumer group is behind, per partition,
// and which broker leads the partitions it is behind on.

const mcpConsumerGroupLagDescription = "How far one consumer group is behind on the pinned Kafka cluster: its state " +
	"and member count, total lag, lag by topic and by the broker that leads each partition now, and the partitions " +
	"with the most lag (committed offset, latest offset, lag, leader). Lag is counted in offsets from the group's " +
	"committed offsets, so partitions the group has never committed are not listed. Leaders are read during the " +
	"call: leadership may have moved since the lag built up, for example after a broker restart or a disruption, " +
	"so lag shown on a broker is not proof that broker caused it. Leaders are looked up for the topics with the " +
	"most lag, up to 20; narrow with topic for the rest. The group id must be given exactly (ids are " +
	"case-sensitive); this server cannot list groups, and it returns the id fenced as third-party text. Only reads."

const (
	// mcpLagDefaultLimit and mcpLagMaxLimit bound the partition list.
	mcpLagDefaultLimit = 50
	mcpLagMaxLimit     = 100
	// mcpLagMaxTopicLookups bounds the topics whose leaders are looked up:
	// each is one request, and each costs the backend a describe of the topic
	// and its configs.
	mcpLagMaxTopicLookups = 20
	// mcpLagLookupConcurrency bounds the topic reads in flight at once.
	mcpLagLookupConcurrency = 4
	mcpLagMaxTopics         = 50
	mcpLagMaxLeaders        = 200
	// mcpLagMaxCommittedTopics bounds the topics listed when a topic filter
	// matches none of the group's committed offsets.
	mcpLagMaxCommittedTopics = 50
	// mcpConsumerGroupMaxRunes bounds a group id argument. Kafka sets no
	// limit; a longer id is refused rather than cut, which would ask for
	// another group.
	mcpConsumerGroupMaxRunes = 255
)

func registerMCPConsumerGroupLag(s *mcp.Server, deps *mcpDeps) {
	addReadTool(s, deps, &mcp.Tool{
		Name:        "consumer_group_lag",
		Title:       "Consumer group lag",
		Description: mcpConsumerGroupLagDescription,
		InputSchema: mcpClusterInputSchema[mcpConsumerGroupLagIn]("consumer_group_lag", func(p map[string]*jsonschema.Schema) {
			one, most := 1, mcpConsumerGroupMaxRunes
			p["group"].MinLength, p["group"].MaxLength = &one, &most
			p["topic"].Pattern = mcpKafkaTopicPattern
			p["limit"].Minimum, p["limit"].Maximum = mcpClusterFloat(1), mcpClusterFloat(mcpLagMaxLimit)
		}),
	}, mcpConsumerGroupLag, mcpCaveatGroupLagCommitted)
}

type mcpConsumerGroupLagIn struct {
	Group string `json:"group" jsonschema:"the consumer group id, exactly as Kafka knows it"`
	Topic string `json:"topic,omitempty" jsonschema:"only this topic's partitions"`
	Limit int    `json:"limit,omitempty" jsonschema:"most partitions to list, largest lag first: 1 to 100 (default 50). partitionCount and totalLag always cover every partition; byLeader and leaderUnknown together cover every partition unless the result is truncated; topics lists the 50 topics with the most lag at most"`
}

// The group id in the result is the argument, fenced: plan §4.1 and §5.8 treat
// consumer group ids as third-party text, since anyone who can join a group
// names it, and the id an agent passes may itself come from an alert or a
// message. mcpValidateConsumerGroup has held it to clean text; the agent keeps
// its own argument to pass back. Nothing the backend says about the group
// (its echo of the id, an error that names it) reaches the result unfenced.
type mcpConsumerGroupLagOut struct {
	Group           mcpUntrusted      `json:"group" jsonschema:"the group id asked for"`
	State           string            `json:"state" jsonschema:"the group's state as Kafka reports it: Stable, Empty (no members), PreparingRebalance, CompletingRebalance, Assigning, Reconciling, Dead or Unknown"`
	Members         int               `json:"members" jsonschema:"consumers in the group now"`
	Topic           string            `json:"topic,omitempty" jsonschema:"the topic filter, when one was given"`
	TopicCommitted  *bool             `json:"topicCommitted,omitempty" jsonschema:"with topic: whether the group has a committed offset on any partition of it. false means the filter matched nothing (a mistyped name, or a topic the group reads but has never committed), not that the group has no lag there; committedTopics then lists the topics it has committed offsets for"`
	CommittedTopics []string          `json:"committedTopics,omitempty" jsonschema:"when topicCommitted is false: the topics the group has committed offsets for, by name, up to 50"`
	PartitionCount  int               `json:"partitionCount" jsonschema:"partitions the group has committed offsets for, after the topic filter"`
	TotalLag        int64             `json:"totalLag" jsonschema:"lag summed over those partitions, in offsets"`
	ByLeader        []mcpLagByLeader  `json:"byLeader" jsonschema:"lag by the broker leading each partition now, largest first; leadership may have moved since the lag built up. Leader -1 means the partition has no leader"`
	LeaderUnknown   *mcpLagTotal      `json:"leaderUnknown,omitempty" jsonschema:"partitions whose leader was not looked up or could not be read; topics[].leaderLookup says which"`
	Topics          []mcpLagTopic     `json:"topics" jsonschema:"lag by topic, largest first"`
	Partitions      []mcpLagPartition `json:"partitions" jsonschema:"partitions, largest lag first, up to limit"`
}

type mcpLagByLeader struct {
	Leader     int   `json:"leader"`
	Partitions int   `json:"partitions"`
	Lag        int64 `json:"lag"`
}

type mcpLagTotal struct {
	Partitions int   `json:"partitions"`
	Lag        int64 `json:"lag"`
}

type mcpLagTopic struct {
	Topic        string `json:"topic"`
	Partitions   int    `json:"partitions"`
	Lag          int64  `json:"lag"`
	LeaderLookup string `json:"leaderLookup" jsonschema:"ok; skipped, for a topic past the 20 with the most lag (narrow with topic); or the error code of a failed read"`
}

type mcpLagPartition struct {
	Topic         string `json:"topic"`
	Partition     int    `json:"partition"`
	CurrentOffset int64  `json:"currentOffset" jsonschema:"the group's committed offset"`
	EndOffset     int64  `json:"endOffset" jsonschema:"the partition's latest offset"`
	Lag           int64  `json:"lag"`
	Leader        *int   `json:"leader,omitempty" jsonschema:"broker id of the partition's leader now, read during this call, which may not be the leader while the lag built up; -1 when it has none; absent when its topic was not looked up or could not be read"`
}

// mcpValidateConsumerGroup refuses a group id that could not be passed on or
// shown as it is. Cleaning would change the id, and so ask Kafka about
// another group, so a group id with characters cleaning removes or replaces
// (control, format and bidi characters, line breaks, tabs, « and ») is
// refused. So is one with a backslash, or a "/"-separated part that is empty,
// "." or ".." once anything from a ';' on is cut: escaped, it is still one
// path segment, but decoded it is a path the read-only transport refuses to
// send, since it cuts each decoded segment at ';' as JAX-RS does with matrix
// parameters (mcpAPIPath). That rule refuses a little more than the
// transport does (a last part such as ";x"), which no real group needs.
func mcpValidateConsumerGroup(field, group string) error {
	const rule = " must be a consumer group id of 1 to 255 characters, without control, format or bidi characters, " +
		"line breaks, tabs, « or », backslashes, or a part between slashes that is empty, \".\" or \"..\" before " +
		"any ';'."
	if group == "" || utf8.RuneCountInString(group) > mcpConsumerGroupMaxRunes || !utf8.ValidString(group) ||
		mcpSanitizeLine(group, 0) != group || strings.Contains(group, `\`) {
		return mcpInvalidArgument(field+rule, group)
	}
	for _, part := range strings.Split(group, "/") {
		seg, _, _ := strings.Cut(part, ";")
		if seg == "" || seg == "." || seg == ".." {
			return mcpInvalidArgument(field+rule, group)
		}
	}
	return nil
}

func mcpConsumerGroupLag(ctx context.Context, call *mcpCall, in mcpConsumerGroupLagIn) (mcpConsumerGroupLagOut, error) {
	var out mcpConsumerGroupLagOut
	if err := mcpValidateConsumerGroup("group", in.Group); err != nil {
		return out, err
	}
	if in.Topic != "" {
		if err := mcpValidateKafkaTopic("topic", in.Topic); err != nil {
			return out, err
		}
	}
	limit := in.Limit
	switch {
	case limit == 0:
		limit = mcpLagDefaultLimit
	case limit < 1 || limit > mcpLagMaxLimit:
		return out, mcpInvalidArgument("limit must be between 1 and 100.", strconv.Itoa(in.Limit))
	}

	g, err := call.Client().ConsumerGroupDetail(ctx, in.Group)
	if err != nil {
		return out, err
	}
	if g == nil {
		return out, &mcpToolError{Code: mcpErrBackend, Message: "The Kates API returned an empty consumer group description.", Retryable: true}
	}
	// Kafka describes a group it does not know as Dead, with no members, and
	// lists no offsets for it, so the backend answers 200, not 404
	// (ConsumerGroupService.java:65-111). The backend's admin client
	// (kafka-clients 3.9.2) falls back from ConsumerGroupDescribe to
	// DescribeGroups v5 at most, which answers an unknown group that way.
	if g.State == "Dead" && g.Members == 0 && len(g.Offsets) == 0 {
		return out, &mcpToolError{
			Code: mcpErrNotFound,
			Message: "Kafka knows no consumer group with this id: it reports the group as Dead, with no members and " +
				"no committed offsets. Group ids are case-sensitive.",
			Detail: in.Group,
		}
	}

	out = mcpConsumerGroupLagOut{
		Group:   call.FenceN(in.Group, mcpConsumerGroupMaxRunes),
		State:   mcpSanitizeLine(g.State, 32),
		Members: g.Members,
		Topic:   in.Topic,
	}
	parts := make([]mcpLagPartition, 0, len(g.Offsets))
	for _, o := range g.Offsets {
		if in.Topic != "" && o.Topic != in.Topic {
			continue
		}
		parts = append(parts, mcpLagPartition{
			// Kafka holds topic names to [a-zA-Z0-9._-]; cleaned, not fenced,
			// so an agent can pass one back as an argument.
			Topic:         mcpSanitizeLine(o.Topic, 249),
			Partition:     o.Partition,
			CurrentOffset: o.CurrentOffset,
			EndOffset:     o.EndOffset,
			Lag:           o.Lag,
		})
		out.TotalLag += o.Lag
	}
	out.PartitionCount = len(parts)
	if in.Topic != "" {
		// A filter that matches nothing would otherwise read as "no lag on
		// that topic". Say so, and name the topics the group does commit on,
		// so a mistyped name can be put right.
		matched := len(parts) > 0
		out.TopicCommitted = &matched
		if !matched {
			out.CommittedTopics = mcpCap(call, mcpLagCommittedTopics(g.Offsets), mcpLagMaxCommittedTopics)
		}
	}

	topics := mcpLagTopicsFrom(parts)
	if err := mcpLagLeaders(ctx, call, topics, parts); err != nil {
		return out, err
	}
	out.ByLeader, out.LeaderUnknown = mcpLagByLeaders(call, parts)

	sort.Slice(parts, func(i, j int) bool {
		a, b := parts[i], parts[j]
		if a.Lag != b.Lag {
			return a.Lag > b.Lag
		}
		if a.Topic != b.Topic {
			return a.Topic < b.Topic
		}
		return a.Partition < b.Partition
	})
	out.Partitions = mcpCap(call, parts, limit)
	out.Topics = mcpCap(call, topics, mcpLagMaxTopics)
	mcpFitLag(call, &out)
	return out, nil
}

// mcpLagCommittedTopics lists, by name, the topics a group has committed
// offsets for.
func mcpLagCommittedTopics(offsets []client.GroupPartitionInfo) []string {
	seen := map[string]bool{}
	names := []string{}
	for _, o := range offsets {
		name := mcpSanitizeLine(o.Topic, 249)
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// mcpLagTopicsFrom sums lag by topic, largest first.
func mcpLagTopicsFrom(parts []mcpLagPartition) []mcpLagTopic {
	idx := map[string]int{}
	topics := []mcpLagTopic{}
	for _, p := range parts {
		i, ok := idx[p.Topic]
		if !ok {
			i = len(topics)
			idx[p.Topic] = i
			topics = append(topics, mcpLagTopic{Topic: p.Topic, LeaderLookup: "skipped"})
		}
		topics[i].Partitions++
		topics[i].Lag += p.Lag
	}
	sort.Slice(topics, func(i, j int) bool {
		if topics[i].Lag != topics[j].Lag {
			return topics[i].Lag > topics[j].Lag
		}
		return topics[i].Topic < topics[j].Topic
	})
	return topics
}

// mcpLagLeaders looks up the leaders of the partitions of the topics with the
// most lag, mcpLagMaxTopicLookups at most, and sets each partition's leader
// and each topic's leaderLookup. A topic that cannot be read leaves its
// partitions' leaders unknown and does not fail the call: the lag is what
// was asked for. The error it returns is a panic in a read, which ends the
// call as it would anywhere else.
func mcpLagLeaders(ctx context.Context, call *mcpCall, topics []mcpLagTopic, parts []mcpLagPartition) error {
	n := min(len(topics), mcpLagMaxTopicLookups)
	if len(topics) > n {
		call.MarkTruncated()
	}
	details := make([]*client.TopicDetail, n)
	errs := make([]error, n)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(mcpLagLookupConcurrency)
	for i := 0; i < n; i++ {
		call.Go(g, func() error {
			details[i], errs[i] = call.Client().MCPTopicDetail(gctx, topics[i].Topic)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	leaders := map[string]map[int]int{}
	for i := 0; i < n; i++ {
		switch {
		case errs[i] != nil:
			topics[i].LeaderLookup = string(call.deps.classify(errs[i]).Code)
			call.deps.logger.Warn("consumer_group_lag: leader lookup failed", "topic", topics[i].Topic, "error", errs[i])
		case details[i] == nil:
			topics[i].LeaderLookup = string(mcpErrBackend)
		default:
			m := make(map[int]int, len(details[i].PartitionInfo))
			for _, p := range details[i].PartitionInfo {
				m[p.Partition] = max(p.Leader, -1)
			}
			leaders[topics[i].Topic] = m
			topics[i].LeaderLookup = "ok"
		}
	}
	for i := range parts {
		if l, ok := leaders[parts[i].Topic][parts[i].Partition]; ok {
			parts[i].Leader = &l
		}
	}
	return nil
}

// mcpLagByLeaders sums lag by leader, largest first, and apart from them the
// partitions whose leader is unknown.
func mcpLagByLeaders(call *mcpCall, parts []mcpLagPartition) ([]mcpLagByLeader, *mcpLagTotal) {
	idx := map[int]int{}
	out := []mcpLagByLeader{}
	var unknown mcpLagTotal
	for _, p := range parts {
		if p.Leader == nil {
			unknown.Partitions++
			unknown.Lag += p.Lag
			continue
		}
		i, ok := idx[*p.Leader]
		if !ok {
			i = len(out)
			idx[*p.Leader] = i
			out = append(out, mcpLagByLeader{Leader: *p.Leader})
		}
		out[i].Partitions++
		out[i].Lag += p.Lag
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lag != out[j].Lag {
			return out[i].Lag > out[j].Lag
		}
		return out[i].Leader < out[j].Leader
	})
	if unknown.Partitions == 0 {
		return mcpCap(call, out, mcpLagMaxLeaders), nil
	}
	return mcpCap(call, out, mcpLagMaxLeaders), &unknown
}

// mcpFitLag halves the longest list until the result fits one call. The
// totals and the unknown-leader count stay whole.
func mcpFitLag(call *mcpCall, out *mcpConsumerGroupLagOut) {
	for !call.Fits(out) {
		call.MarkTruncated()
		p, t, l, c := len(out.Partitions), len(out.Topics), len(out.ByLeader), len(out.CommittedTopics)
		switch {
		case p <= 1 && t <= 1 && l <= 1 && c <= 1:
			return // nothing left to cut; the guard refuses the result
		case p >= t && p >= l && p >= c:
			out.Partitions = out.Partitions[:p/2]
		case t >= l && t >= c:
			out.Topics = out.Topics[:t/2]
		case l >= c:
			out.ByLeader = out.ByLeader[:l/2]
		default:
			out.CommittedTopics = out.CommittedTopics[:c/2]
		}
	}
}
