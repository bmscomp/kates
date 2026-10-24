package cmd

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

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

// ChartVersions is "name=version,…" for every chart at this build's tag; empty
// in a dev build.
var ChartVersions = ""

// chartRegistry is where publish-charts.yml pushes every chart.
const chartRegistry = "oci://ghcr.io/bmscomp/charts"

// deployChartNames are the chart directories deploy installs from, with the
// name each is published under (its Chart.yaml name).
var deployChartNames = map[string]string{
	"apicurio-registry": "apicurio-registry",
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
	chartsPublished bool
	deployCheckout  string
)

// chartRootPath is a checkout-relative path ("charts/kates") under chartRoot.
func chartRootPath(rel string) string {
	return filepath.Join(chartRoot, rel)
}

// resolveChartRoot points chartRoot at the charts this run installs: a
// checkout (checkout, else KATES_CHECKOUT, else a dev build's working
// directory), or this release's published charts, of which it pulls the dirs
// not cached yet.
func resolveChartRoot(ctx context.Context, checkout string, offline bool, dirs ...string) error {
	if checkout == "" {
		checkout = os.Getenv("KATES_CHECKOUT")
	}
	if checkout != "" || ChartVersions == "" {
		if checkout == "" {
			checkout = "."
		}
		chartRoot, chartsPublished = checkout, false
		return nil
	}
	cache, err := strimzi.DefaultCacheDir()
	if err != nil {
		return err
	}
	root := filepath.Join(filepath.Dir(cache), "charts", Version)
	versions := parseChartVersions(ChartVersions)
	for _, dir := range dirs {
		if err := pullPublishedChart(ctx, root, dir, versions, offline); err != nil {
			return err
		}
	}
	chartRoot, chartsPublished = root, true
	return nil
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

// pullPublishedChart unpacks one published chart into root/charts/dir unless
// that version is there already.
func pullPublishedChart(ctx context.Context, root, dir string, versions map[string]string, offline bool) error {
	name := deployChartNames[dir]
	version := versions[name]
	if version == "" {
		return fmt.Errorf("this kates build (%s) names no published version of chart %q; set KATES_CHECKOUT to a Kates checkout to deploy its charts", Version, name)
	}
	dest := filepath.Join(root, "charts", dir)
	if _, have, err := localChart(dest); err == nil && have == version {
		return nil
	}
	ref := chartRegistry + "/" + name
	if offline {
		return fmt.Errorf("chart %s %s is not cached in %s and the network is off", ref, version, dest)
	}
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
	if out, err := runExecCombinedFn(ctx, "helm", "pull", ref, "--version", version, "--untar", "--untardir", tmp); err != nil {
		return fmt.Errorf("pulling %s %s: %w: %s", ref, version, err, strings.TrimSpace(string(out)))
	}
	pulled := filepath.Join(tmp, name)
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
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("removing the stale %s: %w", dest, err)
	}
	// Another kates may have unpacked the same version first.
	if err := os.Rename(pulled, dest); err != nil {
		if _, have, rerr := localChart(dest); rerr != nil || have != version {
			return fmt.Errorf("unpacking %s %s into %s: %w", ref, version, dest, err)
		}
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
		return fmt.Sprintf("%s/%s --version %s", chartRegistry, name, parseChartVersions(ChartVersions)[name])
	}
	return chartRootPath(filepath.Join("charts", dir))
}

// chartSource says where this run's charts come from.
func chartSource() string {
	if chartsPublished {
		return fmt.Sprintf("%s, as published with kates %s", chartRegistry, Version)
	}
	dir, err := filepath.Abs(chartRoot)
	if err != nil {
		dir = chartRoot
	}
	return "the checkout at " + dir
}

// deployChartDirs lists every chart deploy may install, for resolveChartRoot.
func deployChartDirs() []string {
	return slices.Sorted(maps.Keys(deployChartNames))
}
