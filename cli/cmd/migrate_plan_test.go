package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
	"github.com/bmscomp/kates/cli/pkg/migrate"
	"github.com/bmscomp/kates/cli/pkg/strimzi"
)

// fakeMigrateEnv is the environment the resolution tests plan against: a
// Strimzi 1.1.0 primary with the window 4.2.0 4.2.1 4.3.0 and a 4.3.0
// target, cluster-wide or namespace-scoped.
func fakeMigrateEnv(scope string) *migrateEnv {
	op := strimzi.Operator{
		Namespace: "strimzi-operator", Name: "strimzi-cluster-operator", Version: "1.1.0",
		Scope: strimzi.ScopeCluster, Watches: []string{"*"},
		Window: kafkaversion.NewWindow(kafkaversion.MustParse("4.2.0"), kafkaversion.MustParse("4.2.1"), kafkaversion.MustParse("4.3.0")),
	}
	if scope == migrate.ScopeNamespace {
		op.Scope, op.Watches = strimzi.ScopeNamespaces, []string{"kafka", "connect"}
	}
	env := &migrateEnv{
		Operators: []strimzi.Operator{op}, Primary: &op, Window: op.Window,
		StrimziVersion: "1.1.0", OperatorNamespace: "strimzi-operator", IsKind: true,
		Target: migrateTarget{Cluster: "krafter", Namespace: "kafka", Found: true, Version: kafkaversion.MustParse("4.3.0"), Ready: true, Brokers: 1, HasSecret: true},
		Pins:   migrate.Pins{StrimziVersion: "1.1.0", KafkaVersion: "4.3.0", KafkaImage: "quay.io/strimzi/kafka:1.1.0-kafka-4.3.0"},
	}
	env.Scope = env.primaryScope()
	return env
}

