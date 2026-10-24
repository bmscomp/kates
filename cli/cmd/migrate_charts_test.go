package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/migrate"
)

func TestMigrateFromPublishedChartsAndCarriedPins(t *testing.T) {
	withChartBuild(t, "legacy-kafka=0.2.1,mirror-maker2=0.11.12,strimzi-operator=0.4.5")
	calls := fakeHelmPull(t)
	t.Chdir(t.TempDir())
	MigratePins = "STRIMZI_VERSION=1.2.0,STRIMZI_KAFKA_VERSION=1.2.0-kafka-4.3.1,KAFKA_IMAGE=quay.io/strimzi/kafka:1.2.0-kafka-4.3.1"

	pins, err := migrateChartsAndPins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pins.StrimziVersion != "1.2.0" || pins.KafkaVersion != "4.3.1" || pins.KafkaImage != "quay.io/strimzi/kafka:1.2.0-kafka-4.3.1" {
		t.Fatalf("pins %+v", pins)
	}
	pulled := callsOf(*calls, "helm pull")
	if len(pulled) != 3 || pulled[0][2] != "oci://ghcr.io/bmscomp/charts/legacy-kafka" || pulled[1][2] != "oci://ghcr.io/bmscomp/charts/mirror-maker2" || pulled[2][2] != "oci://ghcr.io/bmscomp/charts/strimzi-operator" {
		t.Fatalf("pulled %v, want legacy-kafka, mirror-maker2 and the operator chart to plan against", pulled)
	}
	src := &migrate.Source{Release: "src", Namespace: "kafka-src"}
	if args := helmSourceArgs(src, "v.yaml", "", 0); args[3] != filepath.Join(chartRoot, migrate.SourceChartPath) {
		t.Fatalf("the source installs from %s, want the pulled chart", args[3])
	}
	if err := ensureMirrorChartDeps(context.Background()); err != nil {
		t.Fatalf("a published mirror-maker2 needs no dependency build: %v", err)
	}

	MigratePins = ""
	if _, err := migrateChartsAndPins(context.Background()); err == nil || !strings.Contains(err.Error(), "KATES_CHECKOUT") {
		t.Fatalf("a build that carries no pins must say so: %v", err)
	}
}

func TestMigrateFromACheckout(t *testing.T) {
	withChartBuild(t, "")
	dir := migrateTestRepo(t)
	pins, err := migrateChartsAndPins(context.Background())
	if err != nil || pins.StrimziVersion != "1.1.0" {
		t.Fatalf("checkout pins %+v, %v", pins, err)
	}

	t.Chdir(t.TempDir())
	if _, err := migrateChartsAndPins(context.Background()); err == nil || !strings.Contains(err.Error(), "KATES_CHECKOUT") {
		t.Fatalf("a dev build outside a checkout: %v", err)
	}
	t.Setenv("KATES_CHECKOUT", dir)
	if pins, err := migrateChartsAndPins(context.Background()); err != nil || pins.StrimziVersion != "1.1.0" {
		t.Fatalf("KATES_CHECKOUT: %+v, %v", pins, err)
	}
}

func TestMigrateBuildRoot(t *testing.T) {
	migrateTestRepo(t)
	if got := migrateBuildRoot(); got != ".build/migrate" {
		t.Fatalf("in a checkout: %s", got)
	}
	t.Chdir(t.TempDir())
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if got := migrateBuildRoot(); got != filepath.Join(state, "kates", "migrate") {
		t.Fatalf("elsewhere: %s", got)
	}
}

func TestLegacyDockerfile(t *testing.T) {
	withChartBuild(t, "")
	migrateTestRepo(t)
	dockerfile, buildContext, remove, err := legacyDockerfile()
	if err != nil || dockerfile != migrate.LegacyDockerfileName || buildContext != "." {
		t.Fatalf("in a checkout: %s %s %v", dockerfile, buildContext, err)
	}
	remove()

	t.Chdir(t.TempDir())
	dockerfile, buildContext, remove, err = legacyDockerfile()
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dockerfile); string(data) != string(migrate.LegacyDockerfile) || filepath.Dir(dockerfile) != buildContext {
		t.Fatalf("outside a checkout %s (context %s) is not the built-in Dockerfile", dockerfile, buildContext)
	}
	remove()
	if _, err := os.Stat(buildContext); !os.IsNotExist(err) {
		t.Fatalf("the temporary build context was left behind: %v", err)
	}
}
