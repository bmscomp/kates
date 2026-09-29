package cmd

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/strimzi"
)

// ── fixtures ────────────────────────────────────────────────────────────────

// writeOperatorTgz builds a minimal strimzi-kafka-operator chart tarball with
// the given version, Kafka window and CRD API versions — the four files
// strimzi.ReadChart reads.
func writeOperatorTgz(t *testing.T, path, version string, window []string, served []string, stored string) {
	t.Helper()
	var imgs strings.Builder
	imgs.WriteString("{{- define \"strimzi.kafka.image.map\" }}\n            - name: STRIMZI_KAFKA_IMAGES\n              value: |                 \n")
	for _, v := range window {
		fmt.Fprintf(&imgs, "                %s={{ template \"strimzi.image\" (merge . (dict \"key\" \"kafka\" \"tagSuffix\" \"-kafka-%s\")) }}\n", v, v)
	}
	imgs.WriteString("            - name: STRIMZI_KAFKA_CONNECT_IMAGES\n              value: |                 \n")
	for _, v := range window {
		fmt.Fprintf(&imgs, "                %s={{ template \"strimzi.image\" (merge . (dict \"key\" \"kafkaConnect\" \"tagSuffix\" \"-kafka-%s\")) }}\n", v, v)
	}
	imgs.WriteString("{{- end -}}\n")

	var crd strings.Builder
	crd.WriteString("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: kafkas.kafka.strimzi.io\nspec:\n  group: kafka.strimzi.io\n  versions:\n")
	for _, s := range served {
		fmt.Fprintf(&crd, "    - name: %s\n      served: true\n      storage: %v\n", s, s == stored)
	}

	files := map[string]string{
		"strimzi-kafka-operator/Chart.yaml":                       fmt.Sprintf("apiVersion: v2\nname: strimzi-kafka-operator\nversion: %s\nappVersion: %s\n", version, version),
		"strimzi-kafka-operator/values.yaml":                      fmt.Sprintf("defaultImageRegistry: quay.io\ndefaultImageRepository: strimzi\ndefaultImageTag: %s\nwatchNamespaces: []\nwatchAnyNamespace: false\n", version),
		"strimzi-kafka-operator/templates/_kafka_image_map.tpl":   imgs.String(),
		"strimzi-kafka-operator/crds/040-Crd-kafka.yaml":          crd.String(),
		"strimzi-kafka-operator/templates/060-Deployment-co.yaml": "apiVersion: apps/v1\nkind: Deployment\n",
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

// versionsRepo lays out the two charts resolveVersionPlan reads, with the
// pinned operator tarball already fetched, and returns the repo root.
func versionsRepo(t *testing.T, pin, floor string) string {
	t.Helper()
	root := t.TempDir()
	op := filepath.Join(root, operatorChartDir)
	if err := os.MkdirAll(filepath.Join(op, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	chartYAML := fmt.Sprintf(`apiVersion: v2
name: strimzi-operator
version: 0.2.0
# Tracks the Strimzi operator version this chart deploys.
appVersion: "%s"
dependencies:
  - name: strimzi-kafka-operator
    version: %s
    repository: oci://quay.io/strimzi-helm
`, pin, pin)
	os.WriteFile(filepath.Join(op, "Chart.yaml"), []byte(chartYAML), 0o644)
	os.WriteFile(filepath.Join(op, "values.yaml"), []byte("strimziVersion: \""+pin+"\"\n"), 0o644)
	os.WriteFile(filepath.Join(op, "templates", "NOTES.txt"), []byte("notes\n"), 0o644)
	writeOperatorTgz(t, filepath.Join(op, "charts", "strimzi-kafka-operator-"+pin+".tgz"), pin,
		[]string{"4.2.0", "4.2.1", "4.3.0"}, []string{"v1"}, "v1")

	kc := filepath.Join(root, kafkaChartDir)
	os.MkdirAll(kc, 0o755)
	os.WriteFile(filepath.Join(kc, "Chart.yaml"), []byte(fmt.Sprintf("apiVersion: v2\nname: kafka-cluster\nversion: 0.4.0\nannotations:\n  kates.io/kafka-floor: \"%s\"\n", floor)), 0o644)
	return root
}

// scriptedRunner answers kubectl/helm from a table keyed on a substring of
// the joined argv; anything else errors like an unreachable cluster.
type scriptedRunner struct {
	answers map[string]string
	calls   []string
}

func (s *scriptedRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	joined := name + " " + strings.Join(args, " ")
	s.calls = append(s.calls, joined)
	for k, v := range s.answers {
		if strings.Contains(joined, k) {
			return v, nil
		}
	}
	return "", fmt.Errorf("%s: connection refused", joined)
}

func operatorPodItem(ns string) string {
	return fmt.Sprintf(`{"metadata":{"namespace":%q,"labels":{"pod-template-hash":"abc"},"ownerReferences":[{"kind":"ReplicaSet","name":"strimzi-cluster-operator-abc"}]}}`, ns)
}

func operatorPodsList(namespaces ...string) string {
	items := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		items = append(items, operatorPodItem(ns))
	}
	return `{"items":[` + strings.Join(items, ",") + `]}`
}

func operatorPodsJSON(ns, version, watches string) (pods, deployment string) {
	pods = operatorPodsList(ns)
	var imgs strings.Builder
	var window []string
	switch version {
	case "1.0.1", "1.0.0":
		window = []string{"4.1.0", "4.1.1", "4.1.2", "4.2.0"}
	case "1.2.0":
		window = []string{"4.3.0", "4.4.0"}
	default:
		window = []string{"4.2.0", "4.2.1", "4.3.0"}
	}
	for _, v := range window {
		fmt.Fprintf(&imgs, "%s=quay.io/strimzi/kafka:%s-kafka-%s\\n", v, version, v)
	}
	deployment = fmt.Sprintf(`{"metadata":{"name":"strimzi-cluster-operator","namespace":%q},"spec":{"template":{"spec":{"containers":[{"name":"strimzi-cluster-operator","image":"quay.io/strimzi/operator:%s","env":[{"name":"STRIMZI_NAMESPACE","value":%q},{"name":"STRIMZI_KAFKA_IMAGES","value":"%s"}]}]}}}}`, ns, version, watches, imgs.String())
	return pods, deployment
}

func kafkaListJSON(entries ...string) string {
	// entries are "ns/name/version"
	var items []string
	for _, e := range entries {
		p := strings.Split(e, "/")
		items = append(items, fmt.Sprintf(`{"metadata":{"name":%q,"namespace":%q},"spec":{"kafka":{"version":%q}}}`, p[1], p[0], p[2]))
	}
	return `{"items":[` + strings.Join(items, ",") + `]}`
}

// ── tests ───────────────────────────────────────────────────────────────────

func TestResolveVersionPlan_PinnedDefaults(t *testing.T) {
	root := versionsRepo(t, "1.1.0", "4.2.0")
	vp, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true, KafkaNS: "kafka"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !vp.Pinned || vp.StrimziVersion != "1.1.0" || vp.StrimziSource != "pinned" {
		t.Errorf("pin not recognised: %+v", vp)
	}
	if vp.KafkaVersion != "4.3.0" || !vp.KafkaDefaulted || vp.Metadata != "4.2" {
		t.Errorf("kafka default: got %s (defaulted %v) metadata %s", vp.KafkaVersion, vp.KafkaDefaulted, vp.Metadata)
	}
	if vp.Scope != scopeCluster || vp.OperatorAction != "install" || len(vp.Watch) != 0 {
		t.Errorf("scope/action: %+v", vp)
	}
	if strings.Join(vp.Window, " ") != "4.2.0 4.2.1 4.3.0" {
		t.Errorf("window: %v", vp.Window)
	}
	if vp.ChartDir != filepath.Join(root, operatorChartDir) {
		t.Errorf("chart dir: %s", vp.ChartDir)
	}
	if args := vp.operatorHelmArgs(); len(args) != 0 {
		t.Errorf("pinned cluster-scope install needs no version/scope args, got %v", args)
	}
	kargs := strings.Join(vp.kafkaHelmArgs(primaryCluster{Name: "krafter", Namespace: "kafka"}), " ")
	for _, want := range []string{"clusterName=krafter", "kafkaVersion=4.3.0", "kafka.metadataVersion=4.2", "strimziVersion=1.1.0"} {
		if !strings.Contains(kargs, want) {
			t.Errorf("kafka args miss %s: %s", want, kargs)
		}
	}
	if c := strings.Join(vp.connectHelmArgs(), " "); !strings.Contains(c, "version=4.3.0") {
		t.Errorf("connect args: %s", c)
	}
}

func TestResolveVersionPlan_ExplicitKafkaVersion(t *testing.T) {
	root := versionsRepo(t, "1.1.0", "4.2.0")
	vp, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true, KafkaVersion: "4.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	if vp.KafkaVersion != "4.2.1" || vp.KafkaDefaulted || vp.Metadata != "4.2" {
		t.Errorf("got %s defaulted=%v metadata=%s", vp.KafkaVersion, vp.KafkaDefaulted, vp.Metadata)
	}
	if vp2, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true, KafkaVersion: "latest"}); err != nil || vp2.KafkaVersion != "4.3.0" || !vp2.KafkaDefaulted {
		t.Errorf("latest should equal the default: %+v %v", vp2, err)
	}
}

