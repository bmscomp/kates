package cmd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/detect"
	"gopkg.in/yaml.v3"
)

// What a bundle holds beyond the Kates charts, so a site with no network at
// all can deploy: the third-party charts deploy installs, and with --images
// every container image the stack runs.
//
//	<bundle>/third-party/<name>-<version>.tgz   cert-manager, Kyverno
//	<bundle>/third-party/SHA256SUMS             their checksums, at pull time
//	<bundle>/images.txt                         every image, one per line
//	<bundle>/images.tar                         them, as `docker save` wrote them

// certManagerChartVersion pins the cert-manager chart deploy installs.
const certManagerChartVersion = "v1.13.3"

// thirdPartyChart is a chart deploy installs from its upstream repository,
// added under alias.
type thirdPartyChart struct {
	name, alias, repo, version string
}

// thirdPartyCharts are the charts a bundle carries for cert-manager and
// Kyverno, at the versions deploy pins.
var thirdPartyCharts = []thirdPartyChart{
	{"cert-manager", "jetstack", "https://charts.jetstack.io", certManagerChartVersion},
	{"kyverno", "kyverno", "https://kyverno.github.io/kyverno/", kyvernoChartVersion},
}

const thirdPartyDir = "third-party"

func (c thirdPartyChart) file() string {
	return c.name + "-" + c.version + ".tgz"
}

