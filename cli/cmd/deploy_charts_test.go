package cmd

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/output"
)

// withChartBuild sets the build's published chart versions for one test and
// restores everything resolveChartRoot touches.
func withChartBuild(t *testing.T, versions string) {
	t.Helper()
	oldVersions, oldVersion, oldPins := ChartVersions, Version, MigratePins
	oldRoot, oldPublished, oldBundle := chartRoot, chartsPublished, deployChartBundle
	oldCombined, oldHelm, oldLatest, oldCosign := runExecCombinedFn, runHelmFn, latestReleaseFn, cosignPathFn
	oldOut, oldErr := output.Out, output.Err
	t.Cleanup(func() {
		ChartVersions, Version, MigratePins = oldVersions, oldVersion, oldPins
		chartRoot, chartsPublished, deployChartBundle = oldRoot, oldPublished, oldBundle
		runExecCombinedFn, runHelmFn, latestReleaseFn, cosignPathFn = oldCombined, oldHelm, oldLatest, oldCosign
		output.Out, output.Err = oldOut, oldErr
	})
	ChartVersions, Version = versions, "v9.9.9"
	cosignPathFn = func() (string, error) { return "cosign", nil }
	t.Setenv("KATES_CHECKOUT", "")
	t.Setenv("KATES_REQUIRE_SIGNED_CHARTS", "")
	t.Setenv("KATES_CHART_BUNDLE", "")
	t.Setenv("KATES_CHART_REGISTRY", "")
	t.Setenv("KATES_CHART_REGISTRY_PLAIN_HTTP", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

// fakeDigest is the digest the fake registry serves for a chart at a version.
func fakeDigest(name, version string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(name+"@"+version)))
}

// callsOf are the calls among calls that start with prefix ("helm pull").
func callsOf(calls [][]string, prefix string) [][]string {
	var out [][]string
	for _, c := range calls {
		if strings.HasPrefix(strings.Join(c, " "), prefix) {
			out = append(out, c)
		}
	}
	return out
}

// fakeHelmPull stands in for `helm pull --untar` and `helm package`, writing
// what they would, and for a `cosign verify` that passes, and records every
// call.
func fakeHelmPull(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		write := func(path, data string) {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if name == "cosign" {
			return []byte("Verification for ... --\n"), nil
		}
		switch args[0] {
		case "pull": // pull <ref> --version <v> --untar --untardir <dir>
			chart := filepath.Join(args[6], filepath.Base(args[1]))
			write(filepath.Join(chart, "Chart.yaml"), "name: "+filepath.Base(args[1])+"\nversion: "+args[3]+"\n")
			if filepath.Base(args[1]) == "strimzi-operator" {
				write(filepath.Join(chart, "charts", "strimzi-kafka-operator", "Chart.yaml"), "name: strimzi-kafka-operator\nversion: 1.2.0\n")
			}
			return []byte("Pulled: " + args[1] + ":" + args[3] + "\nDigest: " + fakeDigest(filepath.Base(args[1]), args[3]) + "\n"), nil
		case "package": // package <dir> --destination <dir>
			write(filepath.Join(args[3], "strimzi-kafka-operator-1.2.0.tgz"), "tgz")
		}
		return nil, nil
	}
	return &calls
}

func TestParseChartVersions(t *testing.T) {
	got := parseChartVersions("kates=0.14.1, kates-monitoring=1.7.1,bad,=1.0.0,empty=,")
	want := map[string]string{"kates": "0.14.1", "kates-monitoring": "1.7.1"}
	if len(got) != len(want) || got["kates"] != "0.14.1" || got["kates-monitoring"] != "1.7.1" {
		t.Fatalf("parseChartVersions = %v, want %v", got, want)
	}
}

func TestResolveChartRootDevBuildReadsWorkingDirectory(t *testing.T) {
	withChartBuild(t, "")
	calls := fakeHelmPull(t)
	if err := resolveChartRoot(context.Background(), "", false, deployChartDirs()...); err != nil {
		t.Fatal(err)
	}
	if chartRoot != "." || chartsPublished || len(*calls) != 0 {
		t.Fatalf("dev build: root %q published %v calls %v; want the working directory, nothing pulled", chartRoot, chartsPublished, *calls)
	}
	if got := chartRootPath("charts/kafka-cluster"); got != "charts/kafka-cluster" {
		t.Fatalf("chartRootPath = %q, want the path deploy always used", got)
	}
}

