package podrun

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
)

// DefaultHome is where Kafka lives in every image this package runs:
// quay.io/strimzi/kafka sets KAFKA_HOME=/opt/kafka, the official apache/kafka
// image installs to /opt/kafka, and Dockerfile.legacy-kafka unpacks the
// tarball to /opt/kafka on purpose so the legacy-kafka chart can point at
// either without knowing the difference.
const DefaultHome = "/opt/kafka"

// Tools builds argv slices for the Kafka command-line tools of one image.
// The Kafka line decides the spelling of each tool — the version is known
// (the user's --from, or the target's pin), so the pod never probes its own
// filesystem to find out which tool it has.
type Tools struct {
	// Home is the Kafka installation directory; DefaultHome when empty.
	Home string
	// Version is the Kafka line the image ships.
	Version kafkaversion.Version
}

// The releases where a tool changed its spelling. Each is a fact about the
// Kafka source tree, not a preference:
var (
	// KIP-377: kafka-topics.sh gained --bootstrap-server; before it only
	// --zookeeper worked.
	firstTopicsBootstrap = kafkaversion.Version{Major: 2, Minor: 2, Patch: 0}
	// KIP-499: kafka-console-producer.sh gained --bootstrap-server;
	// --broker-list was the only spelling before, and is gone in 4.0.
	firstProducerBootstrap = kafkaversion.Version{Major: 2, Minor: 5, Patch: 0}
	// KIP-635: bin/kafka-get-offsets.sh appeared, and GetOffsetShell gained
	// --bootstrap-server and --command-config. Before it the tool is reached
	// as kafka-run-class.sh kafka.tools.GetOffsetShell, takes --broker-list
	// and cannot be given a client configuration at all.
	firstGetOffsets = kafkaversion.Version{Major: 3, Minor: 0, Patch: 0}
)

// ToolsFor returns the Tools for the Kafka version v installed at home
// (DefaultHome when empty).
func ToolsFor(v kafkaversion.Version, home string) Tools {
	if home == "" {
		home = DefaultHome
	}
	return Tools{Home: strings.TrimRight(home, "/"), Version: v}
}

// Bin returns the path of a tool script under Home.
func (t Tools) Bin(name string) string {
	home := t.Home
	if home == "" {
		home = DefaultHome
	}
	return home + "/bin/" + name
}

// Topics is kafka-topics.sh against bootstrap, with --command-config when
// configPath is set, followed by extra. Kafka 2.1 (the floor a 4.x mirror can
// read) predates KIP-377 and its kafka-topics.sh only takes --zookeeper; see
// Caveats.
func (t Tools) Topics(bootstrap, configPath string, extra ...string) []string {
	argv := []string{t.Bin("kafka-topics.sh"), "--bootstrap-server", bootstrap}
	argv = withConfig(argv, "--command-config", configPath)
	return append(argv, extra...)
}

// ListTopics is `kafka-topics.sh --list`.
func (t Tools) ListTopics(bootstrap, configPath string) []string {
	return t.Topics(bootstrap, configPath, "--list")
}

// CreateTopic is `kafka-topics.sh --create --if-not-exists` for topic with
// the given partition count and replication factor.
func (t Tools) CreateTopic(bootstrap, configPath, topic string, partitions, rf int) []string {
	return t.Topics(bootstrap, configPath, "--create", "--if-not-exists", "--topic", topic,
		"--partitions", strconv.Itoa(partitions), "--replication-factor", strconv.Itoa(rf))
}

// ProducerArgs is kafka-console-producer.sh for topic, reading records from
// stdin (one per line). Below 2.5.0 the broker flag is --broker-list.
func (t Tools) ProducerArgs(bootstrap, configPath, topic string) []string {
	flag := "--bootstrap-server"
	if t.Version.Less(firstProducerBootstrap) {
		flag = "--broker-list"
	}
	argv := []string{t.Bin("kafka-console-producer.sh"), flag, bootstrap}
	argv = withConfig(argv, "--producer.config", configPath)
	return append(argv, "--topic", topic)
}

