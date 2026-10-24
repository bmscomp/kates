package cmd

import (
	"context"
	"fmt"
	"strings"
)

// The backend image on a local cluster.
//
// charts/kates points at ghcr.io/bmscomp/kates:<appVersion>, which is right
// for a real cluster and wrong for kind in two ways. The published tag lags
// the chart — appVersion moved to 1.22.0 for health endpoints the probes
// depend on while the registry's newest is 1.21.0, so a kind deploy sat in
// ImagePullBackOff on a tag that does not exist — and even when the tag is
// there, pulling somebody else's bytes onto your laptop is not what you want
// while you are changing the backend.
//
// So a local deploy runs the native image built from the working tree, which
// the chart already describes: values-native-local.yaml pins kates:native-local
// with pullPolicy Never, sized for a laptop. This applies ONLY to kind (see
// isKind in deploy.go); every other cluster runs the published native image.
const (
	// localNativeImage is the tag `make kates-image-native-local` builds and
	// loads onto the kind node, and the one values-native-local.yaml names.
	localNativeImage = "kates:native-local"
	// localNativeValues is the chart's own overlay for that image: it supplies
	// pullPolicy Never, laptop sizing and native probe timings. The TAG comes
	// from whichever candidate below is actually present, set explicitly after
	// this file, so building with `make kates-native` instead of
	// `make kates-image-native-local` is not a silent failure.
	localNativeValues = "charts/kates/values-native-local.yaml"
	// publishedNativeValues keeps the native runtime but uses the published image
	// tag and normal image pulling: what every cluster but kind runs, and what a
	// proxied kind cluster needs when no local backend image has been built.
	publishedNativeValues = "charts/kates/values-native.yaml"
)

// localNativeCandidates are the native images a local build produces, newest
// intent first: the working-tree build, then the one `make kates-native`
// leaves behind. Both are native and both live only on the node, which is
// what a local deploy wants; which target you happened to run should not
// decide whether the backend starts.
var localNativeCandidates = []string{localNativeImage, "kates:native"}

// kindClusterName derives the kind cluster's name from the kube context,
// which kind writes as "kind-<name>". Returns "" when the context is not a
// kind one — the caller only asks after isKind.
func kindClusterName(kubeContext string) string {
	return strings.TrimPrefix(strings.TrimSpace(kubeContext), "kind-")
}

// kindBackendImageArgs picks the backend image strategy for kind.
//
// Order matters: the host's Docker is the source of truth when it has the
// image, because that is the build you just made and the node may be holding
// an older copy. Only when the host has nothing do we ask the node, which
// covers the case where the image was loaded earlier and the local Docker has
// since been pruned. If neither local path is usable, the deploy falls back to
// the published native image so kind can pull it through its configured proxy.
//
// It never builds. A native compile is minutes of GraalVM and needs ~8GB for
// the compiler; starting one inside `kates deploy` because an image was
// missing would turn a deploy into something you cannot predict the length
// of. Pulling the published native image is predictable and matches the rest of
// the internet-pulled dependency stack.
func kindBackendImageArgs(ctx context.Context, kubeContext string) ([]string, error) {
	cluster := kindClusterName(kubeContext)
	if cluster == "" {
		return nil, fmt.Errorf("cannot tell which kind cluster %q refers to", kubeContext)
	}

	for _, image := range localNativeCandidates {
		if !localImageOnHost(ctx, image) {
			continue
		}
		dl.Printf("    - Backend image: %s (local build) -> loading into kind cluster %q...\n", image, cluster)
		if err := runExecFn(ctx, "kind", "load", "docker-image", image, "--name", cluster); err != nil {
			return nil, fmt.Errorf("loading %s into kind cluster %q: %w", image, cluster, err)
		}
		return append([]string{"-f", chartRootPath(localNativeValues)}, localImageArgs(image)...), nil
	}

	nodes, err := kindNodeContainers(ctx, cluster)
	if err == nil {
		for _, image := range localNativeCandidates {
			if localImageOnAllNodes(ctx, nodes, image) {
				dl.Printf("    - Backend image: %s (already on every kind node, not in local Docker)\n", image)
				return append([]string{"-f", chartRootPath(localNativeValues)}, localImageArgs(image)...), nil
			}
		}
	}

	dl.Println("    - Backend image: published native image (cluster will pull it)")
	return []string{"-f", chartRootPath(publishedNativeValues)}, nil
}

// localImageOnHost reports whether the local Docker daemon has the image.
func localImageOnHost(ctx context.Context, image string) bool {
	_, err := runExecOutputFn(ctx, "docker", "image", "inspect", image, "--format", "{{.Id}}")
	return err == nil
}

func kindNodeContainers(ctx context.Context, cluster string) ([]string, error) {
	out, err := runExecOutputFn(ctx, "kind", "get", "nodes", "--name", cluster)
	if err != nil {
		return nil, fmt.Errorf("listing kind nodes for cluster %q: %w", cluster, err)
	}
	nodes := strings.Fields(string(out))
	if len(nodes) == 0 {
		return nil, fmt.Errorf("kind cluster %q has no node containers", cluster)
	}
	return nodes, nil
}

func localImageOnAllNodes(ctx context.Context, nodes []string, image string) bool {
	for _, node := range nodes {
		if !localImageOnNode(ctx, node, image) {
			return false
		}
	}
	return true
}

// localImageOnNode reports whether one kind node's container runtime already
// has the exact image. `crictl images -q <ref>` can print unrelated image IDs on
// some versions, so use inspecti and trust only its exit code.
func localImageOnNode(ctx context.Context, node, image string) bool {
	_, err := runExecOutputFn(ctx, "docker", "exec", node,
		"crictl", "inspecti", "docker.io/library/"+image)
	return err == nil
}

// localImageArgs pins the chart to the image that was found, overriding the
// tag values-native-local.yaml hard-codes. Splitting repository from tag is
// what the chart's template expects.
func localImageArgs(image string) []string {
	repo, tag, ok := strings.Cut(image, ":")
	if !ok {
		repo, tag = image, "latest"
	}
	return []string{
		"--set-string", "image.repository=" + repo,
		"--set-string", "image.tag=" + tag,
		"--set-string", "image.pullPolicy=Never",
	}
}

// katesChartValues returns the base values files the backend chart is installed
// with. The kind-specific backend image overlay is chosen later, after checking
// whether a real local image exists.
func (dc *deployContext) katesChartValues() []string {
	args := []string{"-f", dc.valuesFile, "-f", dc.chartOverlay(chartRootPath("charts/kates"))}
	return args
}
