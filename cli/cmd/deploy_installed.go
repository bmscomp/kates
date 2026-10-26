package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"gopkg.in/yaml.v3"
)

// Components a previous deploy left installed. kates deploy keeps a release
// that is already deployed; the wizard, the review and the dry run list each
// one with the chart it runs beside the chart this deploy ships, and the ones
// ticked, or named by --upgrade, are upgraded. None
// is offered a reinstall: helm uninstall would delete the KafkaTopics and
// Secrets their charts own (Connect's offsets, Apicurio's schemas).

// deployUpgradeReleases are the installed releases the wizard or --upgrade asked to upgrade.
var deployUpgradeReleases = map[string]bool{}

// releaseKept says whether deploy leaves an installed release as it is.
func releaseKept(ctx context.Context, release, namespace string) bool {
	return !deployUpgradeReleases[release] && isHelmReleaseDeployedFn(ctx, release, namespace)
}

// upgradableCharts are the releases the list offers to upgrade, and the chart
// each installs from. Kates is always upgraded, and PostgreSQL never is.
var upgradableCharts = map[string]string{
	"connect-cluster": "charts/connect-cluster",
	"monitoring":      "charts/monitoring",
	"apicurio":        "charts/apicurio-registry",
	"kafka-ui":        "charts/kafka-ui",
	"mm2":             "charts/mirror-maker2",
}

// deployedRelease is a release helm list shows as deployed.
type deployedRelease struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Chart     string `json:"chart"` // "kafka-ui-0.3.1"
}

// deployedReleases reads which of the entries' releases are deployed, in any
// namespace, with one helm list -A. An account that may not list every
// namespace (admin-less) has its entries' own namespaces read instead.
func deployedReleases(ctx context.Context, entries []DeploySummaryEntry) []deployedRelease {
	var names, namespaces []string
	for _, e := range entries {
		names = append(names, regexp.QuoteMeta(e.Release))
		if !slices.Contains(namespaces, e.Namespace) {
			namespaces = append(namespaces, e.Namespace)
		}
	}
	if len(names) == 0 {
		return nil
	}
	filter := "^(" + strings.Join(names, "|") + ")$"
	list := func(ctx context.Context, scope ...string) ([]deployedRelease, error) {
		out, err := runExecOutputFn(ctx, "helm", append(append([]string{"list"}, scope...), "--deployed", "--filter", filter, "-o", "json")...)
		if err != nil {
			return nil, err
		}
		var rows []deployedRelease
		return rows, json.Unmarshal(out, &rows)
	}
	// -A decodes every release on the cluster before it filters: on a big
	// one it may time out, and the namespaces still need their share of ctx.
	allCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if rows, err := list(allCtx, "-A"); err == nil {
		return rows
	}
	var rows []deployedRelease
	for _, ns := range namespaces {
		r, _ := list(ctx, "-n", ns)
		rows = append(rows, r...)
	}
	return rows
}

// localChart is the name and version of the chart in dir.
func localChart(dir string) (name, version string, err error) {
	data, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
	if err != nil {
		return "", "", err
	}
	var c struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return "", "", fmt.Errorf("parsing %s/Chart.yaml: %w", dir, err)
	}
	return c.Name, c.Version, nil
}

// describeInstalled sets an installed release's chart beside the one this
// deploy installs: "Kafka UI 0.3.0 → 0.3.1 in team-ui (upgrade)".
func describeInstalled(e DeploySummaryEntry, chart, dir string) string {
	name, ours, err := localChart(dir)
	if err != nil {
		return fmt.Sprintf("%s %s in %s", e.Name, chart, e.Namespace)
	}
	have, sameChart := strings.CutPrefix(chart, name+"-")
	verb := "upgrade"
	switch cmp, cerr := compareVersions(have, ours); {
	case !sameChart:
		verb = "replace with " + name
	case cerr != nil:
	case cmp == 0:
		verb = "same chart; reapply"
	case cmp > 0:
		verb = "older chart here; replace"
	}
	return fmt.Sprintf("%s %s → %s in %s (%s)", e.Name, have, ours, e.Namespace, verb)
}