func TestResolveChartRootCheckoutBeatsPublished(t *testing.T) {
	withChartBuild(t, "kates=0.14.1")
	calls := fakeHelmPull(t)
	t.Setenv("KATES_CHECKOUT", "/env/kates")
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err != nil {
		t.Fatal(err)
	}
	if chartRoot != "/env/kates" || chartsPublished {
		t.Fatalf("KATES_CHECKOUT: root %q published %v", chartRoot, chartsPublished)
	}
	if err := resolveChartRoot(context.Background(), "/flag/kates", false, "kates"); err != nil {
		t.Fatal(err)
	}
	if chartRoot != "/flag/kates" || len(*calls) != 0 {
		t.Fatalf("--checkout: root %q calls %v; the flag wins over the env and nothing is pulled", chartRoot, *calls)
	}
	if got := chartRef("kates"); got != "/flag/kates/charts/kates" {
		t.Fatalf("chartRef = %q, want the checkout's path", got)
	}
}

func TestResolveChartRootPullsThePublishedVersionsFreshEachRun(t *testing.T) {
	withChartBuild(t, "kates=0.14.1,kates-monitoring=1.7.1,strimzi-operator=0.4.5,velero=11.3.4")
	calls := fakeHelmPull(t)
	dirs := []string{"kates", "monitoring", "strimzi-operator"}
	if err := resolveChartRoot(context.Background(), "", false, dirs...); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "kates", "charts", "v9.9.9")
	if chartRoot != want || !chartsPublished {
		t.Fatalf("root %q published %v, want %q published", chartRoot, chartsPublished, want)
	}
	pull := strings.Join((*calls)[0][:6], " ")
	if pull != "helm pull oci://ghcr.io/bmscomp/charts/kates --version 0.14.1 --untar" {
		t.Fatalf("first call %q, want the published kates chart at the build's version", pull)
	}
	// Published as kates-monitoring, unpacked where a checkout has it.
	if _, v, err := localChart(chartRootPath("charts/monitoring")); err != nil || v != "1.7.1" {
		t.Fatalf("charts/monitoring: version %q, %v", v, err)
	}
	// The operator subchart is a tarball again, as pkg/strimzi reads it.
	sub := chartRootPath("charts/strimzi-operator/charts")
	if _, err := os.Stat(filepath.Join(sub, "strimzi-kafka-operator-1.2.0.tgz")); err != nil {
		t.Fatalf("operator tarball: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sub, "strimzi-kafka-operator")); !os.IsNotExist(err) {
		t.Fatalf("the unpacked operator subchart is still there: %v", err)
	}
	if got := chartRef("monitoring"); got != "oci://ghcr.io/bmscomp/charts/kates-monitoring --version 1.7.1" {
		t.Fatalf("chartRef = %q", got)
	}

	n := len(*calls)
	if err := resolveChartRoot(context.Background(), "", true, dirs...); err != nil {
		t.Fatalf("cached, offline: %v", err)
	}
	if len(*calls) != n {
		t.Fatalf("offline, the cache was pulled again: %v", (*calls)[n:])
	}

	// Online, every run pulls fresh copies. The digests are the ones already
	// verified, so cosign is not asked again.
	if err := resolveChartRoot(context.Background(), "", false, dirs...); err != nil {
		t.Fatal(err)
	}
	if again := (*calls)[n:]; len(callsOf(again, "helm pull")) != 3 || len(callsOf(again, "cosign")) != 0 {
		t.Fatalf("a second run made %v; want three pulls and no cosign call", again)
	}

	ChartVersions = "kates=0.14.2,kates-monitoring=1.7.1,strimzi-operator=0.4.5"
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err != nil {
		t.Fatal(err)
	}
	pulls := callsOf(*calls, "helm pull")
	if last := pulls[len(pulls)-1]; !slices.Contains(last, "0.14.2") {
		t.Fatalf("a new kates version was not pulled: %v", last)
	}
	if _, v, _ := localChart(chartRootPath("charts/kates")); v != "0.14.2" {
		t.Fatalf("charts/kates is %q after the re-pull", v)
	}
}

