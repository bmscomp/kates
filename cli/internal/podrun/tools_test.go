package podrun

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
)

func v(s string) kafkaversion.Version { return kafkaversion.MustParse(s) }

func TestToolsForAndBin(t *testing.T) {
	tl := ToolsFor(v("4.3.0"), "")
	if tl.Home != DefaultHome {
		t.Errorf("Home = %q, want %q", tl.Home, DefaultHome)
	}
	if got := tl.Bin("kafka-topics.sh"); got != "/opt/kafka/bin/kafka-topics.sh" {
		t.Errorf("Bin = %q", got)
	}
	if got := ToolsFor(v("2.8.2"), "/usr/local/kafka/").Bin("kafka-run-class.sh"); got != "/usr/local/kafka/bin/kafka-run-class.sh" {
		t.Errorf("Bin with custom home = %q", got)
	}
	if got := (Tools{}).Bin("x.sh"); got != "/opt/kafka/bin/x.sh" {
		t.Errorf("zero Tools Bin = %q", got)
	}
}

func TestToolsArgv(t *testing.T) {
	const bs, cfg = "b:9092", "/tmp/client.properties"
	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{"topics list with config", ToolsFor(v("4.3.0"), "").ListTopics(bs, cfg),
			[]string{"/opt/kafka/bin/kafka-topics.sh", "--bootstrap-server", bs, "--command-config", cfg, "--list"}},
		{"topics list plaintext", ToolsFor(v("2.8.2"), "").ListTopics(bs, ""),
			[]string{"/opt/kafka/bin/kafka-topics.sh", "--bootstrap-server", bs, "--list"}},
		{"create topic", ToolsFor(v("3.9.1"), "").CreateTopic(bs, "", "kates.orders", 3, 1),
			[]string{"/opt/kafka/bin/kafka-topics.sh", "--bootstrap-server", bs, "--create", "--if-not-exists",
				"--topic", "kates.orders", "--partitions", "3", "--replication-factor", "1"}},
		{"producer 4.x", ToolsFor(v("4.3.0"), "").ProducerArgs(bs, cfg, "kates.orders"),
			[]string{"/opt/kafka/bin/kafka-console-producer.sh", "--bootstrap-server", bs, "--producer.config", cfg, "--topic", "kates.orders"}},
		{"producer 2.8 (KIP-499 landed in 2.5)", ToolsFor(v("2.8.2"), "").ProducerArgs(bs, "", "kates.orders"),
			[]string{"/opt/kafka/bin/kafka-console-producer.sh", "--bootstrap-server", bs, "--topic", "kates.orders"}},
		{"producer 2.4 uses --broker-list", ToolsFor(v("2.4.1"), "").ProducerArgs(bs, "", "kates.orders"),
			[]string{"/opt/kafka/bin/kafka-console-producer.sh", "--broker-list", bs, "--topic", "kates.orders"}},
		{"consumer full", ToolsFor(v("4.3.0"), "").ConsumerArgs(bs, cfg, "kates.orders", "kates-migration-verify-m282-430", 400, true, 2*time.Minute),
			[]string{"/opt/kafka/bin/kafka-console-consumer.sh", "--bootstrap-server", bs, "--consumer.config", cfg,
				"--topic", "kates.orders", "--group", "kates-migration-verify-m282-430", "--from-beginning", "--max-messages", "400", "--timeout-ms", "120000"}},
		{"consumer minimal", ToolsFor(v("2.8.2"), "").ConsumerArgs(bs, "", "kates.orders", "", 0, false, 0),
			[]string{"/opt/kafka/bin/kafka-console-consumer.sh", "--bootstrap-server", bs, "--topic", "kates.orders"}},
		{"groups list", ToolsFor(v("4.3.0"), "").ListGroups(bs, cfg),
			[]string{"/opt/kafka/bin/kafka-consumer-groups.sh", "--bootstrap-server", bs, "--command-config", cfg, "--list"}},
		{"group describe", ToolsFor(v("2.8.2"), "").DescribeGroup(bs, "", "kates-migration-m282-430"),
			[]string{"/opt/kafka/bin/kafka-consumer-groups.sh", "--bootstrap-server", bs, "--describe", "--group", "kates-migration-m282-430"}},
		{"api versions", ToolsFor(v("4.3.0"), "").BrokerAPIVersions(bs, cfg),
			[]string{"/opt/kafka/bin/kafka-broker-api-versions.sh", "--bootstrap-server", bs, "--command-config", cfg}},
		{"end offsets 4.x", ToolsFor(v("4.3.0"), "").EndOffsets(bs, cfg, "kates.orders"),
			[]string{"/opt/kafka/bin/kafka-get-offsets.sh", "--bootstrap-server", bs, "--topic", "kates.orders", "--time", "-1", "--command-config", cfg}},
		{"end offsets 3.0 (KIP-635)", ToolsFor(v("3.0.0"), "").EndOffsets(bs, "", "kates.orders"),
			[]string{"/opt/kafka/bin/kafka-get-offsets.sh", "--bootstrap-server", bs, "--topic", "kates.orders", "--time", "-1"}},
		{"end offsets 2.8: old class, --broker-list, config ignored", ToolsFor(v("2.8.2"), "").EndOffsets(bs, cfg, "kates.orders"),
			[]string{"/opt/kafka/bin/kafka-run-class.sh", "kafka.tools.GetOffsetShell", "--broker-list", bs, "--topic", "kates.orders", "--time", "-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !reflect.DeepEqual(tt.got, tt.want) {
				t.Errorf("argv = %q\nwant  %q", tt.got, tt.want)
			}
		})
	}
}