func TestMigrateResolvePair(t *testing.T) {
	cases := []struct {
		name           string
		scope          string
		from, to       string
		provider       string
		wantProvider   kafkaversion.Provider
		wantMode       kafkaversion.LegacyMode
		wantErr        string
		wantNote       string
		wantUnimpl     bool
		wantAdditional bool
		wantName       string
	}{
		{name: "2.8.2 is legacy ZooKeeper", from: "2.8.2", wantProvider: kafkaversion.ProviderLegacy, wantMode: kafkaversion.LegacyZooKeeper,
			wantNote: "serve the v1beta2 API only", wantName: "m282-430"},
		{name: "3.9.1 is legacy on the official image", from: "3.9.1", wantProvider: kafkaversion.ProviderLegacy, wantMode: kafkaversion.LegacyKRaftOfficial,
			wantNote: "No Strimzi that can sit beside this platform's CRDs runs Kafka 3.9.1", wantName: "m391-430"},
		{name: "3.5.2 is legacy on the built KRaft image", from: "3.5.2", wantProvider: kafkaversion.ProviderLegacy, wantMode: kafkaversion.LegacyKRaftBuilt},
		{name: "4.1.2 under cluster scope is legacy with the §3.1 sentence", from: "4.1.2", wantProvider: kafkaversion.ProviderLegacy, wantMode: kafkaversion.LegacyKRaftOfficial,
			wantNote: "Kafka 4.1.2 cannot run under Strimzi 1.1.0, the cluster-wide operator on this cluster (supported: 4.2.0 4.2.1 4.3.0). A cluster-wide operator watches every namespace, so no second Strimzi can be installed beside it."},
		{name: "4.1.2 under namespace scope is a Strimzi source, plan only", scope: migrate.ScopeNamespace, from: "4.1.2", wantProvider: kafkaversion.ProviderStrimzi, wantUnimpl: true},
		{name: "4.2.1 is a Strimzi source on the primary's operator, plan only", from: "4.2.1", wantProvider: kafkaversion.ProviderStrimzi, wantUnimpl: true},
		{name: "4.2.1 with --source-provider legacy runs under the official image", from: "4.2.1", provider: "legacy", wantProvider: kafkaversion.ProviderLegacy, wantMode: kafkaversion.LegacyKRaftOfficial,
			wantNote: "is in the primary operator's window"},
		{name: "2.0.0 is below the KIP-896 floor", from: "2.0.0", wantErr: "below the KIP-896 floor"},
		{name: "--to outside the window is refused with the window", from: "2.8.2", to: "4.1.2", wantErr: "not in the primary operator's window (Strimzi 1.1.0 supports: 4.2.0 4.2.1 4.3.0)"},
		{name: "--to another in-window version is an additional target, plan only", from: "2.8.2", to: "4.2.1", wantProvider: kafkaversion.ProviderLegacy, wantMode: kafkaversion.LegacyZooKeeper, wantUnimpl: true, wantAdditional: true, wantName: "m282-421"},
		{name: "--source-provider strimzi for the dropped line under cluster scope is the §3.1 refusal", from: "4.1.2", provider: "strimzi",
			wantErr: "run it without an operator:   kates migrate up --from 4.1.2 --source-provider legacy"},
		{name: "--source-provider strimzi for a 3.x source is refused", from: "3.9.1", provider: "strimzi", wantErr: "No Strimzi that can sit beside this platform's CRDs runs Kafka 3.9.1"},
		{name: "a malformed version is refused", from: "2.8", wantErr: "invalid version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := fakeMigrateEnv(tc.scope)
			f := newPairFlags(tc.from)
			f.To = tc.to
			if tc.provider != "" {
				f.SourceProvider = tc.provider
			}
			res, err := resolveMigratePair(env, f)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				if tc.from == "2.0.0" && !errors.Is(err, kafkaversion.ErrBelowMirrorFloor) {
					t.Fatalf("want ErrBelowMirrorFloor, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Lab.SourceProvider != tc.wantProvider {
				t.Fatalf("provider = %s, want %s", res.Lab.SourceProvider, tc.wantProvider)
			}
			if res.Lab.SourceMode != tc.wantMode {
				t.Fatalf("mode = %q, want %q", res.Lab.SourceMode, tc.wantMode)
			}
			if tc.wantNote != "" && !strings.Contains(strings.Join(res.Notes, "\n"), tc.wantNote) {
				t.Fatalf("notes %q do not carry %q", res.Notes, tc.wantNote)
			}
			if (len(res.Unimplemented) > 0) != tc.wantUnimpl {
				t.Fatalf("unimplemented = %v, want %v", res.Unimplemented, tc.wantUnimpl)
			}
			if res.TargetAdditional != tc.wantAdditional {
				t.Fatalf("additional target = %v, want %v", res.TargetAdditional, tc.wantAdditional)
			}
			if tc.wantName != "" && res.Lab.Name != tc.wantName {
				t.Fatalf("lab name = %s, want %s", res.Lab.Name, tc.wantName)
			}
		})
	}
}

func TestMigrateResolvePairClusterScopeRefusalShape(t *testing.T) {
	env := fakeMigrateEnv("")
	f := newPairFlags("4.1.2")
	f.SourceProvider = "strimzi"
	_, err := resolveMigratePair(env, f)
	if err == nil {
		t.Fatal("want a refusal")
	}
	msg := err.Error()
	for _, want := range []string{
		"(supported: 4.2.0 4.2.1 4.3.0)",
		"no second Strimzi can be installed beside it",
		"--source-provider legacy",
		"kates deploy --operator-scope namespace",
		"--source-strimzi-version",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal lacks %q:\n%s", want, msg)
		}
	}
}

func TestMigrateResolvePairWarnsOnDirection(t *testing.T) {
	env := fakeMigrateEnv("")
	f := newPairFlags("4.3.0")
	f.SourceProvider = "legacy"
	res, err := resolveMigratePair(env, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "not older than") {
		t.Fatalf("want a direction warning, got %v", res.Warnings)
	}
}

