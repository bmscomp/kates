package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/migrate"
)

// scriptUp scripts the calls up makes for the 2.8.2 lab after the
// prerequisites: the namespace, the two helm installs, the waits.
func scriptUp(f *fakeProc) {
	f.on("kubectl get namespace kafka-m282-430-src --ignore-not-found -o jsonpath={.metadata.name}", "")
	f.on("kubectl create namespace kafka-m282-430-src", "namespace/kafka-m282-430-src created")
	f.on("kubectl label namespace kafka-m282-430-src", "namespace/kafka-m282-430-src labeled")
	f.on("helm upgrade --install m282-430-src charts/legacy-kafka", "")
	f.on("helm test m282-430-src -n kafka-m282-430-src", "")
	f.on("helm upgrade --install mm2-m282-430 charts/mirror-maker2", "")
	f.on("kubectl -n kafka wait kafkamirrormaker2/mm2-m282-430-mirror-maker2 --for=condition=Ready", "condition met")
	f.on("kubectl -n kafka get kafkamirrormaker2 mm2-m282-430-mirror-maker2 -o json", mirrorCRJSON("mm2-m282-430-mirror-maker2", "RUNNING", "RUNNING"))
}

func TestMigrateUpPhasesAndHelmArgv(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	scriptPrerequisites(f)
	scriptUp(f)
	out := captureOutput(t, "table")

	flags := newPairFlags("2.8.2")
	flags.SkipBuild = true
	if err := runMigrateUp(nil, flags); err != nil {
		t.Fatalf("up: %v\n%s", err, out.String())
	}

	// The phases run in the scripts' order, and every step is a row.
	wantOrder := []string{
		"kubectl cluster-info",
		"kubectl get crd kafkamirrormaker2s.kafka.strimzi.io -o name",
		"kubectl -n kafka wait kafka/krafter --for=condition=Ready --timeout=300s",
		"kubectl -n kafka get secret kates-mm2 --ignore-not-found",
		"kubectl create namespace kafka-m282-430-src",
		"kubectl label namespace kafka-m282-430-src --overwrite kates.io/lab=m282-430 kates.io/lab-role=source",
		"helm upgrade --install m282-430-src charts/legacy-kafka -n kafka-m282-430-src -f .build/migrate/m282-430/source-values.yaml --wait --timeout 600s",
		"helm test m282-430-src -n kafka-m282-430-src --timeout 600s",
		"helm upgrade --install mm2-m282-430 charts/mirror-maker2 -n kafka -f charts/mirror-maker2/values-kind.yaml -f charts/mirror-maker2/values-migrate-2x.yaml -f .build/migrate/m282-430/mirror-values.yaml --timeout 10m",
		"kubectl -n kafka wait kafkamirrormaker2/mm2-m282-430-mirror-maker2 --for=condition=Ready --timeout=600s",
		"kubectl -n kafka get kafkamirrormaker2 mm2-m282-430-mirror-maker2 -o json",
	}
	last := f.indexOf("kubectl get daemonset kindnet") // discovery ends here; the phases follow
	for _, prefix := range wantOrder {
		i := f.indexAfter(prefix, last)
		if i < 0 {
			t.Fatalf("command %q was not run after position %d; ran:\n  %s", prefix, last, strings.Join(f.lines(), "\n  "))
		}
		last = i
	}
	// --skip-build: no docker, no kind load.
	for _, l := range f.lines() {
		if strings.HasPrefix(l, "docker") || strings.HasPrefix(l, "kind") {
			t.Fatalf("--skip-build must not build: %q", l)
		}
	}

	// The generated values carry the lab's labels and the era.
	source, err := os.ReadFile(".build/migrate/m282-430/source-values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kates.io/lab: m282-430", "kates.io/lab-role: source", "mode: zookeeper", `version: 2.8.2`, "image: ghcr.io/bmscomp/kates-legacy-kafka:2.8.2", "name: kates.orders"} {
		if !strings.Contains(string(source), want) {
			t.Errorf("source values lack %q:\n%s", want, source)
		}
	}
	mirror, err := os.ReadFile(".build/migrate/m282-430/mirror-values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kates.io/lab-role: mirror", "mode: identity", "minSourceVersion: 2.1.0", "- kates.orders", "groupId: mm2-m282-430", "bootstrapServers: m282-430-src-legacy-kafka-bootstrap.kafka-m282-430-src.svc.cluster.local:9092", "kafkaVersion: 2.8.2", "strimziOperatorNamespace: strimzi-operator"} {
		if !strings.Contains(string(mirror), want) {
			t.Errorf("mirror values lack %q:\n%s", want, mirror)
		}
	}

	text := stripAnsi(out.String())
	for _, row := range []string{
		migrate.RowClusterReachable, migrate.RowStrimziCRDs, migrate.RowTargetKafkaReady, migrate.RowTargetCredentials,
		migrate.RowSourceDeployed, migrate.RowSourceServing, migrate.RowMirrorInstalled, migrate.RowCRReady, migrate.RowConnectorsRunning,
	} {
		if !strings.Contains(text, row) {
			t.Errorf("report lacks row %q:\n%s", row, text)
		}
	}
	if !strings.Contains(text, "9 passed, 0 failed") {
		t.Errorf("want 9 passed rows:\n%s", text)
	}
}