// ConsumerArgs is kafka-console-consumer.sh for topic. group is the consumer
// group to join (always name one: the default console-consumer-<n> group is
// one no ACL grant covers, and the resulting GroupAuthorizationException
// reads as "nothing replicated"); an empty group omits the flag. maxMessages
// > 0 stops after that many records; fromBeginning reads from the earliest
// offset when the group has no committed position; timeout > 0 makes the
// consumer exit after that long without a record, which is what keeps a
// short topic from holding the exec session open until the context expires.
func (t Tools) ConsumerArgs(bootstrap, configPath, topic, group string, maxMessages int, fromBeginning bool, timeout time.Duration) []string {
	argv := []string{t.Bin("kafka-console-consumer.sh"), "--bootstrap-server", bootstrap}
	argv = withConfig(argv, "--consumer.config", configPath)
	argv = append(argv, "--topic", topic)
	if group != "" {
		argv = append(argv, "--group", group)
	}
	if fromBeginning {
		argv = append(argv, "--from-beginning")
	}
	if maxMessages > 0 {
		argv = append(argv, "--max-messages", strconv.Itoa(maxMessages))
	}
	if timeout > 0 {
		argv = append(argv, "--timeout-ms", strconv.FormatInt(int64(timeout/time.Millisecond), 10))
	}
	return argv
}

// ConsumerGroups is kafka-consumer-groups.sh against bootstrap, with
// --command-config when configPath is set, followed by extra.
func (t Tools) ConsumerGroups(bootstrap, configPath string, extra ...string) []string {
	argv := []string{t.Bin("kafka-consumer-groups.sh"), "--bootstrap-server", bootstrap}
	argv = withConfig(argv, "--command-config", configPath)
	return append(argv, extra...)
}

// ListGroups is `kafka-consumer-groups.sh --list`.
func (t Tools) ListGroups(bootstrap, configPath string) []string {
	return t.ConsumerGroups(bootstrap, configPath, "--list")
}

// DescribeGroup is `kafka-consumer-groups.sh --describe --group <group>`;
// parse its output with ParseGroupDescribe.
func (t Tools) DescribeGroup(bootstrap, configPath, group string) []string {
	return t.ConsumerGroups(bootstrap, configPath, "--describe", "--group", group)
}

// BrokerAPIVersions is kafka-broker-api-versions.sh, the ApiVersions
// handshake the chart's preflight uses to prove a broker answers.
func (t Tools) BrokerAPIVersions(bootstrap, configPath string) []string {
	argv := []string{t.Bin("kafka-broker-api-versions.sh"), "--bootstrap-server", bootstrap}
	return withConfig(argv, "--command-config", configPath)
}

// EndOffsets lists the end offset of every partition of topic, one
// `topic:partition:offset` line each (ParseTopicOffsets reads them). From
// 3.0.0 it is kafka-get-offsets.sh with --bootstrap-server, --time -1 and
// --command-config when configPath is set. Below 3.0.0 it is
// kafka-run-class.sh kafka.tools.GetOffsetShell with --broker-list — that
// tool has no --command-config, so configPath is ignored there and the
// bootstrap must be a listener the pod can use unauthenticated (the
// legacy-kafka chart always keeps its plaintext one). OffsetsTakeConfig
// says which case applies.
func (t Tools) EndOffsets(bootstrap, configPath, topic string) []string {
	if t.Version.Less(firstGetOffsets) {
		return []string{t.Bin("kafka-run-class.sh"), "kafka.tools.GetOffsetShell",
			"--broker-list", bootstrap, "--topic", topic, "--time", "-1"}
	}
	argv := []string{t.Bin("kafka-get-offsets.sh"), "--bootstrap-server", bootstrap, "--topic", topic, "--time", "-1"}
	return withConfig(argv, "--command-config", configPath)
}