func TestMigrateResolvePairFindsInstalledAdditionalOperator(t *testing.T) {
	env := fakeMigrateEnv(migrate.ScopeNamespace)
	env.Operators = append(env.Operators, strimzi.Operator{
		Namespace: "kafka-legacy41", Name: "strimzi-cluster-operator", Version: "1.0.1",
		Scope: strimzi.ScopeNamespaces, Watches: []string{"kafka-legacy41"},
		Window: kafkaversion.NewWindow(kafkaversion.MustParse("4.1.2"), kafkaversion.MustParse("4.2.0")),
	})
	res, err := resolveMigratePair(env, newPairFlags("4.1.2"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Lab.SourceProvider != kafkaversion.ProviderStrimzi || res.Lab.SourceStrimziVersion != "1.0.1" {
		t.Fatalf("want a Strimzi 1.0.1 source, got %s %q", res.Lab.SourceProvider, res.Lab.SourceStrimziVersion)
	}
	if !strings.Contains(res.SourceOperator, "1.0.1 in kafka-legacy41") {
		t.Fatalf("source operator = %q", res.SourceOperator)
	}
}

func TestMigratePlanDocument(t *testing.T) {
	env := fakeMigrateEnv("")
	f := newPairFlags("2.8.2")
	f.SASL = true
	res, err := resolveMigratePair(env, f)
	if err != nil {
		t.Fatal(err)
	}
	p, err := buildMigratePlan(res, f)
	if err != nil {
		t.Fatal(err)
	}
	if p.Lab.SourceNamespace != "kafka-m282-430-src" || p.Lab.MirrorRelease != "mm2-m282-430" || p.Lab.MirrorCR != "mm2-m282-430-mirror-maker2" {
		t.Fatalf("lab names: %+v", p.Lab)
	}
	if p.Mirror.MinSourceVersion != "2.1.0" || !p.Mirror.Preflight || p.Mirror.TopicsPattern != `kates\.orders` {
		t.Fatalf("mirror: %+v", p.Mirror)
	}
	if p.Mirror.EraOverlay != "charts/mirror-maker2/values-migrate-2x.yaml" {
		t.Fatalf("era overlay = %s", p.Mirror.EraOverlay)
	}
	if !strings.Contains(p.Values.Source, "<generated by up, never printed>") || strings.Contains(p.Values.Source, res.Lab.SourcePassword) {
		t.Fatalf("the plan must not print the SASL password:\n%s", p.Values.Source)
	}
	if !strings.Contains(p.Values.Mirror, "kates.io/lab: m282-430") {
		t.Fatalf("mirror values lack the lab label:\n%s", p.Values.Mirror)
	}
	if len(p.Helm) != 2 || !strings.Contains(p.Helm[1], "-f charts/mirror-maker2/values-kind.yaml -f charts/mirror-maker2/values-migrate-2x.yaml -f .build/migrate/m282-430/mirror-values.yaml") {
		t.Fatalf("helm commands: %v", p.Helm)
	}
	if !p.Source.Build {
		t.Fatal("a 2.8.2 source needs the built image")
	}
}

func TestMigratePlanCommandJSON(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	out := captureOutput(t, "json")

	flags := newPairFlags("4.1.2")
	if err := runMigratePlan(nil, flags); err != nil {
		t.Fatalf("plan: %v", err)
	}
	var doc migratePlan
	if err := json.Unmarshal([]byte(out.String()), &doc); err != nil {
		t.Fatalf("plan -o json is not a document: %v\n%s", err, out.String())
	}
	if doc.Source.Provider != "legacy" || doc.Source.Mode != "kraft-official" {
		t.Fatalf("source: %+v", doc.Source)
	}
	if !strings.Contains(strings.Join(doc.Notes, "\n"), "cluster-wide operator on this cluster (supported: 4.2.0 4.2.1 4.3.0)") {
		t.Fatalf("notes: %v", doc.Notes)
	}
	if doc.Target.Version != "4.3.0" || !doc.Target.Primary || doc.Target.Brokers != 1 {
		t.Fatalf("target: %+v", doc.Target)
	}
	if !doc.Kind {
		t.Fatal("kind was not detected through the runner")
	}
	// plan creates nothing: no helm upgrade, no kubectl apply/create.
	for _, l := range f.lines() {
		if strings.HasPrefix(l, "helm upgrade") || strings.Contains(l, " apply ") || strings.Contains(l, " create ") {
			t.Fatalf("plan ran %q", l)
		}
	}
}

func TestMigratePlanStrimziSourceIsPlanOnly(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, false)
	captureOutput(t, "table")

	flags := newPairFlags("4.2.1")
	if err := runMigratePlan(nil, flags); err != nil {
		t.Fatalf("plan --from 4.2.1 must describe the Strimzi source: %v", err)
	}
	err := runMigrateUp(nil, flags)
	if err == nil || !strings.Contains(err.Error(), "not yet implemented in this version") || !strings.Contains(err.Error(), "--source-provider legacy") {
		t.Fatalf("up --from 4.2.1 must refuse with the way out, got %v", err)
	}
	if f.hasLine("helm upgrade") {
		t.Fatal("up must not install anything for an unimplemented source")
	}
}

func TestMigratePlanFanInDocument(t *testing.T) {
	env := fakeMigrateEnv("")
	f := newPairFlags("2.8.2", "3.9.1")
	res, err := resolveMigratePair(env, f)
	if err != nil {
		t.Fatal(err)
	}
	p, err := buildMigratePlan(res, f)
	if err != nil {
		t.Fatal(err)
	}
	if p.Lab.Name != "m282-391-430" {
		t.Fatalf("lab name = %s", p.Lab.Name)
	}
	if len(p.Sources) != 2 {
		t.Fatalf("want one row per source, got %+v", p.Sources)
	}
	first, second := p.Sources[0], p.Sources[1]
	if first.Alias != "src282" || first.Namespace != "kafka-m282-391-430-src282" || first.Release != "m282-391-430-src282" {
		t.Errorf("first source row: %+v", first)
	}
	if second.Alias != "src391" || second.Version != "3.9.1" || second.Mode != "kraft-official" || second.Build {
		t.Errorf("second source row: %+v", second)
	}
	if strings.Join(first.Topics, ",") != "kates.orders.src282" || strings.Join(second.Topics, ",") != "kates.orders.src391" {
		t.Errorf("each source gets its own corpus topic: %v %v", first.Topics, second.Topics)
	}
	if first.Values != ".build/migrate/m282-391-430/source-values-src282.yaml" || second.Values != ".build/migrate/m282-391-430/source-values-src391.yaml" {
		t.Errorf("values files: %q %q", first.Values, second.Values)
	}
	if p.Source.Alias != p.Sources[0].Alias || p.Source.Bootstrap != p.Sources[0].Bootstrap {
		t.Error("the first source is still where a single-source reader looks for it")
	}
	// One release, one mirror per source, with each leg's own topics.
	if n := strings.Count(p.Values.Mirror, "alias: src"); n != 2 {
		t.Errorf("mirror values carry %d sources:\n%s", n, p.Values.Mirror)
	}
	for _, want := range []string{"alias: src282", "alias: src391", "- kates.orders.src282", "- kates.orders.src391"} {
		if !strings.Contains(p.Values.Mirror, want) {
			t.Errorf("mirror values lack %q:\n%s", want, p.Values.Mirror)
		}
	}
	// Two source installs, then the one mirror.
	if len(p.Helm) != 3 || !strings.Contains(p.Helm[0], "--install m282-391-430-src282") || !strings.Contains(p.Helm[1], "--install m282-391-430-src391") ||
		!strings.Contains(p.Helm[2], "--install mm2-m282-391-430") {
		t.Fatalf("helm commands: %v", p.Helm)
	}
}

func TestMigratePlanFanInRefusalsBeforeTheCluster(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	captureOutput(t, "table")

	// The same version twice: one alias, which the chart would refuse at
	// render time. The CLI refuses first, naming both --from values.
	err := runMigratePlan(nil, newPairFlags("2.8.2", "2.8.2"))
	if err == nil || !strings.Contains(err.Error(), `--from 2.8.2 and --from 2.8.2 both derive the source alias "src282"`) {
		t.Fatalf("duplicate alias: %v", err)
	}
	// Named topics under the identity policy: both legs would write one
	// target topic.
	flags := newPairFlags("2.8.2", "3.9.1")
	flags.Topics = "orders"
	err = runMigratePlan(nil, flags)
	if err == nil || !strings.Contains(err.Error(), `--from 2.8.2 and --from 3.9.1 would both mirror topic "orders"`) {
		t.Fatalf("identity fan-in: %v", err)
	}
	// The default policy keeps them apart, so the same flags are fine.
	flags.Policy = migrate.PolicyDefault
	if err := runMigratePlan(nil, flags); err != nil {
		t.Fatalf("default policy fan-in: %v", err)
	}
	// Nothing was created for any of the three.
	for _, l := range f.lines() {
		if strings.HasPrefix(l, "helm upgrade") || strings.Contains(l, " apply ") || strings.Contains(l, " create ") {
			t.Fatalf("plan ran %q", l)
		}
	}
}

func TestMigratePlanFanInWithAStrimziSourceStaysPlanOnly(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	scriptPrerequisites(f)
	captureOutput(t, "table")

	// 4.2.1 is in the primary's window, so it is a Strimzi source: plan
	// describes the pair, up refuses it — the mixture does not widen it.
	flags := newPairFlags("2.8.2", "4.2.1")
	if err := runMigratePlan(nil, flags); err != nil {
		t.Fatalf("plan must describe a mixed fan-in: %v", err)
	}
	err := runMigrateUp(nil, flags)
	if err == nil || !strings.Contains(err.Error(), "not yet implemented in this version") {
		t.Fatalf("up must refuse the Strimzi leg, got %v", err)
	}
	if f.hasLine("helm upgrade") {
		t.Fatal("nothing may be installed for an unimplemented source")
	}
}

func TestMigratePlanReadOnlySource(t *testing.T) {
	env := fakeMigrateEnv("")
	f := newPairFlags("2.8.2")
	f.ReadOnlySource = true
	res, err := resolveMigratePair(env, f)
	if err != nil {
		t.Fatal(err)
	}
	p, err := buildMigratePlan(res, f)
	if err != nil {
		t.Fatal(err)
	}
	if p.OffsetSyncs.Location != "target" || !p.OffsetSyncs.ReadOnlySource || p.OffsetSyncs.WritesToSource {
		t.Fatalf("offset-syncs: %+v", p.OffsetSyncs)
	}
	if !strings.Contains(p.OffsetSyncs.SourceGrants, "Read + Describe") || strings.Contains(p.OffsetSyncs.SourceGrants, "Create") {
		t.Errorf("read-only grants: %q", p.OffsetSyncs.SourceGrants)
	}
	if !strings.Contains(p.Values.Mirror, "readOnlySource: true") {
		t.Errorf("mirror values lack readOnlySource:\n%s", p.Values.Mirror)
	}
	// Off by default: the source keeps Kafka's own location, and the plan
	// says what that asks of the source principal.
	def, err := buildMigratePlan(mustResolve(t, env, newPairFlags("2.8.2")), newPairFlags("2.8.2"))
	if err != nil {
		t.Fatal(err)
	}
	if def.OffsetSyncs.Location != "source" || !def.OffsetSyncs.WritesToSource {
		t.Fatalf("default offset-syncs: %+v", def.OffsetSyncs)
	}
	if !strings.Contains(def.OffsetSyncs.SourceGrants, "Create + Write + Describe on mm2-offset-syncs.*") {
		t.Errorf("default grants: %q", def.OffsetSyncs.SourceGrants)
	}
	if strings.Contains(def.Values.Mirror, "readOnlySource") {
		t.Errorf("readOnlySource must be left to the chart when off:\n%s", def.Values.Mirror)
	}
}

func TestMigratePlanPrintsEverySourceAndTheOffsetSyncs(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	out := captureOutput(t, "table")

	flags := newPairFlags("2.8.2", "3.9.1")
	flags.ReadOnlySource = true
	if err := runMigratePlan(nil, flags); err != nil {
		t.Fatalf("plan: %v", err)
	}
	text := stripAnsi(out.String())
	for _, want := range []string{
		"source src282", "source src391",
		"kafka-m282-391-430-src282", "kafka-m282-391-430-src391",
		"kates.orders.src282", "kates.orders.src391",
		"group / aliases", "src282, src391",
		"2 in one release",
		"offset-syncs", "location", "target",
		"none — the mirror writes nothing to any source cluster",
		"Read + Describe on the mirrored topics (nothing else)",
		"Nothing was created.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("plan output lacks %q:\n%s", want, text)
		}
	}
}