func TestResolveVersionPlan_UnsupportedKafkaRefused(t *testing.T) {
	root := versionsRepo(t, "1.1.0", "4.2.0")
	_, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true, KafkaVersion: "4.1.2", Scope: scopeCluster})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"Kafka 4.1.2 cannot run under Strimzi 1.1.0", "4.2.0 4.2.1 4.3.0", "cluster-wide operator", "kates versions strimzi", "kates migrate up --from 4.1.2", "--kafka-version 4.3.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal misses %q:\n%s", want, err)
		}
	}
	if _, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true, KafkaVersion: "4.x"}); err == nil || !strings.Contains(err.Error(), "expected x.y.z") {
		t.Errorf("bad version string: %v", err)
	}
}

func TestResolveVersionPlan_FloorRefusesPrimary(t *testing.T) {
	root := versionsRepo(t, "1.1.0", "4.3.0")
	_, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true, KafkaVersion: "4.2.0"})
	if err == nil || !strings.Contains(err.Error(), "below this platform's floor 4.3.0") {
		t.Errorf("floor: %v", err)
	}
}

func TestResolveVersionPlan_NamespaceScopeWatchList(t *testing.T) {
	root := versionsRepo(t, "1.1.0", "4.2.0")
	vp, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true,
		Scope: scopeNamespace, KafkaNS: "kafka", ConnectNS: "connect", WithConnect: true, Topology: "isolated"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(vp.Watch, ",") != "kafka,connect" {
		t.Errorf("watch list: %v", vp.Watch)
	}
	args := strings.Join(vp.operatorHelmArgs(), " ")
	if !strings.Contains(args, "watchAnyNamespace=false") || !strings.Contains(args, "watchNamespaces={kafka,connect}") {
		t.Errorf("scope args: %s", args)
	}
	single, _ := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true,
		Scope: scopeNamespace, Topology: "single", SingleNS: "kates-stack"})
	if strings.Join(single.Watch, ",") != "kates-stack" {
		t.Errorf("single topology watch: %v", single.Watch)
	}
	if _, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, Offline: true, Scope: "global"}); err == nil {
		t.Error("bad scope accepted")
	}
}