// OffsetsTakeConfig reports whether EndOffsets can carry a client
// configuration (Kafka 3.0.0 and later).
func (t Tools) OffsetsTakeConfig() bool {
	return !t.Version.Less(firstGetOffsets)
}

// Caveats lists, in plain words, what the tools of this Kafka line cannot do
// that newer ones can — for a plan or a status line, so a limitation is
// stated before it is hit.
func (t Tools) Caveats() []string {
	var out []string
	if t.Version.Less(firstTopicsBootstrap) {
		out = append(out, fmt.Sprintf("kafka-topics.sh on Kafka %s takes only --zookeeper (KIP-377 added --bootstrap-server in %s); topics must be created by the broker's own tooling", t.Version, firstTopicsBootstrap))
	}
	if t.Version.Less(firstGetOffsets) {
		out = append(out, fmt.Sprintf("GetOffsetShell on Kafka %s takes no client configuration (KIP-635 added --command-config in %s); end offsets are read over the plaintext listener", t.Version, firstGetOffsets))
	}
	return out
}

func withConfig(argv []string, flag, configPath string) []string {
	if configPath == "" {
		return argv
	}
	return append(argv, flag, configPath)
}

// ErrNoOffsets is returned when an offsets listing contains no
// topic:partition:offset row at all. It is deliberately not a sum of zero:
// an empty reading means the tool failed (wrong flag, denied, no such
// topic), which the caller must be able to tell apart from an empty topic.
var ErrNoOffsets = errors.New("no offset rows in the output")

// offsetLine is one row of GetOffsetShell's output. Topic names are limited
// to [a-zA-Z0-9._-], so the first colon is unambiguous.
var offsetLine = regexp.MustCompile(`^([A-Za-z0-9._-]+):(\d+):(-?\d*)$`)

// ParseOffsets reads GetOffsetShell output (either generation) and returns
// the end offset per partition and their sum. Lines that are not
// `topic:partition:offset` rows — log4j noise, SLF4J warnings — are ignored.
// It is an error when no row is present (ErrNoOffsets), when a row has an
// empty offset (the tool found no offset for that partition), when a
// partition repeats, or when rows for more than one topic are present; use
// ParseTopicOffsets to select one topic from a listing that may carry
// several (the 3.0+ tool treats --topic as a regular expression).
func ParseOffsets(output string) (map[int]int64, int64, error) {
	return parseOffsets(output, "")
}

// ParseTopicOffsets is ParseOffsets restricted to the rows of exactly topic.
func ParseTopicOffsets(output, topic string) (map[int]int64, int64, error) {
	if topic == "" {
		return nil, 0, errors.New("topic is required")
	}
	return parseOffsets(output, topic)
}

func parseOffsets(output, topic string) (map[int]int64, int64, error) {
	type row struct {
		topic     string
		partition int
		offset    string
	}
	var rows []row
	topics := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		m := offsetLine.FindStringSubmatch(line)
		if m == nil || (topic != "" && m[1] != topic) {
			continue
		}
		partition, err := strconv.Atoi(m[2])
		if err != nil {
			return nil, 0, fmt.Errorf("partition %q: %w", m[2], err)
		}
		topics[m[1]] = true
		rows = append(rows, row{topic: m[1], partition: partition, offset: m[3]})
	}
	if len(rows) == 0 {
		if topic != "" {
			return nil, 0, fmt.Errorf("topic %s: %w", topic, ErrNoOffsets)
		}
		return nil, 0, ErrNoOffsets
	}
	if len(topics) > 1 {
		names := make([]string, 0, len(topics))
		for n := range topics {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, 0, fmt.Errorf("offsets for more than one topic (%s); use ParseTopicOffsets", strings.Join(names, ", "))
	}
	per := map[int]int64{}
	var sum int64
	for _, r := range rows {
		if r.offset == "" {
			return nil, 0, fmt.Errorf("partition %d of %s has no offset", r.partition, r.topic)
		}
		offset, err := strconv.ParseInt(r.offset, 10, 64)
		if err != nil {
			return nil, 0, fmt.Errorf("offset of partition %d: %w", r.partition, err)
		}
		if offset < 0 {
			return nil, 0, fmt.Errorf("partition %d of %s reports offset %d", r.partition, r.topic, offset)
		}
		if _, dup := per[r.partition]; dup {
			return nil, 0, fmt.Errorf("partition %d of %s is listed twice", r.partition, r.topic)
		}
		per[r.partition] = offset
		sum += offset
	}
	return per, sum, nil
}

