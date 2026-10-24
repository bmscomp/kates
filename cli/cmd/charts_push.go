package cmd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
)

var (
	chartsPushFrom string
	chartsPushTo   string
)

var chartsPushCmd = &cobra.Command{
	Use:   "push --from DIR --to REGISTRY",
	Short: "Push a bundle's images to the registry a cluster without internet pulls from",
	Long: `Loads the images a bundle holds (kates charts pull --images) into Docker and
pushes each to REGISTRY under two names: REGISTRY/<image as written>, and
REGISTRY/<image without its registry host>, the name charts that replace
the registry ask for. Then it renders every chart the way deploy does when
pointed at REGISTRY, and fails if any image would come from elsewhere.

Deploy and migrate pull from the registry when KATES_IMAGE_REGISTRY names it:

  kates charts push --from ./kates-charts --to registry.internal:5000/kates
  KATES_IMAGE_REGISTRY=registry.internal:5000/kates kates deploy --chart-bundle ./kates-charts

Log Docker in to REGISTRY first (docker login). A registry served over plain
HTTP must be among the Docker daemon's insecure-registries.`,
	Example: `  kates charts push --from ./kates-charts --to registry.internal:5000/kates`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runChartsPush(cmd.Context(), chartsPushFrom, chartsPushTo)
	},
}

func init() {
	chartsPushCmd.Flags().StringVar(&chartsPushFrom, "from", "", "Bundle directory kates charts pull --images wrote")
	chartsPushCmd.Flags().StringVar(&chartsPushTo, "to", "", "Registry, with any path, to push the images to")
	_ = chartsPushCmd.MarkFlagRequired("from")
	_ = chartsPushCmd.MarkFlagRequired("to")
	chartsCmd.AddCommand(chartsPushCmd)
}

func runChartsPush(ctx context.Context, from, to string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	mirror := strings.TrimSuffix(strings.TrimPrefix(to, "oci://"), "/")
	if mirror == "" {
		return errors.New("--to names the registry to push the images to")
	}
	root, err := filepath.Abs(from)
	if err != nil {
		return err
	}
	archive := filepath.Join(root, "images.tar")
	if !fileExistsAt(archive) {
		return fmt.Errorf("%s holds no images: pull the bundle with kates charts pull --images", root)
	}
	images, err := readLines(filepath.Join(root, "images.txt"))
	if err != nil {
		return err
	}
	fmt.Fprintln(output.Err, output.DimStyle.Render("  Loading "+archive+" into Docker..."))
	if out, err := runExecCombinedFn(ctx, "docker", "load", "-i", archive); err != nil {
		return fmt.Errorf("docker load: %w: %s", err, lastLine(string(out)))
	}

	// Two references can lose their hosts to the same name; the first keeps it.
	owner := map[string]string{}
	var pushed, missing []string
	for i, image := range images {
		// images.txt also lists what is published for other platforms only,
		// which saveBundleImages left out of the archive.
		out, err := runExecOutputFn(ctx, "docker", "image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", image)
		if err != nil {
			missing = append(missing, image)
			continue
		}
		// The archive holds one platform of each image; pushing the whole
		// index fails on the platforms it lacks.
		platform := strings.TrimSpace(string(out))
		for _, name := range mirrorNames(mirror, image) {
			if prev, taken := owner[name]; taken {
				warnStderr(fmt.Sprintf("%s and %s are both %s without their registries; it stays %s", prev, image, name, prev))
				continue
			}
			owner[name] = image
			fmt.Fprintln(output.Err, output.DimStyle.Render(fmt.Sprintf("  Pushing %d/%d %s...", i+1, len(images), name)))
			if out, err := runExecCombinedFn(ctx, "docker", "tag", image, name); err != nil {
				return fmt.Errorf("docker tag %s %s: %w: %s", image, name, err, lastLine(string(out)))
			}
			if err := pushImage(ctx, image, name, platform); err != nil {
				return err
			}
			pushed = append(pushed, name)
		}
	}
	if len(missing) > 0 {
		warnStderr("Not in the bundle's archive, so not pushed: " + strings.Join(missing, ", "))
	}

	// What deploy pulls once pointed at the mirror; each must be there.
	want, err := harvestImages(ctx, root, mirror)
	if err != nil {
		return err
	}
	var bypass, absent []string
	for _, image := range want {
		switch {
		case !strings.HasPrefix(image, mirror+"/"):
			bypass = append(bypass, image)
		case !slices.Contains(pushed, image) && !slices.ContainsFunc(missing, func(m string) bool { return slices.Contains(mirrorNames(mirror, m), image) }):
			absent = append(absent, image)
		}
	}
	if len(bypass) > 0 || len(absent) > 0 {
		var b strings.Builder
		if len(bypass) > 0 {
			fmt.Fprintf(&b, "\n  pulled from their own registries, not %s: %s", mirror, strings.Join(bypass, ", "))
		}
		if len(absent) > 0 {
			fmt.Fprintf(&b, "\n  pulled from %s, which this push did not fill: %s", mirror, strings.Join(absent, ", "))
		}
		return fmt.Errorf("pointed at %s, the charts would still pull images it does not hold:%s", mirror, b.String())
	}
	output.Success(fmt.Sprintf("%d images of %s in %s, under %d names", len(images)-len(missing), root, mirror, len(pushed)))
	output.Hint("Deploy from it: KATES_IMAGE_REGISTRY=" + mirror + " kates deploy --chart-bundle " + from)
	return nil
}

// pushImage pushes name, a tag of image, for platform. Docker refuses to
// push or save some multi-platform images it holds and runs, such as
// quay.io/curl/curl:8.7.1 ("does not provide the specified platform"); a
// FROM-only build of the local image makes a single-platform copy of the
// same layers that it pushes.
func pushImage(ctx context.Context, image, name, platform string) error {
	out, err := runExecCombinedFn(ctx, "docker", "push", "--quiet", "--platform", platform, name)
	if err == nil {
		return nil
	}
	if !strings.Contains(string(out), "does not provide") {
		return fmt.Errorf("docker push %s: %w: %s", name, err, lastLine(string(out)))
	}
	if err := runExecStdinFn(ctx, "docker", []string{"build", "--quiet", "--platform", platform, "--pull=false", "-t", name, "-"}, "FROM "+image+"\n"); err != nil {
		return fmt.Errorf("copying %s for %s to push it: %w", image, platform, err)
	}
	if out, err := runExecCombinedFn(ctx, "docker", "push", "--quiet", name); err != nil {
		return fmt.Errorf("docker push %s: %w: %s", name, err, lastLine(string(out)))
	}
	return nil
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, sc.Err()
}

// legacyKafkaImages are the images kates migrate runs for the Kafka
// versions: the official apache/kafka from 3.7.0, to pull, and below it the
// image Dockerfile.legacy-kafka builds, built here for platform and named
// in built too, as no registry has it.
func legacyKafkaImages(ctx context.Context, versions []string, platform string) (images, built []string, err error) {
	for _, s := range versions {
		v, err := kafkaversion.Parse(s)
		if err != nil {
			return nil, nil, fmt.Errorf("--legacy-kafka %s: %w", s, err)
		}
		image := kafkaversion.LegacyImageFor(v, "")
		images = append(images, image)
		if strings.HasPrefix(image, "apache/kafka:") {
			continue
		}
		if err := runMigrateImageBuild(ctx, migrateImageOptions{
			Version: v.String(), Scala: defaultScalaVersion, OSRelease: defaultOSRelease, Platform: platform,
		}); err != nil {
			return nil, nil, err
		}
		built = append(built, image)
	}
	return images, built, nil
}