func TestResolveVersionPlan_CachedOtherVersion(t *testing.T) {
	root := versionsRepo(t, "1.1.0", "4.2.0")
	cache := t.TempDir()
	writeOperatorTgz(t, filepath.Join(cache, strimzi.ChartFileName("1.0.1")), "1.0.1",
		[]string{"4.1.0", "4.1.1", "4.1.2", "4.2.0"}, []string{"v1"}, "v1")
	vp, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: cache, Offline: true, StrimziVersion: "1.0.1"})
	if err != nil {
		t.Fatalf("resolve 1.0.1 from cache: %v", err)
	}
	if vp.Pinned || vp.StrimziSource != "cache" || vp.StrimziVersion != "1.0.1" {
		t.Errorf("source: %+v", vp)
	}
	if vp.KafkaVersion != "4.2.0" || vp.Metadata != "4.1" {
		t.Errorf("newest of 1.0.1 window: %s / %s", vp.KafkaVersion, vp.Metadata)
	}
	if !strings.HasPrefix(vp.ChartDir, cache) {
		t.Errorf("non-pinned install must come from the generated wrapper, got %s", vp.ChartDir)
	}
	if _, err := os.Stat(filepath.Join(vp.ChartDir, "charts", strimzi.ChartFileName("1.0.1"))); err != nil {
		t.Errorf("wrapper has no tarball: %v", err)
	}
	if args := strings.Join(vp.operatorHelmArgs(), " "); !strings.Contains(args, "strimziVersion=1.0.1") {
		t.Errorf("non-pinned install must set strimziVersion for the CRD hook: %s", args)
	}
	// Kafka 4.3.0 is outside 1.0.1's window.
	if _, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: cache, Offline: true, StrimziVersion: "1.0.1", KafkaVersion: "4.3.0"}); err == nil || !strings.Contains(err.Error(), "4.1.0 4.1.1 4.1.2 4.2.0") {
		t.Errorf("out-of-window for 1.0.1: %v", err)
	}
	// Not cached and offline → a clear error, no pull attempted.
	if _, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: cache, Offline: true, StrimziVersion: "1.0.0"}); err == nil || !strings.Contains(err.Error(), "not in the cache") {
		t.Errorf("offline miss: %v", err)
	}
}

