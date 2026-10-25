package cmd

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/bmscomp/kates/cli/pkg/detect"
	"gopkg.in/yaml.v3"
)

// Pulling every image from one mirror registry, for a cluster with no route
// to the public ones.
//
// KATES_IMAGE_REGISTRY names the mirror, and `kates charts push` fills it
// from a bundle. Each image goes there under two names:
//
//	<mirror>/<reference as written>   quay.io/strimzi/operator:1.2.0 → mirror/quay.io/strimzi/operator:1.2.0
//	<mirror>/<reference minus host>   quay.io/strimzi/operator:1.2.0 → mirror/strimzi/operator:1.2.0
//
// Our charts and most others put a registry in front of the reference, which
// is the first name; kube-prometheus-stack replaces the registry, which is
// the second. Each chart takes the mirror through its own key, so deploy and
// migrate set each one's (mirrorSets) on every helm upgrade and template,
// at the helm seam rather than at each call site (withImageMirror).

// imageRegistry is KATES_IMAGE_REGISTRY, the mirror every image is pulled
// from, or "" to pull each from its own registry.
func imageRegistry() string {
	return strings.TrimSuffix(os.Getenv("KATES_IMAGE_REGISTRY"), "/")
}

// mirrorSet is one value a chart takes the mirror through: key set to the
// mirror followed by suffix, or with fromValues, to the mirror followed by
// "/" and the key's default in the chart's values.yaml.
type mirrorSet struct {
	key, suffix string
	fromValues  bool
}

// prefixed sets key to <mirror>/<its default>.
func prefixed(key string) mirrorSet { return mirrorSet{key: key, fromValues: true} }

// mirrorSets are the values that point each chart, by its directory or
// third-party name, at the mirror. A chart left out runs no image of its
// own. `kates charts push` renders every chart with them and refuses a
// mirror an image would bypass, so a chart that changes how it takes a
// registry fails there, not on the cluster.
var mirrorSets = map[string][]mirrorSet{
	"kates":           {{key: "global.imageRegistry"}},
	"kafka-ui":        {{key: "global.imageRegistry"}},
	"connect-cluster": {{key: "global.imageRegistry"}},
	"mirror-maker2":   {{key: "global.imageRegistry"}},
	"cdc-postgresql":  {{key: "global.imageRegistry"}},
	// kube-prometheus-stack's global.imageRegistry replaces each image's
	// registry: the second name.
	"monitoring": {{key: "global.imageRegistry"}},
	// The Litmus subchart takes no registry; its repositories are prefixed.
	"kates-chaos": {
		{key: "global.imageRegistry"},
		prefixed("litmus-core.operator.image.repository"),
		prefixed("litmus-core.runner.image.repository"),
		prefixed("litmus-core.exporter.image.repository"),
	},
	// The built legacy image takes legacyImageRegistry; apache/kafka the global.
	"legacy-kafka": {{key: "global.imageRegistry"}, prefixed("legacyImageRegistry")},
	// Strimzi's registry replaces quay.io, the registry of all its images.
	"strimzi-operator": {
		{key: "strimzi-kafka-operator.defaultImageRegistry", suffix: "/quay.io"},
		prefixed("testImages.kubectl"),
	},
	"kafka-cluster":     {{key: "tests.imageRegistry"}, prefixed("global.imageRegistry")},
	"apicurio-registry": {prefixed("image.registry")},
	"cert-manager": {
		{key: "image.registry"}, {key: "webhook.image.registry"}, {key: "cainjector.image.registry"},
		{key: "acmesolver.image.registry"}, {key: "startupapicheck.image.registry"},
	},
	// global.image.registry replaces reg.kyverno.io in every Kyverno image;
	// the test and cleanup images keep their own registries.
	"kyverno": {
		{key: "global.image.registry", suffix: "/reg.kyverno.io"},
		{key: "test.image.registry", suffix: "/ghcr.io"},
		{key: "webhooksCleanup.image.registry", suffix: "/registry.k8s.io"},
	},
}

// withImageMirror returns helm args with the --set values that point the
// chart they install or render at mirror. Every caller names the release
// and the chart before any flag that takes a value, so the chart is the
// second word after the subcommand that is not a flag.
func withImageMirror(mirror string, args []string) []string {
	if mirror == "" || len(args) == 0 || !slices.Contains([]string{"upgrade", "install", "template"}, args[0]) {
		return args
	}
	var words []string
	for _, a := range args[1:] {
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
		if len(words) == 2 {
			break
		}
	}
	if len(words) < 2 {
		return args
	}
	sets := mirrorSets[chartName(words[1])]
	if len(sets) == 0 {
		return args
	}
	out := slices.Clip(args)
	for _, s := range sets {
		value := mirror + s.suffix
		if s.fromValues {
			def := chartDefault(words[1], s.key)
			if def == "" {
				continue
			}
			value = mirror + "/" + def
		}
		out = append(out, "--set", s.key+"="+value)
	}
	return out
}

// chartName is the directory or third-party name of a helm chart argument:
// a chart directory, a pulled <name>-<version>.tgz, or <repo>/<name>.
func chartName(chart string) string {
	base := filepath.Base(chart)
	if name, ok := strings.CutSuffix(base, ".tgz"); ok {
		if i := strings.LastIndex(name, "-"); i > 0 {
			return name[:i]
		}
		return name
	}
	return base
}

// chartDefault is the string at the dotted key of a chart directory's
// values.yaml, or "" when there is none.
func chartDefault(chartDir, key string) string {
	data, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
	if err != nil {
		return ""
	}
	var node any
	if yaml.Unmarshal(data, &node) != nil {
		return ""
	}
	for _, k := range strings.Split(key, ".") {
		m, ok := node.(map[string]any)
		if !ok {
			return ""
		}
		node = m[k]
	}
	s, _ := node.(string)
	return s
}

// mirrorNames are the two names an image goes under in mirror: the
// reference as written, and without its registry host, which is the same
// for a reference that names none.
func mirrorNames(mirror, ref string) []string {
	names := []string{mirror + "/" + ref}
	if host, rest, ok := strings.Cut(ref, "/"); ok && (strings.ContainsAny(host, ".:") || host == "localhost") {
		names = append(names, mirror+"/"+rest)
	}
	return names
}

// pinImageMirrorOnce pins KATES_IMAGE_REGISTRY once per process, before any
// command runs (rootCmd).
var pinImageMirrorOnce sync.Once

// pinImageMirror points every helm upgrade and template that goes through
// the helm seams at mirror, and the pre-flight's probe pods too, and returns
// the function that restores them, as pinKubeContext does for the kube
// context.
func pinImageMirror(mirror string) (unpin func()) {
	if mirror == "" {
		return func() {}
	}
	probeRegistry := detect.ProbeImageRegistry
	detect.ProbeImageRegistry = mirror
	helmFn, procFn := runHelmFn, runProcFn
	runHelmFn = func(ctx context.Context, args ...string) error {
		return helmFn(ctx, withImageMirror(mirror, args)...)
	}
	// kates migrate runs helm through defaultRunner.
	runProcFn = func(ctx context.Context, stdin, name string, args ...string) (string, error) {
		if name == "helm" {
			args = withImageMirror(mirror, args)
		}
		return procFn(ctx, stdin, name, args...)
	}
	return func() { runHelmFn, runProcFn, detect.ProbeImageRegistry = helmFn, procFn, probeRegistry }
}