// scriptUpFanIn scripts the two-source lab m282-391-430.
func scriptUpFanIn(f *fakeProc) {
	const lab = "m282-391-430"
	for _, alias := range []string{"src282", "src391"} {
		ns := "kafka-" + lab + "-" + alias
		f.on("kubectl get namespace "+ns+" --ignore-not-found -o jsonpath={.metadata.name}", "")
		f.on("kubectl create namespace "+ns, "namespace/"+ns+" created")
		f.on("kubectl label namespace "+ns, "labeled")
		f.on("helm upgrade --install "+lab+"-"+alias+" charts/legacy-kafka", "")
		f.on("helm test "+lab+"-"+alias+" -n "+ns, "")
	}
	f.on("helm upgrade --install mm2-"+lab+" charts/mirror-maker2", "")
	f.on("kubectl -n kafka wait kafkamirrormaker2/mm2-"+lab+"-mirror-maker2 --for=condition=Ready", "condition met")
	f.on("kubectl -n kafka get kafkamirrormaker2 mm2-"+lab+"-mirror-maker2 -o json", mirrorCRJSON("mm2-"+lab+"-mirror-maker2", "RUNNING", "RUNNING"))
}

func TestMigrateUpFanIn(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	scriptPrerequisites(f)
	scriptUpFanIn(f)
	out := captureOutput(t, "table")

	flags := newPairFlags("2.8.2", "3.9.1")
	flags.SkipBuild = true
	if err := runMigrateUp(nil, flags); err != nil {
		t.Fatalf("up: %v\n%s", err, out.String())
	}
	// One namespace, one release and one Helm test per source, then the one
	// mirror release.
	for _, want := range []string{
		"kubectl label namespace kafka-m282-391-430-src282 --overwrite kates.io/lab=m282-391-430 kates.io/lab-role=source kates.io/lab-source=src282",
		"kubectl label namespace kafka-m282-391-430-src391 --overwrite kates.io/lab=m282-391-430 kates.io/lab-role=source kates.io/lab-source=src391",
		"helm upgrade --install m282-391-430-src282 charts/legacy-kafka -n kafka-m282-391-430-src282 -f .build/migrate/m282-391-430/source-values-src282.yaml --wait --timeout 600s",
		"helm upgrade --install m282-391-430-src391 charts/legacy-kafka -n kafka-m282-391-430-src391 -f .build/migrate/m282-391-430/source-values-src391.yaml --wait --timeout 600s",
		"helm upgrade --install mm2-m282-391-430 charts/mirror-maker2 -n kafka -f charts/mirror-maker2/values-kind.yaml -f charts/mirror-maker2/values-migrate-2x.yaml -f .build/migrate/m282-391-430/mirror-values.yaml --timeout 10m",
	} {
		if !f.hasLine(want) {
			t.Errorf("up did not run %q; ran:\n  %s", want, strings.Join(f.lines(), "\n  "))
		}
	}
	if n := len(f.find("helm upgrade --install mm2-")); n != 1 {
		t.Errorf("want one mirror release for both sources, got %d", n)
	}
	// Each source's values carry its own era, image and corpus topic.
	for _, tc := range []struct{ alias, mode, image, topic string }{
		{"src282", "zookeeper", "ghcr.io/bmscomp/kates-legacy-kafka:2.8.2", "kates.orders.src282"},
		{"src391", "kraft", "apache/kafka:3.9.1", "kates.orders.src391"},
	} {
		data, err := os.ReadFile(".build/migrate/m282-391-430/source-values-" + tc.alias + ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"mode: " + tc.mode, "image: " + tc.image, "name: " + tc.topic, "kates.io/lab-source: " + tc.alias} {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s values lack %q:\n%s", tc.alias, want, data)
			}
		}
	}
	// One mirror per source in the one release, each with its own topics.
	mirror, err := os.ReadFile(".build/migrate/m282-391-430/mirror-values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"alias: src282", "alias: src391", "- kates.orders.src282", "- kates.orders.src391",
		"kafkaVersion: 2.8.2", "kafkaVersion: 3.9.1", "groupId: mm2-m282-391-430",
	} {
		if !strings.Contains(string(mirror), want) {
			t.Errorf("mirror values lack %q:\n%s", want, mirror)
		}
	}
	// Every source is its own leg of the report.
	text := stripAnsi(out.String())
	for _, row := range []string{
		migrate.RowSourceDeployed + " [src282]", migrate.RowSourceServing + " [src282]",
		migrate.RowSourceDeployed + " [src391]", migrate.RowSourceServing + " [src391]",
	} {
		if !strings.Contains(text, row) {
			t.Errorf("report lacks row %q:\n%s", row, text)
		}
	}
	if !strings.Contains(text, "11 passed, 0 failed") {
		t.Errorf("want 11 passed rows (4 prerequisites, 2 per source, 3 mirror):\n%s", text)
	}
}

