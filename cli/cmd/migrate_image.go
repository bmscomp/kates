package cmd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/cluster"
	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
)

// kates migrate image build — scripts/build-legacy-kafka-image.sh in Go.
//
// The official Apache Kafka image starts at 3.7.0 (KIP-975); the lines a
// migration needs on the source side have no upstream image at all.
// Dockerfile.legacy-kafka builds one from the Apache tarball; this drives it
// and loads the result into kind, the step everyone forgets and then spends
// ten minutes debugging ImagePullBackOff.

const (
	defaultScalaVersion = "2.13"
	defaultOSRelease    = "jammy"
	legacyImageName     = "kates-legacy-kafka"
	legacyDockerfile    = "Dockerfile.legacy-kafka"
)

// migrateImageOptions are the flags of image build.
type migrateImageOptions struct {
	Version, Scala, JRE, OSRelease, Platform, Registry string
	Load, Push                                         bool
	KindCluster                                        string
}

// image returns the image reference the options build.
func (o migrateImageOptions) image() string {
	registry := strings.TrimSuffix(o.Registry, "/")
	if registry == "" {
		registry = kafkaversion.DefaultLegacyRegistry
	}
	return registry + "/" + legacyImageName + ":" + o.Version
}

// jre is the Temurin JRE major for the version: Kafka 2.x supports Java 8
// and 11 only, 3.x added 17. Picking wrong produces a start-up crash that
// reads like a broker misconfiguration, so it is decided here.
func (o migrateImageOptions) jre() string {
	if o.JRE != "" {
		return o.JRE
	}
	if strings.HasPrefix(o.Version, "2.") {
		return "11"
	}
	return "17"
}

// buildArgs are the docker build arguments shared by build and buildx.
func (o migrateImageOptions) buildArgs() []string {
	args := []string{
		"--build-arg", "KAFKA_VERSION=" + o.Version,
		"--build-arg", "SCALA_VERSION=" + o.Scala,
		"--build-arg", "JRE_VERSION=" + o.jre(),
		"--build-arg", "UBUNTU_RELEASE=" + o.OSRelease,
		"-f", legacyDockerfile,
		"-t", o.image(),
	}
	if o.Platform != "" {
		args = append(args, "--platform", o.Platform)
	}
	return args
}

var (
	migrateImageOpts migrateImageOptions

	migrateImageCmd = &cobra.Command{
		Use:   "image",
		Short: "Build the legacy Kafka source image",
	}

	migrateImageBuildCmd = &cobra.Command{
		Use:   "build --version <kafka version> [--load | --push]",
		Short: "Build the legacy Kafka image from Dockerfile.legacy-kafka, verify it, load it into kind",
		Long: `Builds <registry>/kates-legacy-kafka:<version> from Dockerfile.legacy-kafka
(the Apache tarball, checksum-verified, on a pinned Temurin JRE), checks the
image really contains that Kafka version, and loads it into the kind
cluster (--load) or pushes it (--push, buildx, multi-platform capable).
--load and --push are mutually exclusive: a pushed manifest list is not in
the local daemon for kind to load.`,
		Example: `  kates migrate image build --version 2.8.2 --load
  kates migrate image build --version 3.5.2 --scala 2.13 --jre 17 --load
  kates migrate image build --version 2.8.2 --platform linux/amd64,linux/arm64 --push`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateImageBuild(cmd.Context(), migrateImageOpts)
		},
	}
)

func init() {
	f := migrateImageBuildCmd.Flags()
	f.StringVar(&migrateImageOpts.Version, "version", "2.8.2", "Kafka version to package")
	f.StringVar(&migrateImageOpts.Scala, "scala", defaultScalaVersion, "Scala build of the tarball")
	f.StringVar(&migrateImageOpts.JRE, "jre", "", "Temurin JRE major version (default: 11 for 2.x, 17 for 3.x)")
	f.StringVar(&migrateImageOpts.OSRelease, "os-release", defaultOSRelease, "Ubuntu release of the Temurin base image")
	f.StringVar(&migrateImageOpts.Platform, "platform", "", "Buildx platform(s), e.g. linux/arm64 or a comma list (default: the host's)")
	f.StringVar(&migrateImageOpts.Registry, "registry", kafkaversion.DefaultLegacyRegistry, "Image registry/namespace")
	f.BoolVar(&migrateImageOpts.Load, "load", false, "Load the image into the kind cluster after building")
	f.BoolVar(&migrateImageOpts.Push, "push", false, "Push the image to the registry (mutually exclusive with --load)")
	f.StringVar(&migrateImageOpts.KindCluster, "kind-cluster", cluster.DefaultKindClusterName, "Name of the kind cluster to load into")
	migrateImageCmd.AddCommand(migrateImageBuildCmd)
	migrateCmd.AddCommand(migrateImageCmd)
}