func TestToolsEraFlags(t *testing.T) {
	if ToolsFor(v("2.8.2"), "").OffsetsTakeConfig() {
		t.Error("2.8 GetOffsetShell has no --command-config")
	}
	if !ToolsFor(v("3.0.0"), "").OffsetsTakeConfig() {
		t.Error("3.0 kafka-get-offsets.sh takes --command-config")
	}
	tests := map[string]int{"2.1.0": 2, "2.1.1": 2, "2.2.0": 1, "2.8.2": 1, "3.0.0": 0, "3.9.1": 0, "4.3.0": 0}
	for ver, n := range tests {
		if got := ToolsFor(v(ver), "").Caveats(); len(got) != n {
			t.Errorf("Caveats(%s) = %q, want %d entries", ver, got, n)
		}
	}
	if c := ToolsFor(v("2.1.0"), "").Caveats(); !strings.Contains(c[0], "--zookeeper") || !strings.Contains(c[1], "--command-config") {
		t.Errorf("2.1.0 caveats: %q", c)
	}
}

// The 4.x kafka-get-offsets.sh output, with the log4j noise a client prints
// before the rows when LOG_DIR is unwritable or a config key is unknown.
const offsets43 = `[2026-09-08 10:12:01,114] WARN These configurations '[ssl.endpoint.identification.algorithm]' were supplied but are not used yet. (org.apache.kafka.clients.admin.AdminClientConfig)
kates.orders:0:67
kates.orders:1:66
kates.orders:2:67
`

// The 2.8 kafka.tools.GetOffsetShell output, CRLF line endings included.
const offsets28 = "SLF4J: Class path contains multiple SLF4J bindings.\r\nkates.orders:0:70\r\nkates.orders:1:65\r\nkates.orders:2:65\r\n"

func TestParseOffsets(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		topic   string
		wantPer map[int]int64
		wantSum int64
		wantErr error
		errText string
	}{
		{name: "4.x rows with noise", in: offsets43, wantPer: map[int]int64{0: 67, 1: 66, 2: 67}, wantSum: 200},
		{name: "2.8 rows with CRLF", in: offsets28, wantPer: map[int]int64{0: 70, 1: 65, 2: 65}, wantSum: 200},
		{name: "single partition", in: "kates.audit:0:0\n", wantPer: map[int]int64{0: 0}, wantSum: 0},
		{name: "empty output is not zero", in: "", wantErr: ErrNoOffsets},
		{name: "only noise is not zero", in: "Exception in thread \"main\" joptsimple.UnrecognizedOptionException: bootstrap-server is not a recognized option\n", wantErr: ErrNoOffsets},
		{name: "missing offset", in: "kates.orders:0:67\nkates.orders:1:\n", errText: "partition 1 of kates.orders has no offset"},
		{name: "duplicate partition", in: "kates.orders:0:1\nkates.orders:0:2\n", errText: "listed twice"},
		{name: "two topics", in: "kates.orders:0:1\nkates.payments:0:2\n", errText: "more than one topic (kates.orders, kates.payments)"},
		{name: "topic filter", in: "kates.orders:0:1\nkates.payments:0:2\nkates.orders:1:3\n", topic: "kates.orders", wantPer: map[int]int64{0: 1, 1: 3}, wantSum: 4},
		{name: "topic filter is exact", in: "kates.orders:0:1\nkatesXorders:0:2\n", topic: "kates.orders", wantPer: map[int]int64{0: 1}, wantSum: 1},
		{name: "topic filter with no rows", in: "kates.payments:0:2\n", topic: "kates.orders", wantErr: ErrNoOffsets},
		{name: "negative offset", in: "kates.orders:0:-1\n", errText: "reports offset -1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				per map[int]int64
				sum int64
				err error
			)
			if tt.topic == "" {
				per, sum, err = ParseOffsets(tt.in)
			} else {
				per, sum, err = ParseTopicOffsets(tt.in, tt.topic)
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if tt.errText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errText) {
					t.Fatalf("err = %v, want %q", err, tt.errText)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(per, tt.wantPer) || sum != tt.wantSum {
				t.Errorf("got %v sum %d, want %v sum %d", per, sum, tt.wantPer, tt.wantSum)
			}
		})
	}
	if _, _, err := ParseTopicOffsets(offsets43, ""); err == nil {
		t.Error("ParseTopicOffsets without a topic must fail")
	}
}