func TestMigrateUpNotOnKindSkipsTheKindOverlay(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, false)
	scriptPrerequisites(f)
	scriptUp(f)
	captureOutput(t, "table")

	flags := newPairFlags("2.8.2")
	flags.SkipBuild = true
	flags.ValuesMirror = "my-mirror.yaml"
	flags.ValuesSource = "my-source.yaml"
	if err := runMigrateUp(nil, flags); err != nil {
		t.Fatalf("up: %v", err)
	}
	if !f.hasLine("helm upgrade --install mm2-m282-430 charts/mirror-maker2 -n kafka -f charts/mirror-maker2/values-migrate-2x.yaml -f .build/migrate/m282-430/mirror-values.yaml -f my-mirror.yaml --timeout 10m") {
		t.Fatalf("mirror install argv:\n  %s", strings.Join(f.lines(), "\n  "))
	}
	if !f.hasLine("helm upgrade --install m282-430-src charts/legacy-kafka -n kafka-m282-430-src -f .build/migrate/m282-430/source-values.yaml -f my-source.yaml --wait --timeout 600s") {
		t.Fatalf("source install argv:\n  %s", strings.Join(f.lines(), "\n  "))
	}
}

func TestMigrateUpSASLWritesTheCredentialOnStdin(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	scriptPrerequisites(f)
	scriptUp(f)
	f.on("kubectl -n kafka apply -f -", "secret/m282-430-src-mm2reader created")
	captureOutput(t, "table")

	flags := newPairFlags("2.8.2")
	flags.SkipBuild = true
	flags.SASL = true
	if err := runMigrateUp(nil, flags); err != nil {
		t.Fatalf("up: %v", err)
	}
	applies := f.find("kubectl -n kafka apply -f -")
	if len(applies) != 1 {
		t.Fatalf("want one Secret apply, got %d", len(applies))
	}
	manifest := applies[0].stdin
	if !strings.Contains(manifest, "name: m282-430-src-mm2reader") || !strings.Contains(manifest, "password:") || !strings.Contains(manifest, "kates.io/lab: m282-430") {
		t.Fatalf("secret manifest:\n%s", manifest)
	}
	source, _ := os.ReadFile(".build/migrate/m282-430/source-values.yaml")
	var password string
	for _, line := range strings.Split(string(source), "\n") {
		if strings.Contains(line, "password:") {
			password = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "password:"))
		}
	}
	if password == "" {
		t.Fatal("the source values carry no SASL password")
	}
	for _, l := range f.lines() {
		if strings.Contains(l, password) {
			t.Fatalf("the password reached a command line: %q", l)
		}
	}
	if !strings.Contains(string(source), "username: mm2reader") {
		t.Fatalf("source values lack the SASL user:\n%s", source)
	}
	if !f.hasLine("helm upgrade --install mm2-m282-430") {
		t.Fatal("the mirror was not installed")
	}
	mirror, _ := os.ReadFile(".build/migrate/m282-430/mirror-values.yaml")
	if !strings.Contains(string(mirror), "secretName: m282-430-src-mm2reader") || !strings.Contains(string(mirror), "type: plain") || !strings.Contains(string(mirror), ":9094") {
		t.Fatalf("mirror values do not point at the SASL listener:\n%s", mirror)
	}
}