func TestResolveChartRootKeepsTheCacheWhenTheRegistryFails(t *testing.T) {
	withChartBuild(t, "kates=0.14.1,kafka-ui=0.3.2")
	fakeHelmPull(t)
	if err := resolveChartRoot(context.Background(), "", false, "kafka-ui", "kates"); err != nil {
		t.Fatal(err)
	}
	stderr := output.ResetForTesting()
	attempts := 0
	runExecCombinedFn = func(context.Context, string, ...string) ([]byte, error) {
		attempts++
		return []byte("dial tcp: lookup ghcr.io: no such host"), errors.New("exit status 1")
	}
	if err := resolveChartRoot(context.Background(), "", false, "kafka-ui", "kates"); err != nil {
		t.Fatalf("the cached copies should carry the run: %v", err)
	}
	// One failed refresh: the registry is unreachable, so kates is not tried.
	if attempts != 1 || strings.Count(stderr.String(), "Could not refresh") != 1 {
		t.Errorf("%d pulls, warnings %q; want one of each", attempts, stderr.String())
	}
	for dir, want := range map[string]string{"kafka-ui": "0.3.2", "kates": "0.14.1"} {
		if _, v, err := localChart(chartRootPath("charts/" + dir)); err != nil || v != want {
			t.Fatalf("the cached %s is gone after a failed refresh: %q %v", dir, v, err)
		}
	}

	ChartVersions = "kates=0.14.2,kafka-ui=0.3.2"
	if err := resolveChartRoot(context.Background(), "", false, "kafka-ui", "kates"); err == nil || !strings.Contains(err.Error(), "no such host") {
		t.Fatalf("with no copy of that version cached, the failure must stop the run: %v", err)
	}
}

func TestResolveChartRootRemovesOtherVersionsCaches(t *testing.T) {
	withChartBuild(t, "kates=0.14.1")
	fakeHelmPull(t)
	charts := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "kates", "charts")
	stale := filepath.Join(charts, "v9.9.8", "charts", "kates")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(charts, "v9.9.8")); !os.IsNotExist(err) {
		t.Errorf("v9.9.8's cache is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(charts, "v9.9.9", "charts", "kates", "Chart.yaml")); err != nil {
		t.Errorf("this version's cache: %v", err)
	}
}

func TestWarnIfNewerRelease(t *testing.T) {
	for _, tc := range []struct {
		name, versions, latest string
		err                    error
		want                   bool
	}{
		{"newer release", "kates=0.14.1", "v9.10.0", nil, true},
		{"this release", "kates=0.14.1", "v9.9.9", nil, false},
		{"older tag", "kates=0.14.1", "v9.9.8", nil, false},
		{"unreachable", "kates=0.14.1", "", errors.New("timeout"), false},
		{"dev build", "", "v9.10.0", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withChartBuild(t, tc.versions)
			asked := false
			latestReleaseFn = func(context.Context) (string, error) {
				asked = true
				return tc.latest, tc.err
			}
			out := output.ResetForTesting()
			warnIfNewerRelease(context.Background())
			if got := strings.Contains(out.String(), "upgrade first"); got != tc.want {
				t.Fatalf("warned %v, want %v: %q", got, tc.want, out.String())
			}
			if tc.versions == "" && asked {
				t.Fatal("a dev build asked GitHub for the latest release")
			}
		})
	}
}

func TestResolveChartRootRefusals(t *testing.T) {
	withChartBuild(t, "kates=0.14.1")
	calls := fakeHelmPull(t)
	if err := resolveChartRoot(context.Background(), "", true, "kates"); err == nil || !strings.Contains(err.Error(), "network is off") {
		t.Fatalf("offline with nothing cached: %v", err)
	}
	if err := resolveChartRoot(context.Background(), "", false, "kafka-ui"); err == nil || !strings.Contains(err.Error(), `"kafka-ui"`) {
		t.Fatalf("a chart the build names no version for: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("refusals pulled: %v", *calls)
	}
}

func TestHelmDependencySkipsPublishedPackages(t *testing.T) {
	withChartBuild(t, "")
	var ran [][]string
	runHelmFn = func(_ context.Context, args ...string) error {
		ran = append(ran, args)
		return nil
	}
	chartsPublished = true
	if err := helmDependency(context.Background(), "update", "x"); err != nil || len(ran) != 0 {
		t.Fatalf("published: err %v ran %v; a package carries its subcharts", err, ran)
	}
	chartsPublished = false
	if err := helmDependency(context.Background(), "update", "--skip-refresh", "charts/x"); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || strings.Join(ran[0], " ") != "dependency update --skip-refresh charts/x" {
		t.Fatalf("checkout: ran %v", ran)
	}
}