// pullThirdPartyCharts pulls the third-party charts into root/third-party and
// records their checksums. There is no signature to check: the checksum
// pins what was pulled, so the bundle cannot change on its way across.
func pullThirdPartyCharts(ctx context.Context, root string) error {
	dir := filepath.Join(root, thirdPartyDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var sums strings.Builder
	for _, c := range thirdPartyCharts {
		fmt.Fprintln(output.Err, output.DimStyle.Render(fmt.Sprintf("  Pulling %s %s from %s...", c.name, c.version, c.repo)))
		_ = os.Remove(filepath.Join(dir, c.file()))
		if out, err := runExecCombinedFn(ctx, "helm", "pull", c.name, "--repo", c.repo, "--version", c.version, "--destination", dir); err != nil {
			return fmt.Errorf("pulling %s %s: %w: %s", c.name, c.version, err, strings.TrimSpace(string(out)))
		}
		sum, err := fileSHA256(filepath.Join(dir, c.file()))
		if err != nil {
			return fmt.Errorf("pulling %s %s: %w", c.name, c.version, err)
		}
		fmt.Fprintf(&sums, "%s  %s\n", sum, c.file())
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644)
}

// thirdPartyChartRef is the chart deploy installs for name: in bundle mode
// the bundle's tarball, checked against the checksum the pull recorded;
// otherwise, or for a bundle that lacks it, "" and deploy uses the
// repository as before.
func thirdPartyChartRef(name string) (string, error) {
	if chartBundle() == "" || !chartsPublished {
		return "", nil
	}
	i := slices.IndexFunc(thirdPartyCharts, func(c thirdPartyChart) bool { return c.name == name })
	if i < 0 {
		return "", nil
	}
	c := thirdPartyCharts[i]
	path := filepath.Join(chartRoot, thirdPartyDir, c.file())
	if !fileExistsAt(path) {
		return "", nil
	}
	want, err := recordedSHA256(filepath.Join(chartRoot, thirdPartyDir, "SHA256SUMS"), c.file())
	if err != nil {
		return "", err
	}
	got, err := fileSHA256(path)
	if err != nil {
		return "", err
	}
	if got != want {
		return "", fmt.Errorf("%s in the chart bundle does not match the checksum recorded when it was pulled (%s, recorded %s): pull the bundle again", path, got, want)
	}
	return path, nil
}

// thirdPartyChartArgs names a third-party chart to helm upgrade: the
// bundle's checked tarball, or <alias>/<name> at the pinned version, adding
// the repository first when addRepo is set.
func thirdPartyChartArgs(ctx context.Context, name string, addRepo bool) ([]string, error) {
	ref, err := thirdPartyChartRef(name)
	if err != nil || ref != "" {
		return []string{ref}, err
	}
	c := thirdPartyCharts[slices.IndexFunc(thirdPartyCharts, func(c thirdPartyChart) bool { return c.name == name })]
	if addRepo {
		_ = runHelmFn(ctx, "repo", "add", c.alias, c.repo)
		_ = runHelmFn(ctx, "repo", "update", c.alias)
	}
	return []string{c.alias + "/" + c.name, "--version", c.version}, nil
}

// bundleHolds reports whether the bundle carries the third-party chart name.
func bundleHolds(name string) bool {
	i := slices.IndexFunc(thirdPartyCharts, func(c thirdPartyChart) bool { return c.name == name })
	return i >= 0 && fileExistsAt(filepath.Join(chartRoot, thirdPartyDir, thirdPartyCharts[i].file()))
}

func recordedSHA256(sumsFile, name string) (string, error) {
	data, err := os.ReadFile(sumsFile)
	if err != nil {
		return "", fmt.Errorf("the chart bundle has no checksums for its third-party charts: %w", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if sum, file, ok := strings.Cut(sc.Text(), "  "); ok && file == name {
			return sum, nil
		}
	}
	return "", fmt.Errorf("%s records no checksum for %s", sumsFile, name)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ── Images ──────────────────────────────────────────────────────────────

// imageRef is what an image reference looks like in a manifest:
// [registry/]path[:tag][@sha256:digest], no spaces, no template leftovers.
var imageRef = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(:[0-9]+)?(/[a-z0-9._-]+)*(:[A-Za-z0-9._-]+)?(@sha256:[0-9a-f]{64})?$`)

// strimziKafkaImages is one "version=image" entry of STRIMZI_KAFKA_IMAGES.
var strimziKafkaImages = regexp.MustCompile(`(?m)^\s*([0-9]+\.[0-9]+\.[0-9]+)=(\S+)\s*$`)

// skippedStrimziImages are the operator's images for features deploy does
// not use: Kafka Connect builds and the HTTP bridge.
var skippedStrimziImages = []string{
	"STRIMZI_DEFAULT_KANIKO_EXECUTOR_IMAGE", "STRIMZI_DEFAULT_BUILDAH_IMAGE",
	"STRIMZI_DEFAULT_MAVEN_BUILDER", "STRIMZI_DEFAULT_KAFKA_BRIDGE_IMAGE",
}

// imagesInManifests lists the images rendered manifests run: every
// container's image, every *_IMAGE environment value (operators start their
// operands from those), and from Strimzi's STRIMZI_KAFKA_IMAGES only the
// image of kafkaVersion, the Kafka the platform runs.
func imagesInManifests(manifests []byte, kafkaVersion string) ([]string, error) {
	seen := map[string]bool{}
	add := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref != "" && imageRef.MatchString(ref) {
			seen[ref] = true
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(manifests))
	for {
		var doc any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading the rendered manifests: %w", err)
		}
		walkImages(doc, kafkaVersion, add)
	}
	return slices.Sorted(func(yield func(string) bool) {
		for ref := range seen {
			if !yield(ref) {
				return
			}
		}
	}), nil
}

// imageArgs are container flags that name an image an operator starts:
// the Prometheus Operator's config reloader, a sidecar of every Prometheus
// and Alertmanager pod. Thanos's base image is left out; deploy runs none.
var imageArgs = []string{"--prometheus-config-reloader="}

func walkImages(node any, kafkaVersion string, add func(string)) {
	switch n := node.(type) {
	case map[string]any:
		if image, ok := n["image"].(string); ok {
			add(image)
		}
		if args, ok := n["args"].([]any); ok {
			for _, a := range args {
				for _, flag := range imageArgs {
					if s, ok := a.(string); ok && strings.HasPrefix(s, flag) {
						add(strings.TrimPrefix(s, flag))
					}
				}
			}
		}
		// ConfigMap keys that name an image, such as KATES_CHAOS_STRESS_IMAGE.
		for k, v := range n {
			if s, ok := v.(string); ok && strings.HasSuffix(k, "_IMAGE") {
				add(s)
			}
		}
		if name, ok := n["name"].(string); ok {
			if value, ok := n["value"].(string); ok {
				switch {
				case name == "STRIMZI_KAFKA_IMAGES":
					for _, m := range strimziKafkaImages.FindAllStringSubmatch(value, -1) {
						if m[1] == kafkaVersion {
							add(m[2])
						}
					}
				case strings.HasSuffix(name, "_IMAGE") && !slices.Contains(skippedStrimziImages, name):
					add(value)
				}
			}
		}
		for _, v := range n {
			walkImages(v, kafkaVersion, add)
		}
	case []any:
		for _, v := range n {
			walkImages(v, kafkaVersion, add)
		}
	case string:
		// Manifests carried as text, such as the ChaosExperiments a
		// ConfigMap holds for kates-chaos, name images too.
		if strings.Contains(n, "image:") {
			dec := yaml.NewDecoder(strings.NewReader(n))
			for {
				var doc any
				if dec.Decode(&doc) != nil {
					break
				}
				if _, isText := doc.(string); !isText {
					walkImages(doc, kafkaVersion, add)
				}
			}
		}
	}
}

// bundleRenders are the ways deploy installs each chart, as -f overlays
// relative to the chart: its defaults, and its kind and generic overlays.
// kates also runs the published native image (values-native.yaml).
func bundleRenders(root, dir string) [][]string {
	renders := [][]string{nil}
	for _, overlay := range []string{"values-kind.yaml", "values-generic.yaml"} {
		if fileExistsAt(filepath.Join(root, "charts", dir, overlay)) {
			renders = append(renders, []string{overlay})
		}
	}
	if dir == "kates" {
		renders = append(renders, []string{"values-native.yaml"})
	}
	return renders
}

// harvestImages renders every chart in the bundle at root the ways deploy
// installs it, the third-party charts included, pointed at mirror when it is
// set (withImageMirror), and lists the images, the pre-flight's probe
// images among them.
func harvestImages(ctx context.Context, root, mirror string) ([]string, error) {
	_, kafkaVersion, err := chartAppVersion(filepath.Join(root, "charts", "kafka-cluster"))
	if err != nil {
		return nil, err
	}
	var manifests bytes.Buffer
	render := func(chart string, args ...string) error {
		out, err := runExecOutputFn(ctx, "helm", withImageMirror(mirror, append([]string{"template", "kates-bundle", chart}, args...))...)
		if err != nil {
			return fmt.Errorf("rendering %s %s to list its images: %w", chart, strings.Join(args, " "), err)
		}
		manifests.Write(out)
		manifests.WriteString("\n---\n")
		return nil
	}
	for _, dir := range publishedChartDirs() {
		if dir == "kates-platform" {
			continue // installed by neither deploy nor migrate; its subcharts are rendered on their own
		}
		chart := filepath.Join(root, "charts", dir)
		for _, overlays := range bundleRenders(root, dir) {
			var args []string
			for _, o := range overlays {
				args = append(args, "-f", filepath.Join(chart, o))
			}
			if err := render(chart, args...); err != nil {
				return nil, err
			}
		}
		// --with-monitoring switches on exporters, such as Litmus's.
		if keys, ok := scrapeValues["charts/"+dir]; ok {
			var args []string
			for _, k := range keys {
				args = append(args, "--set", k+"=true")
			}
			if err := render(chart, args...); err != nil {
				return nil, err
			}
		}
	}
	for _, c := range thirdPartyCharts {
		if err := render(filepath.Join(root, thirdPartyDir, c.file()), "--set", "installCRDs=true"); err != nil {
			return nil, err
		}
	}
	images, err := imagesInManifests(manifests.Bytes(), kafkaVersion)
	if err != nil {
		return nil, err
	}
	for _, probe := range detect.ProbeImages {
		if mirror != "" {
			probe = mirror + "/" + probe
		}
		if !slices.Contains(images, probe) {
			images = append(images, probe)
		}
	}
	slices.Sort(images)
	return images, nil
}

// chartAppVersion is a chart directory's name and appVersion.
func chartAppVersion(dir string) (name, appVersion string, err error) {
	data, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
	if err != nil {
		return "", "", err
	}
	var c struct {
		Name       string `yaml:"name"`
		AppVersion string `yaml:"appVersion"`
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return "", "", fmt.Errorf("parsing %s/Chart.yaml: %w", dir, err)
	}
	return c.Name, c.AppVersion, nil
}

// saveBundleImages pulls images for one platform, platform else the Docker
// server's own, except the ones in built, which this machine built and holds
// already, and saves them into root/images.tar, with their list in
// root/images.txt. The archive names every platform an image publishes but
// holds only that one's content, so it is imported per platform
// (loadBundleImages); docker save --platform refuses some multi-platform
// images that do hold it.
func saveBundleImages(ctx context.Context, root string, images, built []string, platform string) error {
	if platform == "" {
		out, err := runExecOutputFn(ctx, "docker", "version", "--format", "{{.Server.Os}}/{{.Server.Arch}}")
		if err != nil {
			return fmt.Errorf("reading the Docker server's platform: %w", err)
		}
		platform = strings.TrimSpace(string(out))
	}
	// A failed save must not leave an older archive for deploy to load.
	if err := os.Remove(filepath.Join(root, "images.tar")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "images.txt"), []byte(strings.Join(images, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	for i, image := range images {
		if slices.Contains(built, image) {
			continue
		}
		fmt.Fprintln(output.Err, output.DimStyle.Render(fmt.Sprintf("  Pulling image %d/%d %s...", i+1, len(images), image)))
		if out, err := runExecCombinedFn(ctx, "docker", "pull", "--quiet", "--platform", platform, image); err != nil {
			return fmt.Errorf("docker pull %s: %w: %s", image, err, strings.TrimSpace(string(out)))
		}
	}
	// An image published for other platforms only is pulled for one of them
	// anyway; it could not run on these nodes, so it stays out of the archive
	// and is named.
	var saved, foreign []string
	for _, image := range images {
		out, err := runExecOutputFn(ctx, "docker", "image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", image)
		if err != nil {
			return fmt.Errorf("docker image inspect %s: %w", image, err)
		}
		if got := strings.TrimSpace(string(out)); got != platform {
			foreign = append(foreign, image+" ("+got+")")
			continue
		}
		saved = append(saved, image)
	}
	if len(foreign) > 0 {
		warnStderr(fmt.Sprintf("Not published for %s, so not in images.tar (images.txt still lists them): %s", platform, strings.Join(foreign, ", ")))
	}
	fmt.Fprintln(output.Err, output.DimStyle.Render(fmt.Sprintf("  Saving %d images for %s into %s...", len(saved), platform, filepath.Join(root, "images.tar"))))
	if out, err := runExecCombinedFn(ctx, "docker", append([]string{"save", "-o", filepath.Join(root, "images.tar")}, saved...)...); err != nil {
		return fmt.Errorf("docker save: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// loadBundleImages puts the bundle's images where the cluster finds them: on
// kind, into every node; elsewhere a cluster pulls from its registry, so it
// says which images that registry must hold.
func loadBundleImages(ctx context.Context, kubeContext string, isKind bool) error {
	archive := chartRootPath("images.tar")
	if chartBundle() == "" || !fileExistsAt(archive) {
		return nil
	}
	if !isKind {
		if imageRegistry() == "" {
			output.Warn("The chart bundle's images are for kind clusters. This cluster pulls from registries: push them to one it reaches (kates charts push --from " + chartRoot + " --to REGISTRY) and deploy with KATES_IMAGE_REGISTRY=REGISTRY.")
		}
		return nil
	}
	cluster := kindClusterName(kubeContext)
	PrintPhaseItem(fmt.Sprintf("Loading the chart bundle's images into kind cluster %q...", cluster))
	out, err := runExecOutputFn(ctx, "kind", "get", "nodes", "--name", cluster)
	if err != nil {
		return fmt.Errorf("listing the nodes of kind cluster %q: %w", cluster, err)
	}
	for _, node := range strings.Fields(string(out)) {
		// kind load image-archive imports every platform the archive names,
		// and it names some it holds no content for; import the node's own.
		arch, err := runExecOutputFn(ctx, "docker", "exec", node, "uname", "-m")
		if err != nil {
			return fmt.Errorf("reading the architecture of %s: %w", node, err)
		}
		platform := "linux/" + map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[strings.TrimSpace(string(arch))]
		if platform == "linux/" {
			return fmt.Errorf("kind node %s runs %s, which kates does not know", node, strings.TrimSpace(string(arch)))
		}
		if out, err := importImagesFn(ctx, archive, "docker", "exec", "--privileged", "-i", node,
			"ctr", "--namespace=k8s.io", "images", "import", "--platform", platform, "--digests", "--snapshotter=overlayfs", "-"); err != nil {
			return fmt.Errorf("importing %s into %s: %w: %s", archive, node, err, lastLine(string(out)))
		}
	}
	return nil
}

// importImagesFn runs a command with archive as its stdin; tests replace it.
var importImagesFn = func(ctx context.Context, archive, name string, args ...string) ([]byte, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = f
	return cmd.CombinedOutput()
}
