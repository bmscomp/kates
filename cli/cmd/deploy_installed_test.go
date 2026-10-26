package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/strimzi"
)

func TestDescribeInstalled(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("charts/kafka-ui", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("charts/kafka-ui", "Chart.yaml"), []byte("apiVersion: v2\nname: kafka-ui\nversion: 0.3.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := DeploySummaryEntry{Name: "Kafka UI", Release: "kafka-ui", Namespace: "team-ui"}

	for chart, want := range map[string]string{
		"kafka-ui-0.3.0":     "Kafka UI 0.3.0 → 0.3.1 in team-ui (upgrade)",
		"kafka-ui-0.3.1":     "Kafka UI 0.3.1 → 0.3.1 in team-ui (same chart; reapply)",
		"kafka-ui-0.4.0":     "Kafka UI 0.4.0 → 0.3.1 in team-ui (older chart here; replace)",
		"provectus-ui-1.2.0": "Kafka UI provectus-ui-1.2.0 → 0.3.1 in team-ui (replace with kafka-ui)",
	} {
		if got := describeInstalled(e, chart, "charts/kafka-ui"); got != want {
			t.Errorf("%s: got %q, want %q", chart, got, want)
		}
	}
}

func writeChart(t *testing.T, dir, name, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("apiVersion: v2\nname: "+name+"\nversion: "+version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeployedReleases_FallsBackToTheEntriesNamespaces(t *testing.T) {
	orig := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = orig })
	var calls []string
	runExecOutputFn = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		calls = append(calls, joined)
		switch {
		case strings.Contains(joined, " -A "):
			return nil, errors.New("forbidden: cannot list secrets at the cluster scope")
		case strings.Contains(joined, "-n team "):
			return []byte(`[{"name":"kafka-ui","namespace":"team","chart":"kafka-ui-0.3.0"}]`), nil
		}
		return []byte("[]"), nil
	}
	entries := []DeploySummaryEntry{{Release: "kafka-ui", Namespace: "team"}, {Release: "kates", Namespace: "team"}, {Release: "mm2", Namespace: "other"}}
	got := deployedReleases(context.Background(), entries)
	if len(got) != 1 || got[0].Chart != "kafka-ui-0.3.0" {
		t.Errorf("got %+v, want the kafka-ui row from team", got)
	}
	want := []string{
		"list -A --deployed --filter ^(kafka-ui|kates|mm2)$ -o json",
		"list -n team --deployed --filter ^(kafka-ui|kates|mm2)$ -o json",
		"list -n other --deployed --filter ^(kafka-ui|kates|mm2)$ -o json",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestInstalledRows(t *testing.T) {
	t.Chdir(t.TempDir())
	orig := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = orig; deployUpgradeReleases = map[string]bool{} })
	kafkaReady := "True"
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "kubectl" && strings.HasPrefix(strings.Join(args, " "), "get kafka "+deployKafkaName+" -n kafka") {
			return []byte(kafkaReady), nil
		}
		return nil, errors.New("unexpected " + name + " " + strings.Join(args, " "))
	}
	writeChart(t, "charts/kafka-ui", "kafka-ui", "0.3.2")
	writeChart(t, "charts/kates", "kates", "0.14.2")
	writeChart(t, "charts/kafka-cluster", "kafka-cluster", "1.0.18")
	writeChart(t, "charts/mirror-maker2", "mirror-maker2", "0.5.0")
	writeChart(t, "charts/connect-cluster", "connect-cluster", "0.2.1")

	entries := []DeploySummaryEntry{
		{Name: "Strimzi Operator", Release: "strimzi-operator", Namespace: "strimzi-operator"},
		{Name: "Cert-Manager", Release: "cert-manager", Namespace: "cert-manager"},
		{Name: "Kafka (krafter)", Release: deployKafkaName, Namespace: "kafka"},
		{Name: "PostgreSQL", Release: "postgresql", Namespace: "db"},
		{Name: "Kates Backend", Release: "kates", Namespace: "kates"},
		{Name: "Kafka UI", Release: "kafka-ui", Namespace: "kafka"},
		{Name: "MirrorMaker 2", Release: "mm2", Namespace: "kafka"},
		{Name: "Apicurio", Release: "apicurio", Namespace: "kafka"},
		{Name: "Kafka Connect", Release: "connect-cluster", Namespace: "connect"},
	}
	deployed := []deployedRelease{
		{Name: "strimzi-operator", Namespace: "strimzi-operator", Chart: "strimzi-operator-0.4.4"},
		{Name: "cert-manager", Namespace: "cert-manager", Chart: "cert-manager-v1.12.0"},
		{Name: deployKafkaName, Namespace: "kafka", Chart: "kafka-cluster-1.0.17"},
		{Name: "postgresql", Namespace: "db", Chart: "postgresql-16.4.0"},
		{Name: "kates", Namespace: "kates", Chart: "kates-0.14.0"},
		{Name: "kafka-ui", Namespace: "kafka", Chart: "kafka-ui-0.3.0"},
		{Name: "mm2", Namespace: "team-mm2", Chart: "mirror-maker2-0.4.0"},
		{Name: "connect-cluster", Namespace: "connect", Chart: "connect-cluster-0.2.0"},
	}
	deployUpgradeReleases = map[string]bool{"kafka-ui": true}

	got := installedSummary(installedRows(context.Background(), entries, deployed))
	want := []string{
		"Cert-Manager cert-manager-v1.12.0 in cert-manager: kept; this deploy ships cert-manager-" + certManagerChartVersion,
		"Kafka (krafter) kafka-cluster-1.0.17 in kafka: kept; this deploy ships kafka-cluster-1.0.18",
		"PostgreSQL postgresql-16.4.0 in db: kept (its data would need migrating to another chart or PostgreSQL major version; one an older kates installed from bitnami/postgresql stays as it is)",
		"Kates Backend 0.14.0 → 0.14.2 in kates (upgrade): always upgraded",
		"Kafka UI 0.3.0 → 0.3.2 in kafka (upgrade): applied",
		"MirrorMaker 2 mirror-maker2-0.4.0 is installed in team-mm2; this deploy installs another one into kafka",
		"Kafka Connect 0.2.0 → 0.2.1 in connect (upgrade): kept, unless deploy finds it broken",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	deployUpgradeReleases = map[string]bool{}
	if got := installedSummary(installedRows(context.Background(), entries[5:6], deployed)); len(got) != 1 || !strings.HasSuffix(got[0], "(upgrade): kept") {
		t.Errorf("an unticked upgradable release: got %q, want it kept", got)
	}

	kafkaReady = "False"
	want = []string{"Kafka (krafter) kafka-cluster-1.0.17 in kafka: its Kafka is not Ready, so this deploy reapplies kafka-cluster-1.0.18"}
	if got := installedSummary(installedRows(context.Background(), entries[2:3], deployed)); strings.Join(got, "\n") != want[0] {
		t.Errorf("a Kafka that is not Ready: got %q, want %q", got, want)
	}
}

func TestCheckUpgradeInDeploy(t *testing.T) {
	entries := []DeploySummaryEntry{{Release: "kafka-ui"}, {Release: "kates"}}
	if err := checkUpgradeInDeploy([]string{"kafka-ui", "all"}, entries); err != nil {
		t.Errorf("components in the deploy refused: %v", err)
	}
	err := checkUpgradeInDeploy([]string{"mm2"}, entries)
	if err == nil || err.Error() != "--upgrade mm2: this deploy does not include it; add --with-mirror-maker2, or leave it out of --upgrade" {
		t.Errorf("a component outside the deploy: got %v", err)
	}
}

func TestApplyUpgradeFlag(t *testing.T) {
	t.Cleanup(func() { deployUpgradeReleases = map[string]bool{} })

	deployUpgradeReleases = map[string]bool{}
	if err := applyUpgradeFlag([]string{"kafka-ui", "mm2"}); err != nil || !deployUpgradeReleases["kafka-ui"] || !deployUpgradeReleases["mm2"] || deployUpgradeReleases["apicurio"] {
		t.Errorf("--upgrade kafka-ui,mm2: err %v, list %v", err, deployUpgradeReleases)
	}

	deployUpgradeReleases = map[string]bool{}
	if err := applyUpgradeFlag([]string{"all"}); err != nil || len(deployUpgradeReleases) != len(upgradableCharts) {
		t.Errorf("--upgrade all: err %v, list %v", err, deployUpgradeReleases)
	}

	err := applyUpgradeFlag([]string{"kafka"})
	if err == nil || !strings.Contains(err.Error(), "deploy upgrades only apicurio, connect-cluster, kafka-ui, mm2, monitoring, or all") {
		t.Errorf("--upgrade kafka: got %v, want a refusal naming the choices", err)
	}
}

func TestReleaseKept_UpgradeListTakesItOut(t *testing.T) {
	orig := isHelmReleaseDeployedFn
	t.Cleanup(func() { isHelmReleaseDeployedFn = orig; deployUpgradeReleases = map[string]bool{} })
	isHelmReleaseDeployedFn = func(context.Context, string, string) bool { return true }

	deployUpgradeReleases = map[string]bool{"kafka-ui": true}
	if releaseKept(context.Background(), "kafka-ui", "ns") {
		t.Error("a release on the upgrade list was kept")
	}
	if !releaseKept(context.Background(), "mm2", "ns") {
		t.Error("an installed release off the list was not kept")
	}
}

func TestAgainstInstalled(t *testing.T) {
	op := &strimzi.Operator{Version: "1.2.0"}
	for version, want := range map[string]string{
		"1.3.0": "1.3.0  (upgrade from 1.2.0)",
		"1.2.0": "1.2.0",
		"1.1.0": "1.1.0",
	} {
		if got := againstInstalled(version, version, op); got != want {
			t.Errorf("%s: got %q, want %q", version, got, want)
		}
	}
	if !olderThan("1.1.0", op) || olderThan("1.2.0", op) || olderThan("1.1.0", nil) {
		t.Error("olderThan: want only a release below the installed operator left out")
	}
}

func TestDeployDryRunListsInstalled(t *testing.T) {
	line := "Kafka UI 0.3.0 → 0.3.2 in kafka (upgrade): kept"
	out := stripStyle(captureStdout(t, func() { renderDeployPreview(uiTestEntries(), map[string]bool{}, []string{line}, nil, nil) }))
	if !strings.Contains(out, "Already installed\n    "+line) {
		t.Errorf("the dry run does not list what is installed:\n%s", out)
	}
}