func TestResolveVersionPlan_BelowFloorOperatorRefused(t *testing.T) {
	root := versionsRepo(t, "1.1.0", "4.2.0")
	cache := t.TempDir()
	writeOperatorTgz(t, filepath.Join(cache, strimzi.ChartFileName("0.50.1")), "0.50.1",
		[]string{"4.0.0", "4.0.1", "4.1.0", "4.1.1"}, []string{"v1beta2", "v1"}, "v1beta2")
	writeOperatorTgz(t, filepath.Join(cache, strimzi.ChartFileName("0.48.0")), "0.48.0",
		[]string{"4.0.0", "4.1.0"}, []string{"v1beta2"}, "v1beta2")
	if _, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: cache, Offline: true, StrimziVersion: "0.50.1"}); err == nil || !strings.Contains(err.Error(), "no Kafka >= 4.2.0") {
		t.Errorf("0.50.1: %v", err)
	}
	if _, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: cache, Offline: true, StrimziVersion: "0.48.0"}); err == nil || !strings.Contains(err.Error(), "v1") {
		t.Errorf("0.48.0: %v", err)
	}
}

func TestResolveVersionPlan_NewerThanPinWarns(t *testing.T) {
	root := versionsRepo(t, "1.1.0", "4.2.0")
	cache := t.TempDir()
	writeOperatorTgz(t, filepath.Join(cache, strimzi.ChartFileName("1.2.0")), "1.2.0", []string{"4.3.0", "4.4.0"}, []string{"v1"}, "v1")
	vp, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: cache, Offline: true, StrimziVersion: "1.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vp.Notes) == 0 || !strings.Contains(vp.Notes[0], "newer than the version this platform release was tested with (1.1.0)") {
		t.Errorf("notes: %v", vp.Notes)
	}
	if vp.KafkaVersion != "4.4.0" || vp.Metadata != "4.3" {
		t.Errorf("1.2.0 defaults: %s %s", vp.KafkaVersion, vp.Metadata)
	}
}

