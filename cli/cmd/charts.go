package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var (
	chartsPullTo       string
	chartsPullImages   bool
	chartsPullPlatform string
)

var chartsCmd = &cobra.Command{
	Use:   "charts",
	Short: "The Helm charts kates deploy installs",
}

var chartsPullCmd = &cobra.Command{
	Use:   "pull --to DIR",
	Short: "Pull this release's charts into a bundle kates deploy reads offline",
	Long: `Pulls every chart kates deploy and kates migrate install, at the versions of this release,
into DIR, and checks each one's cosign signature as deploy does. It also
pulls the cert-manager and Kyverno charts deploy installs, recording their
checksums, and with --images every container image the stack runs, saved
for kind clusters. Copy DIR to a machine without network access, and deploy
from it there:

  kates charts pull --to ./kates-charts
  kates deploy --chart-bundle ./kates-charts      # or KATES_CHART_BUNDLE

KATES_CHART_REGISTRY pulls from a mirror instead of GHCR. Run it again to
refresh the bundle. On kind, deploy loads the bundle's images into the
nodes; another cluster pulls them from its registry, so mirror the ones
images.txt lists there.`,
	Example: `  kates charts pull --to ./kates-charts
  kates charts pull --to ./kates-charts --images --platform linux/amd64`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runChartsPull(cmd.Context(), chartsPullTo, chartsPullImages, chartsPullPlatform)
	},
}

func init() {
	chartsPullCmd.Flags().StringVar(&chartsPullTo, "to", "", "Directory to write the bundle into (created if missing)")
	chartsPullCmd.Flags().BoolVar(&chartsPullImages, "images", false, "Also pull every container image the stack runs and save them into the bundle (needs docker; several GB)")
	chartsPullCmd.Flags().StringVar(&chartsPullPlatform, "platform", "", "Platform of the images, such as linux/amd64 (default: this machine's)")
	_ = chartsPullCmd.MarkFlagRequired("to")
	chartsCmd.AddCommand(chartsPullCmd)
	rootCmd.AddCommand(chartsCmd)
}

func runChartsPull(ctx context.Context, to string, images bool, platform string) error {
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
	dirs := publishedChartDirs()
	if err := fillChartRoot(ctx, root, false, dirs); err != nil {
		return err
	}
	if err := pullThirdPartyCharts(ctx, root); err != nil {
		return err
	}
	output.Success(fmt.Sprintf("%d charts of kates %s and %d third-party charts in %s", len(dirs), Version, len(thirdPartyCharts), root))
	if images {
		list, err := harvestImages(ctx, root)
		if err != nil {
			return err
		}
		if err := saveBundleImages(ctx, root, list, platform); err != nil {
			return err
		}
		output.Success(fmt.Sprintf("%d images in %s, listed in %s", len(list), filepath.Join(root, "images.tar"), filepath.Join(root, "images.txt")))
	}
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
		{deployWithCertManager && !bundleHolds("cert-manager"), "cert-manager (--with-cert-manager, charts.jetstack.io)"},
		{deployWithKyverno && !bundleHolds("kyverno"), "Kyverno (--with-kyverno, kyverno.github.io)"},
	} {
		if c.on {
			outside = append(outside, c.name)
		}
	}
	return outside
}