func TestMigrateUpFailsFastOnPrerequisites(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	f.on("kubectl cluster-info", "ok")
	f.on("kubectl config current-context", "kind-panda")
	f.fail("kubectl get crd kafkamirrormaker2s.kafka.strimzi.io -o name", `error: the server doesn't have a resource type "crd"`)
	out := captureOutput(t, "table")

	flags := newPairFlags("2.8.2")
	flags.SkipBuild = true
	err := runMigrateUp(nil, flags)
	if err == nil {
		t.Fatal("want a failure")
	}
	if f.hasLine("helm upgrade") || f.hasLine("kubectl create namespace") {
		t.Fatalf("nothing may be created after a failed prerequisite:\n  %s", strings.Join(f.lines(), "\n  "))
	}
	text := stripAnsi(out.String())
	if !strings.Contains(text, "FAIL") || !strings.Contains(text, migrate.RowStrimziCRDs) {
		t.Fatalf("report:\n%s", text)
	}
}

func TestMigrateUpRefusesOverlappingLab(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	scriptPrerequisites(f)
	f.on("kubectl -n kafka get kafkamirrormaker2 -l kates.io/lab -o json", `{"items":[{"metadata":{"name":"mm2-other-mirror-maker2","labels":{"kates.io/lab":"other"}},
	  "spec":{"mirrors":[{"topicsPattern":"kates\\.orders|kates\\.payments"}]}}]}`)
	captureOutput(t, "table")

	flags := newPairFlags("2.8.2")
	flags.SkipBuild = true
	err := runMigrateUp(nil, flags)
	if err == nil || !strings.Contains(err.Error(), "--topics") || !strings.Contains(err.Error(), "lab other") {
		t.Fatalf("want the overlap refusal naming --topics, got %v", err)
	}
	if f.hasLine("kubectl create namespace") {
		t.Fatal("nothing may be created after the refusal")
	}
}

func TestMigrateUpBuildsTheImageForABuiltLine(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	scriptPrerequisites(f)
	scriptUp(f)
	f.on("docker --version", "Docker version 27")
	f.on("docker build", "")
	f.on("docker run --rm --entrypoint ls ghcr.io/bmscomp/kates-legacy-kafka:2.8.2 /opt/kafka/libs/", "connect-api-2.8.2.jar\nkafka_2.13-2.8.2.jar\nkafka_2.13-2.8.2-sources.jar")
	f.on("kind get clusters", "panda")
	f.on("kind load docker-image ghcr.io/bmscomp/kates-legacy-kafka:2.8.2 --name panda", "")
	captureOutput(t, "table")

	if err := runMigrateUp(nil, newPairFlags("2.8.2")); err != nil {
		t.Fatalf("up: %v", err)
	}
	build := f.find("docker build")
	if len(build) != 1 {
		t.Fatalf("want one docker build, got %d", len(build))
	}
	line := build[0].line()
	for _, want := range []string{"--build-arg KAFKA_VERSION=2.8.2", "--build-arg SCALA_VERSION=2.13", "--build-arg JRE_VERSION=11", "--build-arg UBUNTU_RELEASE=jammy", "-f Dockerfile.legacy-kafka", "-t ghcr.io/bmscomp/kates-legacy-kafka:2.8.2"} {
		if !strings.Contains(line, want) {
			t.Errorf("docker build lacks %q: %s", want, line)
		}
	}
	if f.indexOf("kind load docker-image") > f.indexOf("helm upgrade --install m282-430-src") {
		t.Fatal("the image must be loaded before the source is installed")
	}
}
