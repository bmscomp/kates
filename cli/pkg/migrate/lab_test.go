package migrate

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
)

func v(s string) kafkaversion.Version { return kafkaversion.MustParse(s) }

func legacyLab(t *testing.T) *Lab {
	t.Helper()
	l, err := New(Options{From: v("2.8.2"), To: v("4.3.0"), TargetIsPrimary: true, StrimziVersion: "1.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestNewLegacyDefaults(t *testing.T) {
	l := legacyLab(t)
	want := map[string]string{
		"Name":            "m282-430",
		"Policy":          PolicyIdentity,
		"Scope":           ScopeCluster,
		"SourceProvider":  string(kafkaversion.ProviderLegacy),
		"SourceMode":      string(kafkaversion.LegacyZooKeeper),
		"TargetCluster":   "krafter",
		"TargetNamespace": "kafka",
		"SourceNamespace": "kafka-m282-430-src",
		"SourceCluster":   "m282-430-src",
		"SourceRelease":   "m282-430-src",
		"SourceImage":     "ghcr.io/bmscomp/kates-legacy-kafka:2.8.2",
		"SourceBootstrap": "m282-430-src-legacy-kafka-bootstrap.kafka-m282-430-src.svc.cluster.local:9092",
		"SourceUser":      "",
		"SourceSecret":    "",
		"MirrorRelease":   "mm2-m282-430",
		"MirrorNamespace": "kafka",
		"MirrorCR":        "mm2-m282-430-mirror-maker2",
		"GroupID":         "mm2-m282-430",
		"SourceAlias":     "source",
		"ConsumerGroup":   "kates-migration-m282-430",
		"VerifyGroup":     "kates-migration-verify-m282-430",
	}
	got := map[string]string{
		"Name": l.Name, "Policy": l.Policy, "Scope": l.Scope,
		"SourceProvider": string(l.SourceProvider), "SourceMode": string(l.SourceMode),
		"TargetCluster": l.TargetCluster, "TargetNamespace": l.TargetNamespace,
		"SourceNamespace": l.SourceNamespace, "SourceCluster": l.SourceCluster, "SourceRelease": l.SourceRelease,
		"SourceImage": l.SourceImage, "SourceBootstrap": l.SourceBootstrap,
		"SourceUser": l.SourceUser, "SourceSecret": l.SourceSecret,
		"MirrorRelease": l.MirrorRelease, "MirrorNamespace": l.MirrorNamespace, "MirrorCR": l.MirrorCR,
		"GroupID": l.GroupID, "SourceAlias": l.SourceAlias,
		"ConsumerGroup": l.ConsumerGroup, "VerifyGroup": l.VerifyGroup,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
	if !reflect.DeepEqual(l.Topics, []string{DefaultTopic}) || l.Messages != DefaultMessages {
		t.Errorf("topics %v messages %d", l.Topics, l.Messages)
	}
	if !reflect.DeepEqual(l.Labels, map[string]string{LabelLab: "m282-430"}) {
		t.Errorf("labels = %v", l.Labels)
	}
	if got := l.RoleLabels(RoleMirror); !reflect.DeepEqual(got, map[string]string{LabelLab: "m282-430", LabelRole: "mirror"}) {
		t.Errorf("RoleLabels = %v", got)
	}
	if l.Selector() != "kates.io/lab=m282-430" {
		t.Errorf("Selector = %q", l.Selector())
	}
	if l.SourceAuth() != SourceAuthNone || l.SourceMechanism() != "" || l.SASL || l.SourcePassword != "" {
		t.Errorf("plaintext lab has auth %q/%q sasl=%v", l.SourceAuth(), l.SourceMechanism(), l.SASL)
	}
	if got := l.ReplicatedTopic("kates.orders"); got != "kates.orders" {
		t.Errorf("identity ReplicatedTopic = %q", got)
	}
	if got := l.Describe(); got != "Kafka 2.8.2 (legacy, zookeeper) → 4.3.0 (krafter in kafka)" {
		t.Errorf("Describe = %q", got)
	}
}

func TestNewLegacySASLAndOptions(t *testing.T) {
	l, err := New(Options{
		From: v("3.9.1"), To: v("4.3.0"), TargetIsPrimary: true, SASL: true, Policy: PolicyDefault,
		Topics: []string{"kates.orders", "kates.payments"}, Messages: 50, Registry: "registry.local:5000/kates",
	})
	if err != nil {
		t.Fatal(err)
	}
	if l.Name != "m391-430" || l.SourceMode != kafkaversion.LegacyKRaftOfficial {
		t.Errorf("name %q mode %q", l.Name, l.SourceMode)
	}
	if l.SourceImage != "apache/kafka:3.9.1" {
		t.Errorf("SourceImage = %q", l.SourceImage)
	}
	if l.SourceBootstrap != "m391-430-src-legacy-kafka-bootstrap.kafka-m391-430-src.svc.cluster.local:9094" {
		t.Errorf("SASL bootstrap = %q", l.SourceBootstrap)
	}
	if l.SourceUser != LegacySASLUser || l.SourceSecret != "m391-430-src-mm2reader" {
		t.Errorf("user %q secret %q", l.SourceUser, l.SourceSecret)
	}
	if len(l.SourcePassword) != 32 {
		t.Errorf("generated password %q is not 32 hex chars", l.SourcePassword)
	}
	if l.SourceAuth() != SourceAuthSASL || l.SourceMechanism() != MechanismPlain {
		t.Errorf("auth %q mechanism %q", l.SourceAuth(), l.SourceMechanism())
	}
	if got := l.ReplicatedTopic("kates.orders"); got != "source.kates.orders" {
		t.Errorf("default-policy ReplicatedTopic = %q", got)
	}
	if l.Messages != 50 || len(l.Topics) != 2 {
		t.Errorf("messages %d topics %v", l.Messages, l.Topics)
	}

	fixed, err := New(Options{From: v("3.9.1"), To: v("4.3.0"), TargetIsPrimary: true, SASL: true, SourcePassword: "lab-literal"})
	if err != nil {
		t.Fatal(err)
	}
	if fixed.SourcePassword != "lab-literal" {
		t.Errorf("SourcePassword = %q, want the one given", fixed.SourcePassword)
	}
	built, err := New(Options{From: v("3.5.2"), To: v("4.3.0"), TargetIsPrimary: true})
	if err != nil {
		t.Fatal(err)
	}
	if built.SourceMode != kafkaversion.LegacyKRaftBuilt || built.SourceImage != "ghcr.io/bmscomp/kates-legacy-kafka:3.5.2" {
		t.Errorf("3.5.2: mode %q image %q", built.SourceMode, built.SourceImage)
	}
}

func TestNewStrimziSource(t *testing.T) {
	l, err := New(Options{From: v("4.1.2"), To: v("4.3.0"), TargetIsPrimary: true,
		SourceProvider: kafkaversion.ProviderStrimzi, SourceStrimziVersion: "1.0.1", Scope: ScopeNamespace})
	if err != nil {
		t.Fatal(err)
	}
	if l.SourceMode != "" || l.SourceImage != "" {
		t.Errorf("strimzi source has legacy mode %q image %q", l.SourceMode, l.SourceImage)
	}
	if l.SourceBootstrap != "m412-430-src-kafka-bootstrap.kafka-m412-430-src.svc.cluster.local:9092" {
		t.Errorf("bootstrap = %q", l.SourceBootstrap)
	}
	if l.SourceUser != TargetUser || l.SourceSecret != "mm2-m412-430-source" || l.SourceCASecret != "" {
		t.Errorf("user %q secret %q ca %q", l.SourceUser, l.SourceSecret, l.SourceCASecret)
	}
	if l.SourceAuth() != SourceAuthSASL || l.SourceMechanism() != MechanismScramSHA512 || !l.SASL {
		t.Errorf("auth %q mechanism %q sasl %v", l.SourceAuth(), l.SourceMechanism(), l.SASL)
	}
	if got := l.Describe(); got != "Kafka 4.1.2 (strimzi 1.0.1) → 4.3.0 (krafter in kafka)" {
		t.Errorf("Describe = %q", got)
	}

	tls, err := New(Options{From: v("4.2.1"), To: v("4.3.0"), TargetIsPrimary: true,
		SourceProvider: kafkaversion.ProviderStrimzi, SourceTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	if tls.SourceBootstrap != "m421-430-src-kafka-bootstrap.kafka-m421-430-src.svc.cluster.local:9093" {
		t.Errorf("TLS bootstrap = %q", tls.SourceBootstrap)
	}
	if tls.SourceCASecret != "m421-430-src-cluster-ca-cert" || tls.SourceAuth() != SourceAuthTLS {
		t.Errorf("ca %q auth %q", tls.SourceCASecret, tls.SourceAuth())
	}
	if got := tls.Describe(); got != "Kafka 4.2.1 (strimzi) → 4.3.0 (krafter in kafka)" {
		t.Errorf("Describe = %q", got)
	}
}

func TestNewAdditionalTargetAndName(t *testing.T) {
	l, err := New(Options{From: v("2.8.2"), To: v("4.2.1"), Name: "lab1"})
	if err != nil {
		t.Fatal(err)
	}
	if l.TargetCluster != "lab1-tgt" || l.TargetNamespace != "kafka-lab1-tgt" || l.MirrorNamespace != "kafka-lab1-tgt" {
		t.Errorf("additional target: %q %q mirror ns %q", l.TargetCluster, l.TargetNamespace, l.MirrorNamespace)
	}
	explicit, err := New(Options{From: v("2.8.2"), To: v("4.3.0"), TargetCluster: "main", TargetNamespace: "kafka-main"})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.TargetCluster != "main" || explicit.MirrorNamespace != "kafka-main" {
		t.Errorf("explicit target: %q %q", explicit.TargetCluster, explicit.MirrorNamespace)
	}
	if got := DefaultName(v("10.2.0"), v("11.0.1")); got != "m1020-1101" {
		t.Errorf("DefaultName = %q", got)
	}
}

func TestNewFanInSources(t *testing.T) {
	l, err := New(Options{
		From: v("2.8.2"), AlsoFrom: []SourceSpec{{From: v("3.9.1")}},
		To: v("4.3.0"), TargetIsPrimary: true, StrimziVersion: "1.1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if l.Name != "m282-391-430" {
		t.Errorf("fan-in name = %q, want m282-391-430", l.Name)
	}
	if !l.FanIn() || len(l.Sources()) != 2 {
		t.Fatalf("FanIn=%v sources=%d", l.FanIn(), len(l.Sources()))
	}
	sources := l.Sources()
	want := []struct {
		alias, namespace, release, image, bootstrap, topic string
	}{
		{"src282", "kafka-m282-391-430-src282", "m282-391-430-src282", "ghcr.io/bmscomp/kates-legacy-kafka:2.8.2",
			"m282-391-430-src282-legacy-kafka-bootstrap.kafka-m282-391-430-src282.svc.cluster.local:9092", "kates.orders.src282"},
		{"src391", "kafka-m282-391-430-src391", "m282-391-430-src391", "apache/kafka:3.9.1",
			"m282-391-430-src391-legacy-kafka-bootstrap.kafka-m282-391-430-src391.svc.cluster.local:9092", "kates.orders.src391"},
	}
	for i, w := range want {
		s := sources[i]
		got := []string{s.Alias, s.Namespace, s.Release, s.Image, s.Bootstrap, strings.Join(s.Topics, ",")}
		for j, g := range []string{w.alias, w.namespace, w.release, w.image, w.bootstrap, w.topic} {
			if got[j] != g {
				t.Errorf("source %d field %d = %q, want %q", i, j, got[j], g)
			}
		}
	}
	if sources[0].Mode != kafkaversion.LegacyZooKeeper || sources[1].Mode != kafkaversion.LegacyKRaftOfficial {
		t.Errorf("modes %q %q", sources[0].Mode, sources[1].Mode)
	}
	// The flat fields are the first source, so every single-source command
	// keeps working; the lab's topics are every source's, in order.
	if l.SourceAlias != "src282" || l.SourceRelease != "m282-391-430-src282" || l.SourceImage != sources[0].Image {
		t.Errorf("flat fields do not describe the first source: %q %q %q", l.SourceAlias, l.SourceRelease, l.SourceImage)
	}
	if !reflect.DeepEqual(l.Topics, []string{"kates.orders.src282", "kates.orders.src391"}) {
		t.Errorf("lab topics = %v", l.Topics)
	}
	if !reflect.DeepEqual(l.ReplicatedTopics(), l.Topics) {
		t.Errorf("identity ReplicatedTopics = %v", l.ReplicatedTopics())
	}
	if got := l.RowFor(RowSourceDeployed, sources[1]); got != RowSourceDeployed+" [src391]" {
		t.Errorf("RowFor = %q", got)
	}
	if got := l.SourceLabels(sources[1]); got[LabelSourceAlias] != "src391" || got[LabelLab] != l.Name {
		t.Errorf("SourceLabels = %v", got)
	}
	if got := l.Describe(); got != "Kafka 2.8.2 (legacy, zookeeper) + Kafka 3.9.1 (legacy, kraft-official) → 4.3.0 (krafter in kafka)" {
		t.Errorf("Describe = %q", got)
	}

	// One source keeps the names it always had, and no alias label.
	one := legacyLab(t)
	if one.FanIn() || one.SourceAlias != SourceAlias || one.SourceRelease != "m282-430-src" {
		t.Errorf("single source changed: fanIn=%v alias=%q release=%q", one.FanIn(), one.SourceAlias, one.SourceRelease)
	}
	if _, has := one.SourceLabels(one.Source())[LabelSourceAlias]; has {
		t.Error("a single-source lab must not stamp an alias label")
	}
	if got := one.RowFor(RowSourceDeployed, one.Source()); got != RowSourceDeployed {
		t.Errorf("single-source RowFor = %q", got)
	}
}

func TestNewFanInRefusals(t *testing.T) {
	// The same version twice derives one alias, which the chart would refuse
	// at render time; the CLI refuses first and names both --from values.
	_, err := New(Options{From: v("2.8.2"), AlsoFrom: []SourceSpec{{From: v("2.8.2")}}, To: v("4.3.0"), TargetIsPrimary: true})
	if err == nil || !strings.Contains(err.Error(), `--from 2.8.2 and --from 2.8.2 both derive the source alias "src282"`) {
		t.Fatalf("duplicate alias = %v", err)
	}
	// Named topics are the user's own names, so two sources under the
	// identity policy would write one target topic: refused, with both
	// versions and the ways out.
	_, err = New(Options{From: v("2.8.2"), AlsoFrom: []SourceSpec{{From: v("3.9.1")}}, To: v("4.3.0"),
		TargetIsPrimary: true, Topics: []string{"orders"}})
	if err == nil || !strings.Contains(err.Error(), "--from 2.8.2 and --from 3.9.1 would both mirror topic \"orders\"") ||
		!strings.Contains(err.Error(), "--policy default") {
		t.Fatalf("identity fan-in = %v", err)
	}
	// The default policy renames them per alias, so the same set is fine.
	shared, err := New(Options{From: v("2.8.2"), AlsoFrom: []SourceSpec{{From: v("3.9.1")}}, To: v("4.3.0"),
		TargetIsPrimary: true, Topics: []string{"orders"}, Policy: PolicyDefault})
	if err != nil {
		t.Fatalf("default policy fan-in: %v", err)
	}
	if got := shared.ReplicatedTopics(); !reflect.DeepEqual(got, []string{"src282.orders", "src391.orders"}) {
		t.Errorf("ReplicatedTopics = %v", got)
	}
	// A further source is held to the same floor as the first.
	if _, err := New(Options{From: v("2.8.2"), AlsoFrom: []SourceSpec{{From: v("2.0.1")}}, To: v("4.3.0"), TargetIsPrimary: true}); !errors.Is(err, kafkaversion.ErrBelowMirrorFloor) {
		t.Fatalf("floor for a further source = %v", err)
	}
	if got := SourceAliasFor(v("4.10.1")); got != "src4101" {
		t.Errorf("SourceAliasFor = %q", got)
	}
}

func TestNewErrors(t *testing.T) {
	ok := Options{From: v("2.8.2"), To: v("4.3.0"), TargetIsPrimary: true}
	tests := []struct {
		name   string
		mutate func(*Options)
		want   string
	}{
		{"no from", func(o *Options) { o.From = kafkaversion.Version{} }, "--from"},
		{"no to", func(o *Options) { o.To = kafkaversion.Version{} }, "--to"},
		{"below floor", func(o *Options) { o.From = v("2.0.1") }, "KIP-896"},
		{"bad name", func(o *Options) { o.Name = "Lab_1" }, "lab name"},
		{"long name", func(o *Options) { o.Name = strings.Repeat("a", 25) }, "at most 24"},
		{"bad policy", func(o *Options) { o.Policy = "rename" }, "policy"},
		{"bad scope", func(o *Options) { o.Scope = "global" }, "scope"},
		{"bad provider", func(o *Options) { o.SourceProvider = "docker" }, "source provider"},
		{"bad mode", func(o *Options) { o.SourceMode = "raft" }, "source mode"},
		{"operator for legacy", func(o *Options) { o.SourceStrimziVersion = "1.0.1" }, "no operator"},
		{"bad topic", func(o *Options) { o.Topics = []string{"kates/orders"} }, "topic"},
		{"dot topic", func(o *Options) { o.Topics = []string{"."} }, "topic"},
		{"duplicate topic", func(o *Options) { o.Topics = []string{"a", "a"} }, "twice"},
		{"negative messages", func(o *Options) { o.Messages = -1 }, "messages"},
		{"bad target ns", func(o *Options) { o.TargetNamespace = "Kafka" }, "target namespace"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := ok
			tt.mutate(&o)
			_, err := New(o)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New = %v, want an error mentioning %q", err, tt.want)
			}
			if tt.name == "below floor" && !errors.Is(err, kafkaversion.ErrBelowMirrorFloor) {
				t.Errorf("floor error %v does not wrap ErrBelowMirrorFloor", err)
			}
		})
	}
}