func TestResolveVersionPlan_InstalledSameAndUpgrade(t *testing.T) {
	root := versionsRepo(t, "1.1.0", "4.2.0")
	pods, dep := operatorPodsJSON("strimzi-operator", "1.1.0", "*")
	r := &scriptedRunner{answers: map[string]string{
		"get pods -A -l strimzi.io/kind=cluster-operator": pods,
		"get deployment -n strimzi-operator":              dep,
		"helm list -A -o json":                            `[{"name":"strimzi-operator","namespace":"strimzi-operator","chart":"strimzi-operator-0.2.0","app_version":"1.1.0"}]`,
		"get kafka -A -o json":                            kafkaListJSON("kafka/krafter/4.3.0"),
	}}
	vp, err := resolveVersionPlan(context.Background(), r, versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true, KafkaNS: "kafka"})
	if err != nil {
		t.Fatal(err)
	}
	if vp.OperatorAction != "same" || vp.Installed != "1.1.0" {
		t.Errorf("same version: %+v", vp)
	}

	// Installed 1.0.1 running 4.2.0 → upgrade to the pin is allowed and rolls krafter.
	pods, dep = operatorPodsJSON("strimzi-operator", "1.0.1", "*")
	r.answers["get pods -A -l strimzi.io/kind=cluster-operator"] = pods
	r.answers["get deployment -n strimzi-operator"] = dep
	r.answers["get kafka -A -o json"] = kafkaListJSON("kafka/krafter/4.2.0", "other/legacy/4.1.2")
	// other/legacy is watched too (cluster-wide) and 4.1.2 is outside 1.1.0's window.
	_, err = resolveVersionPlan(context.Background(), r, versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true, KafkaNS: "kafka"})
	if err == nil || !strings.Contains(err.Error(), "other/legacy runs Kafka 4.1.2") {
		t.Errorf("running out-of-window must refuse: %v", err)
	}
	r.answers["get kafka -A -o json"] = kafkaListJSON("kafka/krafter/4.2.0")
	vp, err = resolveVersionPlan(context.Background(), r, versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true, KafkaNS: "kafka"})
	if err != nil {
		t.Fatal(err)
	}
	if vp.OperatorAction != "upgrade" || vp.Installed != "1.0.1" || len(vp.RollingCRs) != 1 || !strings.Contains(vp.RollingCRs[0], "kafka/krafter (4.2.0)") {
		t.Errorf("upgrade: %+v", vp)
	}
}

