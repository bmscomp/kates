package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/strimzi"
)

// Where deploy's charts come from.
//
// A release build knows the version of every chart at its tag (ChartVersions,
// which release.yml sets) and installs those packages from the registry the
// same release publishes them to. They are pulled once into the user cache
// and unpacked there as charts/<dir>, the layout of a checkout, so the rest of
// deploy reads Chart.yaml files and values overlays the same way either way.
//
// A dev build knows no published versions, so it installs the charts of the
// checkout it runs in, as deploy always did. --checkout or KATES_CHECKOUT
// makes any build do that, from the checkout it names.
//
// Without GHCR, KATES_CHART_REGISTRY names a mirror to pull from instead, and
// a bundle that `kates charts pull --to DIR` wrote (--chart-bundle,
// KATES_CHART_BUNDLE) is read as it is, with no network at all.

// ChartVersions is "name=version,…" for every chart at this build's tag; empty
// in a dev build.
var ChartVersions = ""

// defaultChartRegistry is where publish-charts.yml pushes every chart.
const defaultChartRegistry = "oci://ghcr.io/bmscomp/charts"

// chartRegistry is where the charts are pulled from: KATES_CHART_REGISTRY, a
// mirror holding the same packages and their signatures, else GHCR.
func chartRegistry() string {
	if r := strings.TrimSuffix(os.Getenv("KATES_CHART_REGISTRY"), "/"); r != "" {
		return r
	}
	return defaultChartRegistry
}

// chartRegistryPlainHTTP is KATES_CHART_REGISTRY_PLAIN_HTTP=1: the mirror
// speaks HTTP, like kind's local registry on localhost:5001.
func chartRegistryPlainHTTP() bool {
	return os.Getenv("KATES_CHART_REGISTRY_PLAIN_HTTP") == "1"
}

// deployChartNames are the chart directories deploy and kates migrate
// install from, with the name each is published under (its Chart.yaml name).
var deployChartNames = map[string]string{
	"apicurio-registry": "apicurio-registry",
	"legacy-kafka":      "legacy-kafka",
	"connect-cluster":   "connect-cluster",
	"kafka-cluster":     "kafka-cluster",
	"kafka-ui":          "kafka-ui",
	"kates":             "kates",
	"kates-chaos":       "kates-chaos",
	"mirror-maker2":     "mirror-maker2",
	"monitoring":        "kates-monitoring",
	"strimzi-operator":  "strimzi-operator",
}

var (
	// chartRoot holds charts/<dir>: a checkout, or the cache the published
	// packages are unpacked into.
	chartRoot = "."
	// chartsPublished is true when chartRoot holds published packages.
	chartsPublished   bool
	deployCheckout    string
	deployChartBundle string
)

// chartBundle is the bundle this run reads: --chart-bundle, else
// KATES_CHART_BUNDLE.
func chartBundle() string {
	if deployChartBundle != "" {
		return deployChartBundle
	}
	return os.Getenv("KATES_CHART_BUNDLE")
}

// chartRootPath is a checkout-relative path ("charts/kates") under chartRoot.
func chartRootPath(rel string) string {
	return filepath.Join(chartRoot, rel)
}

// resolveChartRoot points chartRoot at the charts this run installs: a
// checkout (checkout, else KATES_CHECKOUT, else a dev build's working
// directory), a bundle, or this release's published charts. Those dirs are
// pulled fresh on every run, so a cache left damaged or half-written never
// reaches a deploy; offline, or when the registry cannot be reached, the copy
// already cached is used. The caches of other kates versions are removed.
func resolveChartRoot(ctx context.Context, checkout string, offline bool, dirs ...string) error {
	if checkout == "" {
		checkout = os.Getenv("KATES_CHECKOUT")
	}
	bundle := chartBundle()
	switch {
	case checkout != "" && bundle != "":
		return fmt.Errorf("a checkout (%s) and a chart bundle (%s) were both named: choose one", checkout, bundle)
	case bundle != "" && ChartVersions == "":
		return fmt.Errorf("the chart bundle %s needs a release build of kates, which knows its chart versions; this is %s, so use --checkout instead", bundle, Version)
	case checkout != "" || ChartVersions == "":
		if checkout == "" {
			checkout = "."
		}
		chartRoot, chartsPublished = checkout, false
		return nil
	case bundle != "":
		// Read as it is: nothing pulled, nothing beside it touched.
		root, err := filepath.Abs(bundle)
		if err != nil {
			return err
		}
		if err := fillChartRoot(ctx, root, true, dirs); err != nil {
			return fmt.Errorf("the chart bundle %s does not serve kates %s: %w\n  Make it with this kates: kates charts pull --to %s", root, Version, err, bundle)
		}
		return nil
	}
	if !strings.HasPrefix(chartRegistry(), "oci://") {
		return fmt.Errorf("KATES_CHART_REGISTRY=%s: expected an oci:// reference, such as oci://registry.example.com/charts", chartRegistry())
	}
	cache, err := strimzi.DefaultCacheDir()
	if err != nil {
		return err
	}
	root := filepath.Join(filepath.Dir(cache), "charts", Version)
	pruneOtherChartCaches(root)
	return fillChartRoot(ctx, root, offline, dirs)
}

