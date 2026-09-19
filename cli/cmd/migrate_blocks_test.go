package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The building blocks: image build, target, source and mirror.

func TestMigrateImageBuildLoad(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	f.on("docker --version", "Docker version 27")
	f.on("docker build", "")
	f.on("docker run --rm --entrypoint ls ghcr.io/bmscomp/kates-legacy-kafka:2.8.2 /opt/kafka/libs/", "kafka-clients-2.8.2.jar\nkafka_2.13-2.8.2.jar\nkafka_2.13-2.8.2-test.jar")
	f.on("kind get clusters", "panda")
	f.on("kind load docker-image ghcr.io/bmscomp/kates-legacy-kafka:2.8.2 --name panda", "")
	captureOutput(t, "table")

	opts := migrateImageOptions{Version: "2.8.2", Scala: "2.13", OSRelease: "jammy", Registry: "ghcr.io/bmscomp", Load: true, KindCluster: "panda"}
	if err := runMigrateImageBuild(nil, opts); err != nil {
		t.Fatalf("image build: %v", err)
	}
	want := "docker build --build-arg KAFKA_VERSION=2.8.2 --build-arg SCALA_VERSION=2.13 --build-arg JRE_VERSION=11 --build-arg UBUNTU_RELEASE=jammy -f Dockerfile.legacy-kafka -t ghcr.io/bmscomp/kates-legacy-kafka:2.8.2 ."
	if !f.hasLine(want) {
		t.Fatalf("docker build argv; ran:\n  %s", strings.Join(f.lines(), "\n  "))
	}
	if f.indexOf("docker run --rm --entrypoint ls") < f.indexOf("docker build") || f.indexOf("kind load docker-image") < f.indexOf("docker run --rm --entrypoint ls") {
		t.Fatal("build, then verify, then load")
	}
}

func TestMigrateImageBuildPushAndJRE(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	f.on("docker --version", "Docker version 27")
	f.on("docker buildx version", "github.com/docker/buildx v0.20")
	f.on("docker buildx build", "")
	f.on("docker pull --quiet ghcr.io/bmscomp/kates-legacy-kafka:3.5.2", "")
	f.on("docker run --rm --entrypoint ls ghcr.io/bmscomp/kates-legacy-kafka:3.5.2 /opt/kafka/libs/", "kafka_2.13-3.5.2.jar")
	captureOutput(t, "table")

	opts := migrateImageOptions{Version: "3.5.2", Platform: "linux/amd64,linux/arm64", Registry: "ghcr.io/bmscomp", Push: true}
	if err := runMigrateImageBuild(nil, opts); err != nil {
		t.Fatalf("image build --push: %v", err)
	}
	if !f.hasLine("docker buildx build --build-arg KAFKA_VERSION=3.5.2 --build-arg SCALA_VERSION=2.13 --build-arg JRE_VERSION=17 --build-arg UBUNTU_RELEASE=jammy -f Dockerfile.legacy-kafka -t ghcr.io/bmscomp/kates-legacy-kafka:3.5.2 --platform linux/amd64,linux/arm64 --push .") {
		t.Fatalf("buildx argv; ran:\n  %s", strings.Join(f.lines(), "\n  "))
	}
	if f.hasLine("kind") {
		t.Fatal("--push never loads into kind")
	}
}

func TestMigrateImageBuildRefusals(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	captureOutput(t, "table")

	err := runMigrateImageBuild(nil, migrateImageOptions{Version: "2.8.2", Load: true, Push: true})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("--load with --push must be refused, got %v", err)
	}
	if len(f.lines()) != 0 {
		t.Fatal("nothing runs before the flag check")
	}

	f.on("docker --version", "Docker version 27")
	f.on("docker build", "")
	f.on("docker run --rm --entrypoint ls ghcr.io/bmscomp/kates-legacy-kafka:2.8.2 /opt/kafka/libs/", "kafka_2.13-2.8.1.jar")
	err = runMigrateImageBuild(nil, migrateImageOptions{Version: "2.8.2"})
	if err == nil || !strings.Contains(err.Error(), `image reports Kafka "2.8.1", expected 2.8.2`) {
		t.Fatalf("a wrong jar version must fail the build, got %v", err)
	}
	if kafkaJarVersion("kafka-clients-2.8.2.jar\n") != "" {
		t.Fatal("a classifier jar must not satisfy the check")
	}
}

