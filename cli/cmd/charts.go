package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var chartsPullTo string

var chartsCmd = &cobra.Command{
	Use:   "charts",
	Short: "The Helm charts kates deploy installs",
}

var chartsPullCmd = &cobra.Command{
	Use:   "pull --to DIR",
	Short: "Pull this release's charts into a bundle kates deploy reads offline",
	Long: `Pulls every chart kates deploy installs, at the versions of this release,
into DIR, and checks each one's cosign signature as deploy does. Copy DIR to
a machine without network access, and deploy from it there:

  kates charts pull --to ./kates-charts
  kates deploy --chart-bundle ./kates-charts      # or KATES_CHART_BUNDLE

KATES_CHART_REGISTRY pulls from a mirror instead of GHCR. Run it again to
refresh the bundle. The bundle holds the Kates charts only: cert-manager,
Kyverno and the PostgreSQL of Kafka Connect still come from their own
repositories, so turn them off where there is no network.`,
	Example: `  kates charts pull --to ./kates-charts`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runChartsPull(cmd.Context(), chartsPullTo)
	},
}

func init() {
	chartsPullCmd.Flags().StringVar(&chartsPullTo, "to", "", "Directory to write the bundle into (created if missing)")
	_ = chartsPullCmd.MarkFlagRequired("to")
	chartsCmd.AddCommand(chartsPullCmd)
	rootCmd.AddCommand(chartsCmd)
}

func runChartsPull(ctx context.Context, to string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ChartVersions == "" {
		return fmt.Errorf("this is %s, a build that knows no published chart versions; pull a bundle with a release build of kates", Version)
	}
	if to == "" {
		return errors.New("--to names the directory to write the bundle into")
	}
	root, err := filepath.Abs(to)
	if err != nil {
		return err
	}
	dirs := deployChartDirs()
	if err := fillChartRoot(ctx, root, false, dirs); err != nil {
		return err
	}
	output.Success(fmt.Sprintf("%d charts of kates %s in %s", len(dirs), Version, root))
	output.Hint("Copy the directory across, then: kates deploy --chart-bundle " + to)
	return nil
}

// chartsOutsideBundle names what this deploy installs from repositories a
// bundle does not hold.
func chartsOutsideBundle() []string {
	var outside []string
	for _, c := range []struct {
		on   bool
		name string
	}{
		{deployWithCertManager, "cert-manager (--with-cert-manager, charts.jetstack.io)"},
		{deployWithKyverno, "Kyverno (--with-kyverno, kyverno.github.io)"},
		{deployWithKafkaConnect, "the PostgreSQL of Kafka Connect (--with-kafka-connect, charts.bitnami.com)"},
	} {
		if c.on {
			outside = append(outside, c.name)
		}
	}
	return outside
}