// fillChartRoot pulls dirs into root, or only checks them there offline,
// checks their signatures, and makes root the chart root.
func fillChartRoot(ctx context.Context, root string, offline bool, dirs []string) error {
	versions := parseChartVersions(ChartVersions)
	// After one failed refresh the registry is taken as unreachable for this
	// run: the other cached charts are used as they are, rather than each
	// waiting out a pull of its own.
	var refreshErr error
	for _, dir := range dirs {
		failed, err := pullPublishedChart(ctx, root, dir, versions, offline, refreshErr != nil)
		if err != nil {
			return err
		}
		if refreshErr == nil {
			refreshErr = failed
		}
	}
	if refreshErr != nil {
		warnStderr(fmt.Sprintf("Could not refresh the published charts, so the copies cached in %s are used: %v", root, refreshErr))
	}
	if err := checkChartSignatures(root, dirs); err != nil {
		return err
	}
	chartRoot, chartsPublished = root, true
	return nil
}

// warnStderr warns on stderr, so `kates versions -o json` still prints only JSON.
func warnStderr(msg string) {
	fmt.Fprintln(output.Err, output.WarningStyle.Render("  "+output.Glyphs().Warn+" "+msg))
}

// pruneOtherChartCaches removes the charts cached for other kates versions,
// beside root. Only this build's versions are ever installed.
func pruneOtherChartCaches(root string) {
	entries, err := os.ReadDir(filepath.Dir(root))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && e.Name() != filepath.Base(root) {
			_ = os.RemoveAll(filepath.Join(filepath.Dir(root), e.Name()))
		}
	}
}

// parseChartVersions reads ChartVersions into name → version.
func parseChartVersions(s string) map[string]string {
	versions := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		if name, version, ok := strings.Cut(strings.TrimSpace(pair), "="); ok && name != "" && version != "" {
			versions[name] = version
		}
	}
	return versions
}

// pullPublishedChart unpacks a fresh, signature-checked copy of one published
// chart into root/charts/dir. Offline, or with keepCached, a cached copy of
// the same version is used as it is. When the pull fails and such a copy is
// cached, it is kept and the failure comes back as failed rather than err;
// a signature that does not check out is always err.
func pullPublishedChart(ctx context.Context, root, dir string, versions map[string]string, offline, keepCached bool) (failed, err error) {
	name := deployChartNames[dir]
	version := versions[name]
	if version == "" {
		return nil, fmt.Errorf("this kates build (%s) names no published version of chart %q; set KATES_CHECKOUT to a Kates checkout to deploy its charts", Version, name)
	}
	dest := filepath.Join(root, "charts", dir)
	ref := chartRegistry() + "/" + name
	_, have, lerr := localChart(dest)
	cached := lerr == nil && have == version
	switch {
	case cached && (offline || keepCached):
		return nil, nil
	case offline:
		return nil, fmt.Errorf("chart %s %s is not cached in %s and the network is off", ref, version, dest)
	}
	err = fetchPublishedChart(ctx, ref, name, version, dir, dest)
	var sigErr *chartSignatureError
	if err != nil && cached && !errors.As(err, &sigErr) {
		return err, nil
	}
	return nil, err
}