func TestMigrateTargetOffsetsAndTopics(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	f.on("kubectl -n kafka get pod kates-migrate-target --ignore-not-found -o jsonpath={.status.phase}", "")
	f.on("kubectl -n kafka apply -f -", "pod/kates-migrate-target created")
	f.on("kubectl -n kafka wait --for=condition=Ready pod/kates-migrate-target", "condition met")
	f.on("kubectl -n kafka get secret kates-mm2 -o jsonpath={.data.password}", "czNjcmV0") // s3cret
	f.handle("kubectl -n kafka exec -i kates-migrate-target -c client -- tee /tmp/client.properties", func(_ []string, stdin string) (string, error) { return stdin, nil })
	f.on("kubectl -n kafka exec kates-migrate-target -c client -- /opt/kafka/bin/kafka-get-offsets.sh --bootstrap-server krafter-kafka-bootstrap.kafka.svc.cluster.local:9092 --topic kates.orders --time -1 --command-config /tmp/client.properties",
		"kates.orders:0:67\nkates.orders:1:67\nkates.orders:2:66")
	f.on("kubectl -n kafka exec kates-migrate-target -c client -- /opt/kafka/bin/kafka-topics.sh --bootstrap-server krafter-kafka-bootstrap.kafka.svc.cluster.local:9092 --command-config /tmp/client.properties --list",
		"kates.orders\n__consumer_offsets\nkates-events")
	f.on("kubectl -n kafka delete pod kates-migrate-target --ignore-not-found", "")
	out := captureOutput(t, "table")

	opts := migrateTargetOptions{Namespace: "kafka", Cluster: "krafter", User: "kates-mm2", Partitions: true, Timeout: 300}
	if err := runMigrateTarget(nil, opts, "offsets", "kates.orders"); err != nil {
		t.Fatalf("target offsets: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "kates.orders:0:67\n") || !strings.HasSuffix(strings.TrimSpace(text), "kates.orders end offsets: 200") {
		t.Fatalf("offsets output:\n%s", text)
	}
	applies := f.find("kubectl -n kafka apply -f -")
	if len(applies) != 1 || !strings.Contains(applies[0].stdin, "image: quay.io/strimzi/kafka:1.1.0-kafka-4.3.0") || !strings.Contains(applies[0].stdin, "kates.io/test-pod") {
		t.Fatalf("pod manifest: %+v", applies)
	}
	for _, l := range f.lines() {
		if strings.Contains(l, "s3cret") {
			t.Fatalf("the password reached a command line: %q", l)
		}
	}
	if !f.hasLine("kubectl -n kafka delete pod kates-migrate-target") {
		t.Fatal("the client pod is removed afterwards")
	}

	out.Reset()
	outputMode = "json"
	if err := runMigrateTarget(nil, opts, "topics", ""); err != nil {
		t.Fatalf("target topics: %v", err)
	}
	var doc map[string][]string
	if err := json.Unmarshal([]byte(out.String()), &doc); err != nil {
		t.Fatalf("topics -o json: %v\n%s", err, out.String())
	}
	if strings.Join(doc["topics"], ",") != "__consumer_offsets,kates-events,kates.orders" {
		t.Fatalf("topics: %v", doc)
	}
}

func TestMigrateSourceDeployAndRemove(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	f.on("kubectl get namespace kafka-old --ignore-not-found -o jsonpath={.metadata.name}", "")
	f.on("kubectl create namespace kafka-old", "")
	f.on("kubectl label namespace kafka-old", "")
	f.on("helm upgrade --install old charts/legacy-kafka -n kafka-old -f .build/migrate/m391-430/source-values.yaml --wait --timeout 600s", "")
	f.on("helm test old -n kafka-old --timeout 600s", "")
	captureOutput(t, "table")

	flags := &migrateSourceFlags{Version: "3.9.1", Provider: "auto", Namespace: "kafka-old", Release: "old", Topics: "kates.orders", Yes: true, Timeout: 600, Registry: "ghcr.io/bmscomp", TargetCluster: "krafter", TargetNS: "kafka", SkipBuild: true}
	if err := runMigrateSourceDeploy(nil, flags); err != nil {
		t.Fatalf("source deploy: %v", err)
	}
	if f.hasLine("docker") {
		t.Fatal("a 3.9.1 source runs the official image: nothing to build")
	}
	if f.hasLine("helm upgrade --install mm2") {
		t.Fatal("source deploy installs no mirror")
	}

	// remove finds it through its bootstrap Service and leaves a namespace
	// the lab did not create.
	f.on("kubectl get service -l app.kubernetes.io/name=legacy-kafka,app.kubernetes.io/component=broker -o json -A",
		`{"items":[{"metadata":{"name":"old-legacy-kafka-bootstrap","namespace":"kafka-old",
		  "labels":{"app.kubernetes.io/instance":"old","kates.io/lab":"m391-430","kates.io/kafka-mode":"kraft"},
		  "annotations":{"kates.io/bootstrap-address":"old-legacy-kafka-bootstrap.kafka-old.svc.cluster.local:9092","kates.io/kafka-version":"3.9.1"}}}]}`)
	f.on("kubectl -n kafka-old get statefulset old-legacy-kafka -o jsonpath={.spec.template.spec.containers[0].image}", "apache/kafka:3.9.1")
	f.on(`kubectl get namespace kafka-old --ignore-not-found -o jsonpath={.metadata.labels.kates\.io/lab}`, "")
	f.on("kubectl delete pod -A -l kates.io/lab=m391-430", "")
	f.on("helm uninstall old -n kafka-old", "")
	f.on("kubectl -n kafka delete secret -l kates.io/lab=m391-430,kates.io/lab-role=mirror --ignore-not-found", "")
	rm := &migrateSourceFlags{Name: "old", Yes: true, Timeout: 600, TargetNS: "kafka"}
	if err := runMigrateSourceRemove(nil, rm); err != nil {
		t.Fatalf("source remove: %v", err)
	}
	if !f.hasLine("helm uninstall old -n kafka-old") || f.hasLine("kubectl delete namespace") {
		t.Fatalf("remove: ran\n  %s", strings.Join(f.lines(), "\n  "))
	}
}

func TestMigrateMirrorDeployDryRunFromSource(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	f.on("kubectl get service -l app.kubernetes.io/name=legacy-kafka,app.kubernetes.io/component=broker -o json -A",
		`{"items":[{"metadata":{"name":"m282-430-src-legacy-kafka-bootstrap","namespace":"kafka-m282-430-src",
		  "labels":{"app.kubernetes.io/instance":"m282-430-src","kates.io/lab":"m282-430","kates.io/kafka-mode":"zookeeper"},
		  "annotations":{"kates.io/bootstrap-address":"m282-430-src-legacy-kafka-bootstrap.kafka-m282-430-src.svc.cluster.local:9092","kates.io/kafka-version":"2.8.2"}}}]}`)
	f.on("kubectl -n kafka-m282-430-src get statefulset m282-430-src-legacy-kafka -o jsonpath=", "ghcr.io/bmscomp/kates-legacy-kafka:2.8.2")
	out := captureOutput(t, "table")

	flags := &migrateMirrorFlags{From: "m282-430-src", Policy: "default", Topics: "kates.orders", Namespace: "kafka", TargetCluster: "krafter", DryRun: true, Timeout: 600, SourceMechanism: "plain"}
	if err := runMigrateMirrorDeploy(nil, flags); err != nil {
		t.Fatalf("mirror deploy --dry-run: %v", err)
	}
	text := stripAnsi(out.String())
	for _, want := range []string{
		"helm upgrade --install mm2-m282-430 charts/mirror-maker2 -n kafka -f charts/mirror-maker2/values-kind.yaml -f charts/mirror-maker2/values-migrate-2x.yaml -f .build/migrate/m282-430/mirror-values.yaml --timeout 10m",
		"mode: default",
		"bootstrapServers: m282-430-src-legacy-kafka-bootstrap.kafka-m282-430-src.svc.cluster.local:9092",
		`kafkaVersion: 2.8.2`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("dry-run output lacks %q:\n%s", want, text)
		}
	}
	if f.hasLine("helm upgrade") {
		t.Fatal("--dry-run installs nothing")
	}
}