func mustResolve(t *testing.T, env *migrateEnv, f *migratePairFlags) *migrateResolution {
	t.Helper()
	res, err := resolveMigratePair(env, f)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestMigratePairsRows(t *testing.T) {
	env := fakeMigrateEnv("")
	rows := migratePairRows(env)
	if len(rows) != 4 {
		t.Fatalf("want 4 rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].From != "2.1.0 – 3.2.x" || rows[1].From != "3.3.0 – 3.6.x" || rows[2].From != "3.7.0 – 4.1.x" || rows[3].From != "4.2.0 – 4.3.0" {
		t.Fatalf("from column: %q %q %q %q", rows[0].From, rows[1].From, rows[2].From, rows[3].From)
	}
	if strings.Join(rows[0].To, " ") != "4.2.0 4.2.1 [4.3.0]" {
		t.Fatalf("to column: %v", rows[0].To)
	}
	if !strings.Contains(rows[2].Note, "legacy under cluster scope") {
		t.Fatalf("dropped-line note: %q", rows[2].Note)
	}
}

func TestMigratePickPrimaryOperator(t *testing.T) {
	window := kafkaversion.NewWindow(kafkaversion.MustParse("4.3.0"))
	clusterWide := strimzi.Operator{Namespace: "strimzi-operator", Name: "a", Version: "1.1.0", Scope: strimzi.ScopeCluster, Watches: []string{"*"}, Window: window}
	namespaced := strimzi.Operator{Namespace: "kafka-x", Name: "b", Version: "1.0.1", Scope: strimzi.ScopeNamespaces, Watches: []string{"kafka-x"}, Window: window}
	primaryNS := strimzi.Operator{Namespace: "strimzi-operator", Name: "c", Version: "1.1.0", Scope: strimzi.ScopeNamespaces, Watches: []string{"kafka", "connect"}, Window: window}

	if op, err := pickPrimaryOperator([]strimzi.Operator{clusterWide}, "kafka"); err != nil || op.Name != "a" {
		t.Fatalf("cluster-wide: %v %v", op, err)
	}
	if op, err := pickPrimaryOperator([]strimzi.Operator{namespaced, primaryNS}, "kafka"); err != nil || op.Name != "c" {
		t.Fatalf("namespace scope: %v %v", op, err)
	}
	if _, err := pickPrimaryOperator([]strimzi.Operator{clusterWide, namespaced}, "kafka"); err == nil || !strings.Contains(err.Error(), "mixed") {
		t.Fatalf("mixed installation must be refused, got %v", err)
	}
	if _, err := pickPrimaryOperator([]strimzi.Operator{namespaced}, "kafka"); err == nil || !strings.Contains(err.Error(), "no Strimzi operator watches namespace kafka") {
		t.Fatalf("unwatched namespace must be refused, got %v", err)
	}
}