// installedRow is a component of this deploy that is already installed.
type installedRow struct {
	release string
	// text sets what is installed beside what this deploy ships. For an
	// upgradable row it says what an upgrade would do; for the others, what
	// deploy does with it.
	text       string
	upgradable bool
}

// keptChartDirs are the charts of installed releases deploy keeps and does
// not offer to upgrade: upgrading the Kafka cluster's chart rolls its brokers,
// and cert-manager and Kyverno are the whole cluster's. The Kafka release is
// named by --kafka-name, so it is matched separately.
var keptChartDirs = map[string]string{"chaos": "charts/kates-chaos"}

// shippedChart is the chart this deploy installs for a kept release
// ("kafka-cluster-1.0.18"), or "" when it cannot be read.
func shippedChart(release string) string {
	if i := slices.IndexFunc(thirdPartyCharts, func(c thirdPartyChart) bool { return c.name == release }); i >= 0 {
		return thirdPartyCharts[i].name + "-" + thirdPartyCharts[i].version
	}
	dir, ok := keptChartDirs[release]
	if release == deployKafkaName {
		dir, ok = kafkaChartDir, true
	}
	if !ok {
		return ""
	}
	name, version, err := localChart(chartRootPath(dir))
	if err != nil {
		return ""
	}
	return name + "-" + version
}

// reconciledWhenBroken are the upgradable releases deploy upgrades even when
// kept, once it finds them broken (deployGroupB, deployKafkaConnect); what it
// checks needs the introspection the review comes before.
var reconciledWhenBroken = map[string]bool{"connect-cluster": true, "monitoring": true}

// installedRows reads, for each entry already deployed, what is installed
// beside what this deploy ships. The Strimzi operator is left out: its
// version screen and the versions phase say the same about it.
func installedRows(ctx context.Context, entries []DeploySummaryEntry, deployed []deployedRelease) []installedRow {
	var rows []installedRow
	for _, e := range entries {
		if e.Release == "strimzi-operator" {
			continue
		}
		i := slices.IndexFunc(deployed, func(r deployedRelease) bool { return r.Name == e.Release && r.Namespace == e.Namespace })
		if i < 0 {
			if j := slices.IndexFunc(deployed, func(r deployedRelease) bool { return r.Name == e.Release }); j >= 0 {
				r := deployed[j]
				rows = append(rows, installedRow{release: e.Release, text: fmt.Sprintf("%s %s is installed in %s; this deploy installs another one into %s", e.Name, r.Chart, r.Namespace, e.Namespace)})
			}
			continue
		}
		chart := deployed[i].Chart
		dir, upgradable := upgradableCharts[e.Release]
		switch {
		case upgradable:
			rows = append(rows, installedRow{release: e.Release, text: describeInstalled(e, chart, chartRootPath(dir)), upgradable: true})
		case e.Release == "kates":
			rows = append(rows, installedRow{release: e.Release, text: describeInstalled(e, chart, chartRootPath("charts/kates")) + ": always upgraded"})
		case e.Release == "postgresql":
			rows = append(rows, installedRow{release: e.Release, text: fmt.Sprintf("%s %s in %s: kept (its data would need migrating to another chart or PostgreSQL major version; one an older kates installed from bitnami/postgresql stays as it is)", e.Name, chart, e.Namespace)})
		case e.Release == deployKafkaName && !isKafkaClusterReady(ctx, e.Namespace, e.Release):
			// deployGroupB reapplies a release whose Kafka is not Ready.
			ours := shippedChart(e.Release)
			if ours == "" {
				ours = "this deploy's chart"
			}
			rows = append(rows, installedRow{release: e.Release, text: fmt.Sprintf("%s %s in %s: its Kafka is not Ready, so this deploy reapplies %s", e.Name, chart, e.Namespace, ours)})
		default:
			text := fmt.Sprintf("%s %s in %s: kept", e.Name, chart, e.Namespace)
			if ours := shippedChart(e.Release); ours != "" && ours != chart {
				text += "; this deploy ships " + ours
			}
			rows = append(rows, installedRow{release: e.Release, text: text})
		}
	}
	return rows
}

