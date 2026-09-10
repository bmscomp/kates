package migrate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
)

const repoRoot = "../../.."

// values decodes a values document into nested maps for key assertions.
func values(t *testing.T, doc string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("values are not valid YAML: %v\n%s", err, doc)
	}
	return m
}

// path builds a key path: the first argument is split on "/", the rest are
// taken verbatim (for keys that contain dots or slashes themselves).
func path(first string, rest ...string) []string {
	return append(strings.Split(first, "/"), rest...)
}

// at walks a key path (map keys or list indexes) through decoded values.
func at(t *testing.T, m any, keys []string) any {
	t.Helper()
	cur := m
	for _, k := range keys {
		switch x := cur.(type) {
		case map[string]any:
			var ok bool
			if cur, ok = x[k]; !ok {
				t.Fatalf("%v: key %q missing in %v", keys, k, x)
			}
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i >= len(x) {
				t.Fatalf("%v: bad index %q for a list of %d", keys, k, len(x))
			}
			cur = x[i]
		default:
			t.Fatalf("%v: cannot descend into %T at %q", keys, cur, k)
		}
	}
	return cur
}

type check struct {
	path []string
	want any
}

func assertValues(t *testing.T, m map[string]any, checks []check) {
	t.Helper()
	for _, c := range checks {
		if got := at(t, m, c.path); got != c.want {
			t.Errorf("%v = %v (%T), want %v (%T)", c.path, got, got, c.want, c.want)
		}
	}
}