func TestMigrateMirrorCutoverAndRollbackByRelease(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	state := "RUNNING"
	f.on("helm dependency build charts/mirror-maker2", "")
	f.handle("helm upgrade mm2 charts/mirror-maker2 -n kafka --reuse-values -f charts/mirror-maker2/values-cutover.yaml --timeout 600s", func([]string, string) (string, error) { state = "STOPPED"; return "", nil })
	f.handle("helm upgrade mm2 charts/mirror-maker2 -n kafka --reuse-values -f .build/migrate/mm2/rollback-values.yaml --timeout 600s", func([]string, string) (string, error) { state = "RUNNING"; return "", nil })
	f.handle("kubectl -n kafka get kafkamirrormaker2 mm2-mirror-maker2 -o json", func([]string, string) (string, error) {
		return mirrorCRJSON("mm2-mirror-maker2", state, "RUNNING"), nil
	})
	captureOutput(t, "table")

	flags := &migrateMirrorFlags{Release: "mm2", Namespace: "kafka", Yes: true, Timeout: 600}
	if err := runMigrateMirrorCutover(nil, flags, false); err != nil {
		t.Fatalf("mirror cutover: %v", err)
	}
	if state != "STOPPED" {
		t.Fatal("the cutover overlay was not applied")
	}
	if err := runMigrateMirrorCutover(nil, flags, true); err != nil {
		t.Fatalf("mirror rollback: %v", err)
	}
	if state != "RUNNING" {
		t.Fatal("the rollback overlay was not applied")
	}
	// Both need consent.
	flags.Yes = false
	if err := runMigrateMirrorCutover(nil, flags, false); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("cutover without consent must refuse, got %v", err)
	}
}

// TestMigrateCommandsHaveDocEntries asserts every runnable command of the
// migrate tree carries a doc_entries.yaml entry under its full path
// ("migrate up", "migrate image build"), the convention the other nested
// commands use; the groups that only hold subcommands (migrate, migrate
// image, …) have none, like test or ctx.
func TestMigrateCommandsHaveDocEntries(t *testing.T) {
	documented := map[string]bool{}
	for _, e := range docEntries {
		documented[e.Name] = true
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		name := strings.TrimPrefix(c.CommandPath(), "kates ")
		if c.Runnable() && !documented[name] {
			t.Errorf("no doc_entries.yaml entry for %q", name)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(migrateCmd)
	if c, _, err := rootCmd.Find([]string{"migrate", "run"}); err != nil || c.Name() != "run" {
		t.Fatalf("kates migrate run is not registered: %v", err)
	}
}
