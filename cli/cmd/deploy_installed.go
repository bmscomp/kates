package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"gopkg.in/yaml.v3"
)

// Components a previous deploy left installed. kates deploy keeps a release
// that is already deployed; the wizard lists each one with the chart it runs
// beside the chart this repository ships, and upgrades the ones ticked. None
// is offered a reinstall: helm uninstall would delete the KafkaTopics and
// Secrets their charts own (Connect's offsets, Apicurio's schemas).

// deployUpgradeReleases are the installed releases the wizard was asked to upgrade.
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

// installedChart is the chart a deployed release runs, as helm list shows it
// ("kafka-ui-0.3.1"), or "" when none is deployed.
func installedChart(ctx context.Context, release, namespace string) string {
	out, err := runExecOutputFn(ctx, "helm", "list", "-n", namespace, "--deployed", "--filter", "^"+release+"$", "-o", "json")
	if err != nil {
		return ""
	}
	var rows []struct {
		Chart string `json:"chart"`
	}
	if json.Unmarshal(out, &rows) != nil || len(rows) == 0 {
		return ""
	}
	return rows[0].Chart
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

// installedReleaseChoices reads which of the components about to be deployed
// are already installed: the ones the list offers to upgrade, and notes on
// the ones it does not.
func installedReleaseChoices(ctx context.Context, entries []DeploySummaryEntry) (options []huh.Option[string], notes []string) {
	for _, e := range entries {
		dir, upgradable := upgradableCharts[e.Release]
		if !upgradable && e.Release != "kates" && e.Release != "postgresql" {
			continue // Strimzi has the version screen; Kafka and the operators are not listed
		}
		chart := installedChart(ctx, e.Release, e.Namespace)
		switch {
		case chart == "":
		case e.Release == "kates":
			notes = append(notes, describeInstalled(e, chart, chartRootPath("charts/kates"))+": always upgraded")
		case e.Release == "postgresql":
			notes = append(notes, fmt.Sprintf("%s %s in %s: kept (its data would need migrating to another chart or PostgreSQL major version; one an older kates installed from bitnami/postgresql stays as it is)", e.Name, chart, e.Namespace))
		default:
			options = append(options, huh.NewOption(describeInstalled(e, chart, chartRootPath(dir)), e.Release))
		}
	}
	return options, notes
}

// runInstalledReleasesForm lists the components already installed and asks
// which to upgrade to this deploy's charts; the rest are kept.
func runInstalledReleasesForm() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	options, notes := installedReleaseChoices(ctx, buildSharedEntries(resolveNamespaces()))
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
	for _, release := range picked {
		deployUpgradeReleases[release] = true
	}
	return nil
}