// installedSummary is one line per installed component, with what this
// deploy does to it, for the review and the dry run.
func installedSummary(rows []installedRow) []string {
	var lines []string
	for _, r := range rows {
		switch {
		case !r.upgradable:
			lines = append(lines, r.text)
		case deployUpgradeReleases[r.release]:
			lines = append(lines, r.text+": applied")
		case reconciledWhenBroken[r.release]:
			lines = append(lines, r.text+": kept, unless deploy finds it broken")
		default:
			lines = append(lines, r.text+": kept")
		}
	}
	return lines
}

// componentFlags turn on the components --upgrade may name.
var componentFlags = map[string]string{
	"apicurio":        "--with-schema-registry apicurio",
	"connect-cluster": "--with-kafka-connect",
	"kafka-ui":        "--with-kafka-ui",
	"mm2":             "--with-mirror-maker2",
	"monitoring":      "--with-monitoring",
}

// checkUpgradeInDeploy refuses an --upgrade name this deploy does not
// include, which would otherwise be ignored without a word.
func checkUpgradeInDeploy(names []string, entries []DeploySummaryEntry) error {
	for _, n := range names {
		if n != "all" && !slices.ContainsFunc(entries, func(e DeploySummaryEntry) bool { return e.Release == n }) {
			return fmt.Errorf("--upgrade %s: this deploy does not include it; add %s, or leave it out of --upgrade", n, componentFlags[n])
		}
	}
	return nil
}

// applyUpgradeFlag puts the releases --upgrade names on the upgrade list.
func applyUpgradeFlag(names []string) error {
	for _, n := range names {
		switch {
		case n == "all":
			for release := range upgradableCharts {
				deployUpgradeReleases[release] = true
			}
		case upgradableCharts[n] != "":
			deployUpgradeReleases[n] = true
		default:
			return fmt.Errorf("--upgrade %s: deploy upgrades only %s, or all", n, strings.Join(slices.Sorted(maps.Keys(upgradableCharts)), ", "))
		}
	}
	return nil
}

// runInstalledReleasesForm lists the components already installed and asks
// which to upgrade to this deploy's charts; the rest are kept.
func runInstalledReleasesForm() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	entries := buildSharedEntries(resolveNamespaces())
	var options []huh.Option[string]
	var notes []string
	for _, r := range installedRows(ctx, entries, deployedReleases(ctx, entries)) {
		if r.upgradable {
			options = append(options, huh.NewOption(r.text, r.release).Selected(deployUpgradeReleases[r.release]))
		} else {
			notes = append(notes, r.text)
		}
	}
	if len(options) == 0 && len(notes) == 0 {
		return nil
	}
	var fields []huh.Field
	if len(notes) > 0 {
		fields = append(fields, huh.NewNote().Title("Already installed").Description(strings.Join(notes, "\n")))
	}
	var picked []string
	if len(options) > 0 {
		fields = append(fields, huh.NewMultiSelect[string]().
			Title("Upgrade installed components?").
			Description("Space ticks one to upgrade to this deploy's chart; the rest are kept as they are.").
			Options(options...).
			Value(&picked))
	}
	if err := huh.NewForm(huh.NewGroup(fields...)).WithTheme(ThemeKates()).Run(); err != nil {
		return err
	}
	for _, o := range options {
		deployUpgradeReleases[o.Value] = slices.Contains(picked, o.Value)
	}
	return nil
}