// mergeValues layers overlay on base the way Helm merges values files: maps
// deeply, everything else (lists included) replaced.
func mergeValues(base, overlay map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		if bm, ok := out[k].(map[string]any); ok {
			if om, ok := v.(map[string]any); ok {
				out[k] = mergeValues(bm, om)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// validateAgainstChart validates the generated values the way Helm would:
// merged onto the chart's own values.yaml (and the era preset for the
// mirror), then checked against the chart's values.schema.json. The overlay
// alone is also held to the schema's key set: a stray key would be silently
// ignored by the chart.
func validateAgainstChart(t *testing.T, chartPath string, presets []string, doc string) {
	t.Helper()
	schema := loadSchema(t, filepath.Join(repoRoot, chartPath, "values.schema.json"))
	layers := append([]string{"values.yaml"}, presets...)
	merged := map[string]any{}
	for _, f := range layers {
		data, err := os.ReadFile(filepath.Join(repoRoot, chartPath, f))
		if err != nil {
			t.Skipf("chart values not available: %v", err)
		}
		merged = mergeValues(merged, values(t, string(data)))
	}
	merged = mergeValues(merged, values(t, doc))
	out, err := yaml.Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	if errs := schema.validateYAML(t, string(out)); len(errs) > 0 {
		t.Errorf("%s schema violations:\n  %s\n--- generated values:\n%s", chartPath, strings.Join(errs, "\n  "), doc)
	}
	for _, e := range schema.validateYAML(t, doc) {
		if strings.Contains(e, "is not in the schema") {
			t.Errorf("%s: %s", chartPath, e)
		}
	}
}

func TestEraOverlayAndPaths(t *testing.T) {
	tests := map[string]string{
		"2.1.0": EraOverlay2x, "2.8.2": EraOverlay2x, "3.0.0": EraOverlay3x, "3.9.1": EraOverlay3x,
		"4.0.0": EraOverlay4x, "4.1.2": EraOverlay4x, "4.2.1": EraOverlay4x,
	}
	for ver, want := range tests {
		if got := EraOverlay(v(ver)); got != want {
			t.Errorf("EraOverlay(%s) = %q, want %q", ver, got, want)
		}
	}
	if got := EraOverlayPath(v("2.8.2")); got != "charts/mirror-maker2/values-migrate-2x.yaml" {
		t.Errorf("EraOverlayPath = %q", got)
	}
	if got := CutoverValues(); got != "charts/mirror-maker2/values-cutover.yaml" {
		t.Errorf("CutoverValues = %q", got)
	}
	// The presets the CLI layers on exist in the chart (the 4x one is being
	// added by the chart work and is not asserted).
	for _, f := range []string{EraOverlay2x, EraOverlay3x, CutoverFile} {
		if _, err := os.Stat(filepath.Join(repoRoot, MirrorChartPath, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func TestTopicsPatternAndJavaRegexQuote(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{[]string{"kates.orders"}, `kates\.orders`},
		{[]string{"kates.orders", "kates.payments"}, `kates\.orders|kates\.payments`},
		{[]string{"orders-v2_a"}, `orders\-v2_a`},
		{[]string{"a.b|c$"}, `a\.b\|c\$`},
	}
	for _, tt := range tests {
		if got := TopicsPattern(tt.in); got != tt.want {
			t.Errorf("TopicsPattern(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := JavaRegexQuote(`\`); got != `\\` {
		t.Errorf("JavaRegexQuote(backslash) = %q", got)
	}
}

func TestSourceValues(t *testing.T) {
	l := legacyLab(t)
	doc, err := SourceValues(l)
	if err != nil {
		t.Fatal(err)
	}
	validateAgainstChart(t, SourceChartPath, nil, doc)
	m := values(t, doc)
	assertValues(t, m, []check{
		{path("extraLabels", LabelLab), "m282-430"},
		{path("extraLabels", LabelRole), "source"},
		{path("mode"), "zookeeper"},
		{path("kafka/version"), "2.8.2"},
		{path("kafka/image"), "ghcr.io/bmscomp/kates-legacy-kafka:2.8.2"},
		{path("kafka/replicas"), 1},
		{path("kafka/replicationFactor"), 1},
		{path("kafka/heapOpts"), "-Xms256m -Xmx384m"},
		{path("kafka/config", "inter.broker.protocol.version"), "2.8"},
		{path("kafka/config", "log.message.format.version"), "2.8"},
		{path("kafka/config", "log.retention.hours"), 168},
		{path("kafka/resources/requests/memory"), "640Mi"},
		{path("kafka/resources/limits/cpu"), "1000m"},
		{path("zookeeper/replicas"), 1},
		{path("zookeeper/heapOpts"), "-Xms128m -Xmx192m"},
		{path("zookeeper/resources/limits/memory"), "512Mi"},
		{path("persistence/enabled"), false},
		{path("networkPolicy/enabled"), true},
		{path("topics/0/name"), "kates.orders"},
		{path("topics/0/partitions"), 3},
		{path("topics/0/replicationFactor"), 1},
	})
	if _, has := m["listeners"]; has {
		t.Error("a plaintext lab must not set listeners")
	}
	if list := at(t, m, path("topics")).([]any); len(list) != 1 {
		t.Errorf("topics has %d entries; the chart's defaults must be replaced, not appended to", len(list))
	}

	// A 3.x KRaft source with SASL: no zookeeper block, no 2.x protocol
	// keys, the SASL listener with the lab's password.
	sasl, err := New(Options{From: v("3.9.1"), To: v("4.3.0"), TargetIsPrimary: true, SASL: true, SourcePassword: "pw-1234",
		Topics: []string{"kates.orders", "kates.payments"}})
	if err != nil {
		t.Fatal(err)
	}
	doc, err = SourceValues(sasl)
	if err != nil {
		t.Fatal(err)
	}
	validateAgainstChart(t, SourceChartPath, nil, doc)
	m = values(t, doc)
	assertValues(t, m, []check{
		{path("mode"), "kraft"},
		{path("kafka/image"), "apache/kafka:3.9.1"},
		{path("listeners/plaintext/enabled"), true},
		{path("listeners/plaintext/port"), 9092},
		{path("listeners/sasl/enabled"), true},
		{path("listeners/sasl/port"), 9094},
		{path("listeners/sasl/users/0/username"), "mm2reader"},
		{path("listeners/sasl/users/0/password"), "pw-1234"},
		{path("topics/1/name"), "kates.payments"},
		{path("kafka/config", "log.retention.hours"), 168},
	})
	if _, has := m["zookeeper"]; has {
		t.Error("a KRaft source must not set zookeeper")
	}
	if cfg := at(t, m, path("kafka/config")).(map[string]any); cfg["inter.broker.protocol.version"] != nil {
		t.Error("a 3.x source must not pin inter.broker.protocol.version")
	}

	strimzi, err := New(Options{From: v("4.1.2"), To: v("4.3.0"), TargetIsPrimary: true, SourceProvider: kafkaversion.ProviderStrimzi})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SourceValues(strimzi); err == nil || !strings.Contains(err.Error(), "legacy-kafka") {
		t.Errorf("SourceValues(strimzi) = %v, want a refusal", err)
	}
	if _, err := SourceValues(nil); err == nil {
		t.Error("SourceValues(nil) must fail")
	}
}

func TestMirrorValuesLegacyPlaintext(t *testing.T) {
	l := legacyLab(t)
	doc, err := MirrorValues(l, MirrorInputs{TargetBrokerCount: 1, OperatorNamespace: "strimzi-operator"})
	if err != nil {
		t.Fatal(err)
	}
	validateAgainstChart(t, MirrorChartPath, []string{EraOverlay(l.From)}, doc)
	m := values(t, doc)
	assertValues(t, m, []check{
		{path("extraLabels", LabelLab), "m282-430"},
		{path("extraLabels", LabelRole), "mirror"},
		{path("version"), "4.3.0"},
		{path("strimziVersion"), "1.1.0"},
		{path("replicationPolicy/mode"), "identity"},
		{path("replicationPolicy/excludeInternalTopics"), true},
		{path("compatibility/enabled"), true},
		{path("compatibility/enforce"), true},
		{path("compatibility/minSourceVersion"), "2.1.0"},
		{path("compatibility/requireDeclaredVersion"), true},
		{path("preflight/enabled"), true},
		{path("preflight/failOnError"), true},
		{path("target/alias"), "target"},
		{path("target/clusterName"), "krafter"},
		{path("target/namespace"), "kafka"},
		{path("target/groupId"), "mm2-m282-430"},
		{path("target/brokerCount"), 1},
		{path("target/config", "config.storage.replication.factor"), 1},
		{path("target/config", "offset.storage.replication.factor"), 1},
		{path("target/config", "status.storage.replication.factor"), 1},
		{path("mirrors/0/source/alias"), "source"},
		{path("mirrors/0/source/bootstrapServers"), "m282-430-src-legacy-kafka-bootstrap.kafka-m282-430-src.svc.cluster.local:9092"},
		{path("mirrors/0/source/kafkaVersion"), "2.8.2"},
		{path("mirrors/0/source/authentication/type"), ""},
		{path("mirrors/0/topics/0"), "kates.orders"},
		{path("mirrors/0/groupsPattern"), ".*"},
		{path("mirrors/0/sourceConnector/tasksMax"), 2},
		{path("mirrors/0/sourceConnector/state"), "running"},
		{path("mirrors/0/sourceConnector/config", "replication.factor"), 1},
		{path("mirrors/0/sourceConnector/config", "offset-syncs.topic.replication.factor"), 1},
		{path("mirrors/0/sourceConnector/config", "sync.topic.acls.enabled"), "false"},
		{path("mirrors/0/sourceConnector/config", "sync.topic.configs.enabled"), "true"},
		{path("mirrors/0/sourceConnector/config", "refresh.topics.interval.seconds"), 20},
		{path("mirrors/0/checkpointConnector/tasksMax"), 1},
		{path("mirrors/0/checkpointConnector/state"), "running"},
		{path("mirrors/0/checkpointConnector/config", "checkpoints.topic.replication.factor"), 1},
		{path("mirrors/0/checkpointConnector/config", "sync.group.offsets.enabled"), "true"},
		{path("mirrors/0/checkpointConnector/config", "sync.group.offsets.interval.seconds"), 10},
		{path("mirrors/0/checkpointConnector/config", "emit.checkpoints.interval.seconds"), 10},
		{path("mirrors/0/checkpointConnector/config", "refresh.groups.interval.seconds"), 20},
		{path("networkPolicy/strimziOperatorNamespace"), "strimzi-operator"},
	})
	src := at(t, m, path("mirrors/0/source")).(map[string]any)
	if _, has := src["tls"]; has {
		t.Error("a plaintext source must not set tls")
	}
	for _, absent := range []string{"secretSync", "tests"} {
		if _, has := m[absent]; has {
			t.Errorf("%s must not be set unless asked for", absent)
		}
	}
	if list := at(t, m, path("mirrors")).([]any); len(list) != 1 {
		t.Errorf("mirrors has %d entries, want 1 (Helm replaces lists wholesale)", len(list))
	}
}

func TestMirrorValuesSASLTestsAndSizing(t *testing.T) {
	l, err := New(Options{From: v("3.9.1"), To: v("4.3.0"), TargetIsPrimary: true, SASL: true, SourcePassword: "x",
		Policy: PolicyDefault, Topics: []string{"kates.orders", "kates.payments"}, Messages: 50})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := MirrorValues(l, MirrorInputs{TargetBrokerCount: 3, HelmTests: true, KafkaVersion: "4.2.1", StrimziVersion: "1.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	validateAgainstChart(t, MirrorChartPath, []string{EraOverlay(l.From)}, doc)
	m := values(t, doc)
	assertValues(t, m, []check{
		{path("version"), "4.2.1"},
		{path("strimziVersion"), "1.0.1"},
		{path("replicationPolicy/mode"), "default"},
		{path("target/brokerCount"), 3},
		{path("target/config", "config.storage.replication.factor"), 3},
		{path("mirrors/0/source/bootstrapServers"), "m391-430-src-legacy-kafka-bootstrap.kafka-m391-430-src.svc.cluster.local:9094"},
		{path("mirrors/0/source/authentication/type"), "plain"},
		{path("mirrors/0/source/authentication/username"), "mm2reader"},
		{path("mirrors/0/source/authentication/secretName"), "m391-430-src-mm2reader"},
		{path("mirrors/0/source/authentication/secretKey"), "password"},
		{path("mirrors/0/topics/0"), "kates.orders"},
		{path("mirrors/0/topics/1"), "kates.payments"},
		{path("mirrors/0/topics/2"), "kates.mm2-e2e"},
		{path("mirrors/0/sourceConnector/config", "replication.factor"), 3},
		{path("mirrors/0/checkpointConnector/config", "checkpoints.topic.replication.factor"), 3},
		{path("tests/replication/enabled"), true},
		{path("tests/replication/topic"), "kates.mm2-e2e"},
		{path("tests/replication/partitions"), 1},
		{path("tests/replication/replicationFactor"), 3},
		{path("tests/replication/messages"), 50},
		{path("tests/replication/sourceAlias"), "source"},
		{path("tests/replication/group"), "kates-mm2-test"},
		{path("tests/offsetTranslation/enabled"), true},
		{path("tests/offsetTranslation/group"), "kates-mm2-e2e"},
	})
	if _, has := m["secretSync"]; has {
		t.Error("the legacy SASL Secret is created in the mirror namespace by the CLI, not synced")
	}
	if _, has := m["networkPolicy"]; has {
		t.Error("networkPolicy must be left to the chart when no operator namespace is given")
	}

	// A two-broker target caps the replication factor at two; five brokers
	// keep the durable default of three; an explicit RF wins.
	two, err := MirrorValues(l, MirrorInputs{TargetBrokerCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := at(t, values(t, two), path("mirrors/0/sourceConnector/config", "replication.factor")); got != 2 {
		t.Errorf("RF for 2 brokers = %v", got)
	}
	five, err := MirrorValues(l, MirrorInputs{TargetBrokerCount: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got := at(t, values(t, five), path("target/config", "offset.storage.replication.factor")); got != 3 {
		t.Errorf("RF for 5 brokers = %v", got)
	}
	explicit, err := MirrorValues(l, MirrorInputs{TargetBrokerCount: 5, TargetRF: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := at(t, values(t, explicit), path("target/config", "offset.storage.replication.factor")); got != 2 {
		t.Errorf("explicit RF = %v", got)
	}
}

func TestMirrorValuesStrimziTLS(t *testing.T) {
	l, err := New(Options{From: v("4.2.1"), To: v("4.3.0"), TargetIsPrimary: true,
		SourceProvider: kafkaversion.ProviderStrimzi, SourceTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := MirrorValues(l, MirrorInputs{TargetBrokerCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	// The 4x preset does not exist yet; validate on the chart defaults only.
	validateAgainstChart(t, MirrorChartPath, nil, doc)
	m := values(t, doc)
	assertValues(t, m, []check{
		{path("mirrors/0/source/bootstrapServers"), "m421-430-src-kafka-bootstrap.kafka-m421-430-src.svc.cluster.local:9093"},
		{path("mirrors/0/source/kafkaVersion"), "4.2.1"},
		{path("mirrors/0/source/tls/enabled"), true},
		{path("mirrors/0/source/tls/trustedCertificateSecret"), "m421-430-src-cluster-ca-cert"},
		{path("mirrors/0/source/tls/certificateKey"), "ca.crt"},
		{path("mirrors/0/source/authentication/type"), "scram-sha-512"},
		{path("mirrors/0/source/authentication/username"), "kates-mm2"},
		{path("mirrors/0/source/authentication/secretName"), "mm2-m421-430-source"},
		{path("secretSync/enabled"), true},
		{path("secretSync/secrets/0/name"), "m421-430-src-cluster-ca-cert"},
		{path("secretSync/secrets/0/fromNamespace"), "kafka-m421-430-src"},
	})
	// Only the CA is synced by the chart: the credential is copied under
	// its own name by the CLI, because the source's Secret is named like
	// the target's.
	if list := at(t, m, path("secretSync/secrets")).([]any); len(list) != 1 {
		t.Errorf("secretSync copies %d Secrets, want 1", len(list))
	}

	// A credential the caller keeps in another namespace under its own
	// name is synced too.
	in := l.MirrorInputs()
	in.SourceSecret = "mm2-reader-credential"
	in.SourceSecretNamespace = "kafka-shared"
	doc, err = MirrorValues(l, in)
	if err != nil {
		t.Fatal(err)
	}
	if list := at(t, values(t, doc), path("secretSync/secrets")).([]any); len(list) != 2 {
		t.Errorf("secretSync copies %d Secrets, want 2:\n%s", len(list), doc)
	}
}

func TestMirrorValuesReadOnlySource(t *testing.T) {
	l := legacyLab(t)
	// Off by default: the lab owns both ends of its own source, so Kafka's
	// own location — the source — stands.
	doc, err := MirrorValues(l, l.MirrorInputs())
	if err != nil {
		t.Fatal(err)
	}
	if m := at(t, values(t, doc), path("mirrors/0")).(map[string]any); m["readOnlySource"] != nil {
		t.Errorf("readOnlySource = %v, want it left to the chart", m["readOnlySource"])
	}

	ro, err := New(Options{From: v("2.8.2"), To: v("4.3.0"), TargetIsPrimary: true, ReadOnlySource: true})
	if err != nil {
		t.Fatal(err)
	}
	in := ro.MirrorInputs()
	if !in.ReadOnlySource {
		t.Fatal("MirrorInputs does not carry ReadOnlySource")
	}
	in.TargetBrokerCount = 1
	doc, err = MirrorValues(ro, in)
	if err != nil {
		t.Fatal(err)
	}
	validateAgainstChart(t, MirrorChartPath, []string{EraOverlay(ro.From)}, doc)
	assertValues(t, values(t, doc), []check{{path("mirrors/0/readOnlySource"), true}})
}

func TestMirrorValuesFanIn(t *testing.T) {
	l, err := New(Options{From: v("2.8.2"), AlsoFrom: []SourceSpec{{From: v("3.9.1")}}, To: v("4.3.0"),
		TargetIsPrimary: true, SASL: true, SourcePassword: "pw", StrimziVersion: "1.1.0", ReadOnlySource: true})
	if err != nil {
		t.Fatal(err)
	}
	in := l.MirrorInputs()
	in.TargetBrokerCount = 1
	doc, err := MirrorValues(l, in)
	if err != nil {
		t.Fatal(err)
	}
	validateAgainstChart(t, MirrorChartPath, []string{EraOverlay(l.From)}, doc)
	m := values(t, doc)
	if list := at(t, m, path("mirrors")).([]any); len(list) != 2 {
		t.Fatalf("mirrors has %d entries, want one per source", len(list))
	}
	assertValues(t, m, []check{
		{path("mirrors/0/source/alias"), "src282"},
		{path("mirrors/0/source/kafkaVersion"), "2.8.2"},
		{path("mirrors/0/source/bootstrapServers"), "m282-391-430-src282-legacy-kafka-bootstrap.kafka-m282-391-430-src282.svc.cluster.local:9094"},
		{path("mirrors/0/source/authentication/secretName"), "m282-391-430-src282-mm2reader"},
		{path("mirrors/0/topics/0"), "kates.orders.src282"},
		{path("mirrors/0/readOnlySource"), true},
		{path("mirrors/1/source/alias"), "src391"},
		{path("mirrors/1/source/kafkaVersion"), "3.9.1"},
		{path("mirrors/1/source/bootstrapServers"), "m282-391-430-src391-legacy-kafka-bootstrap.kafka-m282-391-430-src391.svc.cluster.local:9094"},
		{path("mirrors/1/source/authentication/secretName"), "m282-391-430-src391-mm2reader"},
		{path("mirrors/1/source/authentication/type"), "plain"},
		{path("mirrors/1/topics/0"), "kates.orders.src391"},
		{path("mirrors/1/readOnlySource"), true},
		{path("mirrors/1/sourceConnector/config", "replication.factor"), 1},
	})
	// Each source's corpus is its own, and each release carries its own
	// era's values.
	for i, want := range []string{"zookeeper", "kraft"} {
		src, err := SourceValuesFor(l, l.Sources()[i])
		if err != nil {
			t.Fatal(err)
		}
		validateAgainstChart(t, SourceChartPath, nil, src)
		assertValues(t, values(t, src), []check{
			{path("mode"), want},
			{path("topics/0/name"), l.Sources()[i].Topics[0]},
			{path("extraLabels", LabelSourceAlias), l.Sources()[i].Alias},
			{path("listeners/sasl/users/0/password"), l.Sources()[i].Password},
		})
	}
}

func TestMirrorValuesErrors(t *testing.T) {
	l := legacyLab(t)
	strimzi, err := New(Options{From: v("4.2.1"), To: v("4.3.0"), TargetIsPrimary: true, SourceProvider: kafkaversion.ProviderStrimzi})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		lab  *Lab
		in   MirrorInputs
		want string
	}{
		{"nil lab", nil, MirrorInputs{}, "lab is required"},
		{"bad bootstrap", l, MirrorInputs{SourceBootstrap: "no-port"}, "host:port"},
		{"below floor", l, MirrorInputs{SourceVersion: v("2.0.0")}, "KIP-896"},
		{"negative brokers", l, MirrorInputs{TargetBrokerCount: -1}, "negative"},
		{"rf above brokers", l, MirrorInputs{TargetBrokerCount: 1, TargetRF: 3}, "exceeds"},
		{"sasl without user", l, MirrorInputs{SourceAuth: SourceAuthSASL, SourceMechanism: MechanismPlain, SourceSecret: "s"}, "needs a user"},
		{"sasl without mechanism", l, MirrorInputs{SourceAuth: SourceAuthSASL, SourceUser: "u", SourceSecret: "s"}, "needs a SASL mechanism"},
		{"bad mechanism", l, MirrorInputs{SourceAuth: SourceAuthSASL, SourceMechanism: "gssapi", SourceUser: "u", SourceSecret: "s"}, "mechanism"},
		{"bad auth", l, MirrorInputs{SourceAuth: "mtls"}, "source auth"},
		{"tls without ca", l, MirrorInputs{SourceAuth: SourceAuthTLS, SourceMechanism: MechanismScramSHA512, SourceUser: "u", SourceSecret: "s"}, "CA Secret"},
		{"secret named like the target's", strimzi, MirrorInputs{SourceAuth: SourceAuthSASL, SourceMechanism: MechanismScramSHA512, SourceUser: "kates-mm2", SourceSecret: "kates-mm2"}, "target credential's name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := MirrorValues(tt.lab, tt.in)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("MirrorValues = %v, want an error mentioning %q", err, tt.want)
			}
		})
	}
}

func TestLabMirrorInputs(t *testing.T) {
	l := legacyLab(t)
	got := l.MirrorInputs()
	want := MirrorInputs{
		SourceBootstrap: l.SourceBootstrap, SourceVersion: v("2.8.2"), SourceAuth: SourceAuthNone,
		TargetCluster: "krafter", TargetNamespace: "kafka", StrimziVersion: "1.1.0", KafkaVersion: "4.3.0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MirrorInputs = %+v\nwant %+v", got, want)
	}
}

func TestSecretManifest(t *testing.T) {
	got, err := SecretManifest("kafka", "m391-430-src-mm2reader", map[string]string{LabelLab: "m391-430"}, map[string]string{"password": `p"w\d`})
	if err != nil {
		t.Fatal(err)
	}
	want := `apiVersion: v1
kind: Secret
metadata:
    name: m391-430-src-mm2reader
    namespace: kafka
    labels:
        kates.io/lab: m391-430
type: Opaque
stringData:
    password: p"w\d
`
	if got != want {
		t.Errorf("SecretManifest:\n%s\nwant:\n%s", got, want)
	}
	var back struct {
		StringData map[string]string `yaml:"stringData"`
	}
	if err := yaml.Unmarshal([]byte(got), &back); err != nil {
		t.Fatal(err)
	}
	if back.StringData["password"] != `p"w\d` {
		t.Errorf("password round-trips to %q", back.StringData["password"])
	}
	for _, bad := range []struct{ ns, name string }{{"", "x"}, {"ns", ""}} {
		if _, err := SecretManifest(bad.ns, bad.name, nil, map[string]string{"k": "v"}); err == nil {
			t.Errorf("SecretManifest(%q, %q) must fail", bad.ns, bad.name)
		}
	}
	if _, err := SecretManifest("ns", "n", nil, nil); err == nil {
		t.Error("SecretManifest without data must fail")
	}
}

// TestMirrorValuesRenderWithHelm renders the generated overlay on top of the
// era preset with the real chart, the way the CLI installs it, and checks
// the KafkaMirrorMaker2 the chart produces. It needs helm on PATH and skips
// without it; it touches no cluster.
func TestMirrorValuesRenderWithHelm(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not on PATH")
	}
	chart := filepath.Join(repoRoot, MirrorChartPath)
	if _, err := os.Stat(chart); err != nil {
		t.Skipf("chart not available: %v", err)
	}
	l := legacyLab(t)
	doc, err := MirrorValues(l, MirrorInputs{TargetBrokerCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(t.TempDir(), "mirror.yaml")
	if err := os.WriteFile(overlay, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, helm, "template", l.MirrorRelease, chart, "-n", l.MirrorNamespace,
		"-f", filepath.Join(chart, EraOverlay(l.From)), "-f", overlay, "-s", "templates/kafka-mirror-maker2.yaml")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	rendered := string(out)
	for _, want := range []string{
		"name: mm2-m282-430-mirror-maker2",
		"kates.io/lab: m282-430",
		"kates.io/lab-role: mirror",
		`groupId: "mm2-m282-430"`,
		`topicsPattern: "kates\\.orders"`,
		`bootstrapServers: "m282-430-src-legacy-kafka-bootstrap.kafka-m282-430-src.svc.cluster.local:9092"`,
		"replication.policy.class: org.apache.kafka.connect.mirror.IdentityReplicationPolicy",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered CR lacks %q:\n%s", want, rendered)
		}
	}
}