// A cluster running a newer operator than the checkout's pin is an ordinary
// state, and `kates deploy` with no version flag must keep it. It used to
// propose the pin and then refuse its own proposal as a downgrade, which made
// the platform undeployable against any cluster that had moved ahead — the
// user saw "Strimzi 1.2.0 is installed and 1.1.0 was requested" without ever
// having requested 1.1.0.
func TestResolveVersionPlan_KeepsNewerInstalled(t *testing.T) {
	root := versionsRepo(t, "1.1.0", "4.2.0")
	cache := t.TempDir()
	// The adopted operator's window is deliberately not the pin's: the Kafka
	// version must come from the operator that will actually reconcile it.
	writeOperatorTgz(t, filepath.Join(cache, strimzi.ChartFileName("1.2.0")), "1.2.0",
		[]string{"4.3.0", "4.4.0"}, []string{"v1"}, "v1")

	pods, dep := operatorPodsJSON("strimzi-operator", "1.2.0", "*")
	r := &scriptedRunner{answers: map[string]string{
		"get pods -A -l strimzi.io/kind=cluster-operator": pods,
		"get deployment -n strimzi-operator":              dep,
		"helm list -A -o json":                            `[]`,
		"get kafka -A -o json":                            kafkaListJSON("kafka/krafter/4.3.0"),
	}}

	vp, err := resolveVersionPlan(context.Background(), r, versionOptions{RepoRoot: root, CacheDir: cache, Offline: true, KafkaNS: "kafka"})
	if err != nil {
		t.Fatalf("a newer installed operator must not be a refusal: %v", err)
	}
	if vp.StrimziVersion != "1.2.0" || !vp.Adopted || vp.Pinned {
		t.Errorf("should have kept the installed operator: %+v", vp)
	}
	if vp.OperatorAction != "same" {
		t.Errorf("nothing to upgrade, got action %q", vp.OperatorAction)
	}
	if vp.StrimziSource != "installed" {
		t.Errorf("source should say where the version came from, got %q", vp.StrimziSource)
	}
	// The window, and so the default Kafka, is the adopted operator's.
	if vp.KafkaVersion != "4.4.0" || strings.Join(vp.Window, " ") != "4.3.0 4.4.0" {
		t.Errorf("window must come from the installed operator: %s / %v", vp.KafkaVersion, vp.Window)
	}
	if !hasNote(vp, "already installed and newer") {
		t.Errorf("the choice must be explained in the notes: %v", vp.Notes)
	}

	// An explicit request still wins — including one that is refused.
	if _, err := resolveVersionPlan(context.Background(), r, versionOptions{RepoRoot: root, CacheDir: cache, Offline: true, KafkaNS: "kafka", StrimziVersion: "1.1.0"}); err == nil || !strings.Contains(err.Error(), "does not support downgrading") {
		t.Errorf("an explicit older version is still a downgrade: %v", err)
	}

	// An older installed operator is an upgrade to the pin, not an adoption.
	pods, dep = operatorPodsJSON("strimzi-operator", "1.0.1", "*")
	r.answers["get pods -A -l strimzi.io/kind=cluster-operator"] = pods
	r.answers["get deployment -n strimzi-operator"] = dep
	r.answers["get kafka -A -o json"] = kafkaListJSON()
	vp, err = resolveVersionPlan(context.Background(), r, versionOptions{RepoRoot: root, CacheDir: cache, Offline: true, KafkaNS: "kafka"})
	if err != nil {
		t.Fatal(err)
	}
	if vp.Adopted || vp.StrimziVersion != "1.1.0" || vp.OperatorAction != "upgrade" {
		t.Errorf("older installed must upgrade to the pin: %+v", vp)
	}
}