// fetchPublishedChart pulls ref at version, checks its signature, and swaps
// it in for dest. dest is set aside first and put back if the swap fails, so
// a failed refresh leaves the cached copy in place. Without cosign the chart
// goes in unchecked, and its verified marker is removed.
func fetchPublishedChart(ctx context.Context, ref, name, version, dir, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating the chart cache: %w", err)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), ".pull-"+dir+"-")
	if err != nil {
		return fmt.Errorf("creating the chart cache: %w", err)
	}
	defer os.RemoveAll(tmp)

	// On stderr, so a first `kates versions -o json` still prints only JSON.
	fmt.Fprintln(output.Err, output.DimStyle.Render(fmt.Sprintf("  Pulling %s %s...", ref, version)))
	pullDir := filepath.Join(tmp, "pull")
	// The largest package is a few MB; a stalled network must not hold the run.
	pullCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	pullArgs := []string{"pull", ref, "--version", version, "--untar", "--untardir", pullDir}
	if chartRegistryPlainHTTP() {
		pullArgs = append(pullArgs, "--plain-http")
	}
	out, err := runExecCombinedFn(pullCtx, "helm", pullArgs...)
	if err != nil {
		return fmt.Errorf("pulling %s %s: %w: %s", ref, version, err, strings.TrimSpace(string(out)))
	}
	pulled := filepath.Join(pullDir, name)
	if _, got, err := localChart(pulled); err != nil || got != version {
		return fmt.Errorf("pulling %s %s gave %q: %v", ref, version, got, err)
	}
	m := helmDigest.FindStringSubmatch(string(out))
	if m == nil {
		return fmt.Errorf("helm pull printed no digest for %s %s, so its signature cannot be checked", ref, version)
	}
	signed := true
	if err := verifyChart(ctx, ref, m[1], dest); errors.Is(err, errNoCosign) {
		signed = false
	} else if err != nil {
		return err
	}
	if dir == "strimzi-operator" {
		// A package carries its subcharts unpacked; pkg/strimzi reads the
		// operator chart as the tarball `helm dependency build` leaves in a
		// checkout.
		sub := filepath.Join(pulled, "charts", strimzi.ChartName)
		if out, err := runExecCombinedFn(ctx, "helm", "package", sub, "--destination", filepath.Dir(sub)); err != nil {
			return fmt.Errorf("packing the operator chart of %s %s: %w: %s", ref, version, err, strings.TrimSpace(string(out)))
		}
		if err := os.RemoveAll(sub); err != nil {
			return err
		}
	}
	marker := verifiedMarker(dest)
	if !signed {
		if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	old := filepath.Join(tmp, "old")
	if err := os.Rename(dest, old); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("setting aside the cached %s: %w", dest, err)
	}
	if err := os.Rename(pulled, dest); err != nil {
		// Another kates may have unpacked the same version first.
		if _, have, rerr := localChart(dest); rerr == nil && have == version {
			return nil
		}
		_ = os.Rename(old, dest)
		return fmt.Errorf("unpacking %s %s into %s: %w", ref, version, dest, err)
	}
	if signed {
		return os.WriteFile(marker, []byte(m[1]+"\n"), 0o644)
	}
	return nil
}

// helmDependency runs `helm dependency` on a checkout's chart. A published
// package carries its subcharts already, and its file:// ones point outside it.
func helmDependency(ctx context.Context, args ...string) error {
	if chartsPublished {
		return nil
	}
	return runHelmFn(ctx, append([]string{"dependency"}, args...)...)
}

// chartRef names a chart in a command printed for someone else to run: its
// published reference, or its path in the checkout.
func chartRef(dir string) string {
	if chartsPublished {
		name := deployChartNames[dir]
		return fmt.Sprintf("%s/%s --version %s", chartRegistry(), name, parseChartVersions(ChartVersions)[name])
	}
	return chartRootPath(filepath.Join("charts", dir))
}

// chartSource says where this run's charts come from.
func chartSource() string {
	if chartsPublished {
		if bundle := chartBundle(); bundle != "" {
			return fmt.Sprintf("the bundle at %s, for kates %s", chartRoot, Version)
		}
		return fmt.Sprintf("%s, as published with kates %s", chartRegistry(), Version)
	}
	dir, err := filepath.Abs(chartRoot)
	if err != nil {
		dir = chartRoot
	}
	return "the checkout at " + dir
}

// deployChartDirs lists every chart deploy may install, for resolveChartRoot:
// all of them but legacy-kafka, which only kates migrate installs.
func deployChartDirs() []string {
	return slices.DeleteFunc(publishedChartDirs(), func(dir string) bool { return dir == "legacy-kafka" })
}

// publishedChartDirs lists every chart the CLI pulls, deploy's and migrate's.
func publishedChartDirs() []string {
	return slices.Sorted(maps.Keys(deployChartNames))
}

// latestReleaseURL answers with the newest kates release.
const latestReleaseURL = "https://api.github.com/repos/bmscomp/kates/releases/latest"

// latestReleaseFn reads the newest kates release's tag; tests replace it.
var latestReleaseFn = latestRelease

func latestRelease(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", latestReleaseURL, resp.Status)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

// warnIfNewerRelease says when a kates release newer than this build is out.
// This build installs its own release's charts, the ones it was tested with,
// so the newer charts come with the newer kates. The check never stops a run:
// offline, or on any error, it says nothing.
func warnIfNewerRelease(ctx context.Context) {
	if ChartVersions == "" || chartBundle() != "" {
		return // a dev build installs its checkout; a bundle is read offline
	}
	latest, err := latestReleaseFn(ctx)
	if err != nil {
		return
	}
	if cmp, err := compareVersions(strings.TrimPrefix(latest, "v"), strings.TrimPrefix(Version, "v")); err == nil && cmp > 0 {
		output.Warn(fmt.Sprintf("kates %s is out; this is %s, so this run installs %s's charts. For the new ones, upgrade first: brew upgrade bmscomp/tap/kates", latest, Version, Version))
	}
}