// kafka-consumer-groups.sh --describe on a 4.x target, after MirrorMaker 2
// translated the group: no active members, so the trailing columns are "-".
const describe43 = `
Consumer group 'kates-migration-m282-430' has no active members.

GROUP                     TOPIC           PARTITION  CURRENT-OFFSET  LOG-END-OFFSET  LAG             CONSUMER-ID     HOST            CLIENT-ID
kates-migration-m282-430  kates.orders    0          34              67              33              -               -               -
kates-migration-m282-430  kates.orders    1          33              66              33              -               -               -
kates-migration-m282-430  kates.orders    2          33              67              34              -               -               -
`

// The same table as Kafka 2.1/2.2 print it, without the GROUP column, and
// with an active member on one partition.
const describe21 = `
TOPIC           PARTITION  CURRENT-OFFSET  LOG-END-OFFSET  LAG             CONSUMER-ID                                     HOST            CLIENT-ID
kates.orders    0          34              67              33              consumer-1-6d0c9f2e-1c3e-4c7c-9a1b-2f2d5a7e9b3c /10.244.0.9     consumer-1
kates.orders    1          -               66              -               -                                               -               -
`

func TestParseGroupDescribe(t *testing.T) {
	rows := ParseGroupDescribe(describe43)
	want := []GroupOffset{
		{Group: "kates-migration-m282-430", Topic: "kates.orders", Partition: 0, CurrentOffset: 34, LogEndOffset: 67, Lag: 33},
		{Group: "kates-migration-m282-430", Topic: "kates.orders", Partition: 1, CurrentOffset: 33, LogEndOffset: 66, Lag: 33},
		{Group: "kates-migration-m282-430", Topic: "kates.orders", Partition: 2, CurrentOffset: 33, LogEndOffset: 67, Lag: 34},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("4.x rows = %+v\nwant %+v", rows, want)
	}

	rows = ParseGroupDescribe(describe21)
	want = []GroupOffset{
		{Topic: "kates.orders", Partition: 0, CurrentOffset: 34, LogEndOffset: 67, Lag: 33},
		{Topic: "kates.orders", Partition: 1, CurrentOffset: -1, LogEndOffset: 66, Lag: -1},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("2.1 rows = %+v\nwant %+v", rows, want)
	}

	// Rows without their header still parse, by shape.
	headless := "g1 kates.orders 0 1 2 1 - - -\nkates.orders 1 3 4 1 - - -\n"
	rows = ParseGroupDescribe(headless)
	want = []GroupOffset{
		{Group: "g1", Topic: "kates.orders", Partition: 0, CurrentOffset: 1, LogEndOffset: 2, Lag: 1},
		{Topic: "kates.orders", Partition: 1, CurrentOffset: 3, LogEndOffset: 4, Lag: 1},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("headless rows = %+v\nwant %+v", rows, want)
	}

	if rows := ParseGroupDescribe("Error: Consumer group 'nope' does not exist.\n"); len(rows) != 0 {
		t.Errorf("error output yielded rows: %+v", rows)
	}
	if rows := ParseGroupDescribe(""); len(rows) != 0 {
		t.Errorf("empty output yielded rows: %+v", rows)
	}
	if got := GroupOffsetsFor(ParseGroupDescribe(describe43), "kates.payments"); len(got) != 0 {
		t.Errorf("GroupOffsetsFor another topic = %+v", got)
	}
	if got := GroupOffsetsFor(ParseGroupDescribe(describe43), "kates.orders"); len(got) != 3 {
		t.Errorf("GroupOffsetsFor = %d rows, want 3", len(got))
	}
}