func hasNote(vp *versionPlan, substr string) bool {
	for _, n := range vp.Notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

func TestResolveVersionPlan_DowngradeMixedAndScope(t *testing.T) {
	root := versionsRepo(t, "1.1.0", "4.2.0")
	cache := t.TempDir()
	pods, dep := operatorPodsJSON("strimzi-operator", "1.2.0", "*")
	r := &scriptedRunner{answers: map[string]string{
		"get pods -A -l strimzi.io/kind=cluster-operator": pods,
		"get deployment -n strimzi-operator":              dep,
		"helm list -A -o json":                            `[]`,
		"get kafka -A -o json":                            kafkaListJSON(),
	}}
	// Asking for the older version BY NAME is the downgrade Strimzi refuses.
	// (Asking for nothing is not: see TestResolveVersionPlan_KeepsNewerInstalled.)
	if _, err := resolveVersionPlan(context.Background(), r, versionOptions{RepoRoot: root, CacheDir: cache, Offline: true, KafkaNS: "kafka", StrimziVersion: "1.1.0"}); err == nil || !strings.Contains(err.Error(), "does not support downgrading") {
		t.Errorf("downgrade: %v", err)
	}

	// A namespaced primary plus an additional operator: switching to cluster scope is refused.
	_, depA := operatorPodsJSON("strimzi-operator", "1.1.0", "kafka,connect")
	_, depB := operatorPodsJSON("kafka-legacy41", "1.0.1", "kafka-legacy41")
	r.answers["get pods -A -l strimzi.io/kind=cluster-operator"] = operatorPodsList("strimzi-operator", "kafka-legacy41")
	r.answers["get deployment -n strimzi-operator"] = depA
	r.answers["get deployment -n kafka-legacy41"] = depB
	if _, err := resolveVersionPlan(context.Background(), r, versionOptions{RepoRoot: root, CacheDir: cache, Offline: true, KafkaNS: "kafka", Scope: scopeCluster}); err == nil || !strings.Contains(err.Error(), "cannot switch to a cluster-wide operator while an additional operator runs in kafka-legacy41") {
		t.Errorf("namespace→cluster with additional: %v", err)
	}
	vp, err := resolveVersionPlan(context.Background(), r, versionOptions{RepoRoot: root, CacheDir: cache, Offline: true, KafkaNS: "kafka", Scope: scopeNamespace, ConnectNS: "connect", WithConnect: true, Topology: "isolated"})
	if err != nil {
		t.Fatalf("namespace scope with matching install: %v", err)
	}
	if vp.OperatorAction != "same" || vp.ScopeChange != "" {
		t.Errorf("expected same/no scope change: %+v", vp)
	}

	// Cluster-wide installed, namespace requested → a scope change, confirmed later.
	_, depC := operatorPodsJSON("strimzi-operator", "1.1.0", "*")
	r.answers["get pods -A -l strimzi.io/kind=cluster-operator"] = operatorPodsList("strimzi-operator")
	r.answers["get deployment -n strimzi-operator"] = depC
	vp, err = resolveVersionPlan(context.Background(), r, versionOptions{RepoRoot: root, CacheDir: cache, Offline: true, KafkaNS: "kafka", Scope: scopeNamespace, Topology: "isolated"})
	if err != nil {
		t.Fatal(err)
	}
	if vp.ScopeChange != "cluster → namespace" {
		t.Errorf("scope change: %q", vp.ScopeChange)
	}

	// Mixed shape: a cluster-wide and a namespaced operator together.
	r.answers["get pods -A -l strimzi.io/kind=cluster-operator"] = operatorPodsList("strimzi-operator", "kafka-legacy41")
	if _, err := resolveVersionPlan(context.Background(), r, versionOptions{RepoRoot: root, CacheDir: cache, Offline: true, KafkaNS: "kafka"}); err == nil || !strings.Contains(err.Error(), "both a cluster-wide and namespace-scoped") {
		t.Errorf("mixed: %v", err)
	}
}

func TestResolveVersionPlan_OneZeroBoundary(t *testing.T) {
	root := versionsRepo(t, "1.1.0", "4.2.0")
	pods, dep := operatorPodsJSON("strimzi-operator", "0.51.0", "*")
	// 0.51.0's window (from the fake) is the 1.1.0 default; force the running Kafka into both windows.
	r := &scriptedRunner{answers: map[string]string{
		"get pods -A -l strimzi.io/kind=cluster-operator": pods,
		"get deployment -n strimzi-operator":              dep,
		"helm list -A -o json":                            `[]`,
		"get kafka -A -o json":                            kafkaListJSON("kafka/krafter/4.2.0"),
		"get crd -l app=strimzi -o json":                  `{"items":[{"metadata":{"name":"kafkas.kafka.strimzi.io"},"status":{"storedVersions":["v1beta2","v1"]}}]}`,
	}}
	_, err := resolveVersionPlan(context.Background(), r, versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true, KafkaNS: "kafka"})
	if err == nil || !strings.Contains(err.Error(), "still stores v1beta2") {
		t.Errorf("1.0 boundary must be refused until converted: %v", err)
	}
	r.answers["get crd -l app=strimzi -o json"] = `{"items":[{"metadata":{"name":"kafkas.kafka.strimzi.io"},"status":{"storedVersions":["v1"]}}]}`
	vp, err := resolveVersionPlan(context.Background(), r, versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true, KafkaNS: "kafka"})
	if err != nil || vp.OperatorAction != "upgrade" {
		t.Errorf("converted cluster may cross: %v %+v", err, vp)
	}
}

func TestVersionPlanDescribe(t *testing.T) {
	root := versionsRepo(t, "1.1.0", "4.2.0")
	vp, err := resolveVersionPlan(context.Background(), nil, versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	vp.describe(func(s string) { lines = append(lines, s) })
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"1.1.0 (pinned; cluster-wide)", "4.3.0 (newest supported by Strimzi 1.1.0", "window: 4.2.0 4.2.1 4.3.0", "→ install"} {
		if !strings.Contains(joined, want) {
			t.Errorf("describe misses %q:\n%s", want, joined)
		}
	}
}