// GroupOffset is one row of `kafka-consumer-groups.sh --describe`: the
// committed position of a group on one partition. Lag, CurrentOffset and
// LogEndOffset are -1 when the tool printed "-" (no committed offset yet).
type GroupOffset struct {
	Group         string
	Topic         string
	Partition     int
	CurrentOffset int64
	LogEndOffset  int64
	Lag           int64
}

// ParseGroupDescribe reads the table `kafka-consumer-groups.sh --describe`
// prints and returns one GroupOffset per row. Two layouts exist: from Kafka
// 2.3 the columns are GROUP TOPIC PARTITION CURRENT-OFFSET LOG-END-OFFSET
// LAG …; 2.1 and 2.2 print the same table without the GROUP column, in
// which case Group is empty (the caller named the group on the command
// line). The verbose-only LEADER-EPOCH column of 4.x never appears because
// DescribeGroup does not pass --verbose. Header lines, blank lines, log
// noise and the tool's notes ("Consumer group 'x' has no active members.")
// are skipped. An output with no rows yields an empty slice and no error: a
// group that has committed nothing is a valid answer, and callers decide
// what it means.
func ParseGroupDescribe(output string) []GroupOffset {
	var rows []GroupOffset
	// The layout of the table. A header line settles it; without one (a
	// caller that kept only the rows) the shape of each row tells:
	// PARTITION is the second column in the old layout, the third in the
	// new one.
	withGroup, headerSeen := true, false
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimRight(line, "\r"))
		if len(fields) < 5 {
			continue
		}
		switch fields[0] {
		case "GROUP":
			withGroup, headerSeen = fields[1] == "TOPIC", true
			continue
		case "TOPIC":
			withGroup, headerSeen = false, true
			continue
		}
		layoutWithGroup := withGroup
		if !headerSeen {
			if _, err := strconv.Atoi(fields[1]); err == nil {
				layoutWithGroup = false
			} else if _, err := strconv.Atoi(fields[2]); err == nil && len(fields) >= 6 {
				layoutWithGroup = true
			}
		}
		group, cells := "", fields
		if layoutWithGroup {
			if len(fields) < 6 {
				continue
			}
			group, cells = fields[0], fields[1:]
		}
		partition, err := strconv.Atoi(cells[1])
		if err != nil {
			continue
		}
		current, ok1 := parseOffsetCell(cells[2])
		end, ok2 := parseOffsetCell(cells[3])
		lag, ok3 := parseOffsetCell(cells[4])
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		rows = append(rows, GroupOffset{
			Group: group, Topic: cells[0], Partition: partition,
			CurrentOffset: current, LogEndOffset: end, Lag: lag,
		})
	}
	return rows
}

// parseOffsetCell reads a numeric cell of the describe table; "-" means no
// value and is returned as -1.
func parseOffsetCell(s string) (int64, bool) {
	if s == "-" {
		return -1, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// GroupOffsetsFor returns the rows of rows that belong to topic.
func GroupOffsetsFor(rows []GroupOffset, topic string) []GroupOffset {
	var out []GroupOffset
	for _, r := range rows {
		if r.Topic == topic {
			out = append(out, r)
		}
	}
	return out
}