// defaultKindClusterName is the kind cluster the repo's tooling creates.
func defaultKindClusterName() string { return cluster.DefaultKindClusterName }

// kafkaJarLine matches the main Kafka jar, kafka_<scala>-<version>.jar,
// anchored on that shape so a classifier jar sorting first cannot make the
// check pass or fail for the wrong reason.
var kafkaJarLine = regexp.MustCompile(`^kafka_[0-9.]+-([0-9.]+)\.jar$`)

// runMigrateImageBuild is `kates migrate image build`.
func runMigrateImageBuild(ctx context.Context, o migrateImageOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := kafkaversion.Parse(o.Version); err != nil {
		return fmt.Errorf("--version: %w", err)
	}
	if o.Scala == "" {
		o.Scala = defaultScalaVersion
	}
	if o.OSRelease == "" {
		o.OSRelease = defaultOSRelease
	}
	if o.KindCluster == "" {
		o.KindCluster = cluster.DefaultKindClusterName
	}
	if o.Push && o.Load {
		return errors.New("--push and --load are mutually exclusive: a pushed (possibly multi-platform) image is not in the local daemon, so there is nothing for kind to load")
	}
	if _, err := defaultRunner.Run(ctx, "docker", "--version"); err != nil {
		return fmt.Errorf("docker is required to build the image: %w", err)
	}
	if o.Push {
		if _, err := defaultRunner.Run(ctx, "docker", "buildx", "version"); err != nil {
			return fmt.Errorf("--push needs docker buildx: %w", err)
		}
	}
	image := o.image()
	output.SubHeader("Building " + image)
	migrateHint(fmt.Sprintf("Kafka %s (scala %s) on Temurin JRE %s, %s", o.Version, o.Scala, o.jre(), o.OSRelease))

	if o.Push {
		args := append([]string{"buildx", "build"}, o.buildArgs()...)
		args = append(args, "--push", ".")
		if _, err := defaultRunner.Run(ctx, "docker", args...); err != nil {
			return fmt.Errorf("docker buildx build --push: %w", err)
		}
		migrateSuccess("pushed " + image)
		// A pushed manifest is not in the local store; pull the host's own
		// platform back, which is the one docker run can execute.
		if _, err := defaultRunner.Run(ctx, "docker", "pull", "--quiet", image); err != nil {
			return fmt.Errorf("pull %s back for verification: %w", image, err)
		}
	} else {
		args := append([]string{"build"}, o.buildArgs()...)
		args = append(args, ".")
		if _, err := defaultRunner.Run(ctx, "docker", args...); err != nil {
			return fmt.Errorf("docker build: %w", err)
		}
		migrateSuccess("built " + image)
	}

	if err := verifyLegacyImage(ctx, image, o.Version); err != nil {
		return err
	}

	if o.Load {
		if _, err := defaultRunner.Run(ctx, "kind", "get", "clusters"); err != nil {
			return fmt.Errorf("kind is required for --load: %w", err)
		}
		migrateHint(fmt.Sprintf("Loading %s into kind cluster %q...", image, o.KindCluster))
		if _, err := defaultRunner.Run(ctx, "kind", "load", "docker-image", image, "--name", o.KindCluster); err != nil {
			return fmt.Errorf("kind load docker-image: %w", err)
		}
		migrateSuccess("loaded into kind")
	}
	migrateHint("Use it:  kates migrate up --from " + o.Version + "  (or: kates migrate source deploy --version " + o.Version + ")")
	return nil
}

// verifyLegacyImage lists /opt/kafka/libs in the image and checks the main
// Kafka jar carries the expected version — no shell in the container, the
// listing is parsed here.
func verifyLegacyImage(ctx context.Context, image, version string) error {
	migrateHint(fmt.Sprintf("Verifying the image actually contains Kafka %s...", version))
	out, err := defaultRunner.Run(ctx, "docker", "run", "--rm", "--entrypoint", "ls", image, "/opt/kafka/libs/")
	if err != nil {
		return fmt.Errorf("list the image's Kafka libraries: %w", err)
	}
	found := kafkaJarVersion(out)
	if found != version {
		return fmt.Errorf("image reports Kafka %q, expected %s", found, version)
	}
	migrateSuccess("image contains Kafka " + found)
	return nil
}

// kafkaJarVersion finds the version of the main Kafka jar in a directory
// listing; empty when there is none.
func kafkaJarVersion(listing string) string {
	for _, line := range strings.Split(listing, "\n") {
		if m := kafkaJarLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			return m[1]
		}
	}
	return ""
}
