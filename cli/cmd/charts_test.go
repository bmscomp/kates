package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChartsFromAMirror(t *testing.T) {
	withChartBuild(t, "kates=0.14.1")
	calls := fakeHelmPull(t)
	t.Setenv("KATES_CHART_REGISTRY", "oci://mirror.corp/kates/charts/")
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err != nil {
		t.Fatal(err)
	}
	if pull := callsOf(*calls, "helm pull")[0]; pull[2] != "oci://mirror.corp/kates/charts/kates" || strings.Contains(strings.Join(pull, " "), "--plain-http") {
		t.Fatalf("pulled %v, want the mirror over TLS", pull)
	}
	if verify := callsOf(*calls, "cosign")[0]; !strings.HasPrefix(verify[2], "mirror.corp/kates/charts/kates@sha256:") || verify[4] != chartSignerIdentity {
		t.Fatalf("verified %v, want the mirror's copy against the same signer", verify)
	}
	if got := chartRef("kates"); got != "oci://mirror.corp/kates/charts/kates --version 0.14.1" {
		t.Fatalf("chartRef = %q", got)
	}

	t.Setenv("KATES_CHART_REGISTRY_PLAIN_HTTP", "1")
	t.Setenv("KATES_CHART_REGISTRY", "oci://localhost:5001/charts")
	n := len(*calls)
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err != nil {
		t.Fatal(err)
	}
	again := strings.Join(callsOf((*calls)[n:], "helm pull")[0], " ")
	if !strings.HasSuffix(again, "--plain-http") {
		t.Fatalf("a plain-HTTP mirror needs --plain-http: %s", again)
	}

	t.Setenv("KATES_CHART_REGISTRY", "https://charts.corp/kates")
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err == nil || !strings.Contains(err.Error(), "oci://") {
		t.Fatalf("a classic chart repository URL must be refused: %v", err)
	}
}

func TestAPlainHTTPMirrorIsVerifiedOverHTTP(t *testing.T) {
	withChartBuild(t, "kates=0.14.1")
	calls := fakeHelmPull(t)
	t.Setenv("KATES_CHART_REGISTRY", "oci://registry.lab:5000/charts")
	t.Setenv("KATES_CHART_REGISTRY_PLAIN_HTTP", "1")
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err != nil {
		t.Fatal(err)
	}
	if verify := callsOf(*calls, "cosign")[0]; verify[len(verify)-1] != "--allow-http-registry" {
		t.Fatalf("cosign must be allowed to read the mirror over HTTP: %v", verify)
	}
}

// allChartVersions names every chart deploy installs, as a release build does.
const allChartVersions = "cdc-postgresql=0.1.0,legacy-kafka=0.2.1,apicurio-registry=0.4.1,connect-cluster=2.1.14,kafka-cluster=1.0.18,kafka-ui=0.3.2,kates=0.14.1,kates-chaos=2.3.4,mirror-maker2=0.11.12,kates-monitoring=1.7.1,strimzi-operator=0.4.5"

func TestAChartBundleIsPulledThenReadWithNoNetwork(t *testing.T) {
	withChartBuild(t, allChartVersions)
	fakeHelmPull(t)
	parent := t.TempDir()
	neighbour := filepath.Join(parent, "notes")
	if err := os.MkdirAll(neighbour, 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(parent, "kates-charts")
	if err := runChartsPull(context.Background(), bundle, false, ""); err != nil {
		t.Fatalf("charts pull: %v", err)
	}
	for _, dir := range publishedChartDirs() {
		if !chartVerified(filepath.Join(bundle, "charts", dir)) {
			t.Errorf("%s is not in the bundle with its signature check", dir)
		}
	}

	// Elsewhere, with nothing cached and no network at all.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KATES_REQUIRE_SIGNED_CHARTS", "1")
	runExecCombinedFn = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("no network here")
	}
	latestReleaseFn = func(context.Context) (string, error) {
		t.Fatal("a bundle run asked GitHub for the newest release")
		return "", nil
	}
	deployChartBundle = bundle
	warnIfNewerRelease(context.Background())
	if err := resolveChartRoot(context.Background(), "", false, deployChartDirs()...); err != nil {
		t.Fatalf("the bundle should serve the deploy offline: %v", err)
	}
	if chartRoot != bundle || !chartsPublished {
		t.Fatalf("chart root %q published %v, want the bundle", chartRoot, chartsPublished)
	}
	if !strings.Contains(chartSource(), "bundle") {
		t.Errorf("the source line should name the bundle: %q", chartSource())
	}
	if _, err := os.Stat(neighbour); err != nil {
		t.Fatalf("a bundle's neighbour was removed: %v", err)
	}

	ChartVersions = strings.Replace(allChartVersions, "kates=0.14.1", "kates=0.14.2", 1)
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err == nil || !strings.Contains(err.Error(), "kates charts pull --to") {
		t.Fatalf("a bundle of another release must be refused with the way out: %v", err)
	}
}

func TestChartBundleRefusals(t *testing.T) {
	withChartBuild(t, "kates=0.14.1")
	deployChartBundle = t.TempDir()
	if err := resolveChartRoot(context.Background(), "/src/kates", false, "kates"); err == nil || !strings.Contains(err.Error(), "choose one") {
		t.Fatalf("a checkout and a bundle together: %v", err)
	}
	ChartVersions = ""
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err == nil || !strings.Contains(err.Error(), "release build") {
		t.Fatalf("a dev build cannot read a bundle: %v", err)
	}
	if err := runChartsPull(context.Background(), t.TempDir(), false, ""); err == nil || !strings.Contains(err.Error(), "release build") {
		t.Fatalf("a dev build cannot pull a bundle: %v", err)
	}
}

func TestChartsOutsideBundle(t *testing.T) {
	setDeployFlags(t, func() {
		deployWithCertManager, deployWithKyverno, deployWithKafkaConnect = true, false, true
	})
	got := strings.Join(chartsOutsideBundle(), "; ")
	// Kafka Connect's PostgreSQL is this repository's chart, so it is bundled.
	if !strings.Contains(got, "cert-manager") || strings.Contains(got, "Kyverno") || strings.Contains(got, "PostgreSQL") {
		t.Fatalf("chartsOutsideBundle = %q", got)
	}
}
