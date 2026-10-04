package cmd

import (
	"context"
	"errors"
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
// isKind in deploy.go); every other cluster keeps the published image.
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
	// localNativeMake is the target that builds and loads it.
	localNativeMake = "make kates-image-native-local"
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

// ensureLocalNativeImage makes sure kates:native-local is on the kind node
// before the chart that pins it (pullPolicy: Never) is installed.
//
// Order matters: the host's Docker is the source of truth when it has the
// image, because that is the build you just made and the node may be holding
// an older copy. Only when the host has nothing do we ask the node, which
// covers the case where the image was loaded earlier and the local Docker has
// since been pruned.
//
// It never builds. A native compile is minutes of GraalVM and needs ~8GB for
// the compiler; starting one inside `kates deploy` because an image was
// missing would turn a deploy into something you cannot predict the length
// of. Refusing early with the command is faster than an ErrImageNeverPull
// nobody reads until the next morning.
// It returns the image reference the chart should be pinned to, so the caller
// sets the tag explicitly rather than trusting the overlay to have named the
// one that exists.
func ensureLocalNativeImage(ctx context.Context, kubeContext string) (string, error) {
	cluster := kindClusterName(kubeContext)
	if cluster == "" {
		return "", fmt.Errorf("cannot tell which kind cluster %q refers to", kubeContext)
	}

	for _, image := range localNativeCandidates {
		if !localImageOnHost(ctx, image) {
			continue
		}
		dl.Printf("    - Backend image: %s (local build) → loading into kind cluster %q...\n", image, cluster)
		if err := runExecFn(ctx, "kind", "load", "docker-image", image, "--name", cluster); err != nil {
			return "", fmt.Errorf("loading %s into kind cluster %q: %w", image, cluster, err)
		}
		return image, nil
	}

	for _, image := range localNativeCandidates {
		if localImageOnNode(ctx, cluster, image) {
			dl.Printf("    - Backend image: %s (already on the node, not in local Docker)\n", image)
			return image, nil
		}
	}

	return "", errors.New(localImageMissing(cluster))
}

// localImageOnHost reports whether the local Docker daemon has the image.
func localImageOnHost(ctx context.Context, image string) bool {
	_, err := runExecOutputFn(ctx, "docker", "image", "inspect", image, "--format", "{{.Id}}")
	return err == nil
}

// localImageOnNode reports whether the kind node's container runtime already
// has the image. kind nodes ship crictl, and the node is a container named
// <cluster>-control-plane.
func localImageOnNode(ctx context.Context, cluster, image string) bool {
	out, err := runExecOutputFn(ctx, "docker", "exec", cluster+"-control-plane",
		"crictl", "images", "-q", "docker.io/library/"+image)
	return err == nil && strings.TrimSpace(string(out)) != ""
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

// localImageMissing is the refusal: what is missing, why deploy will not make
// it, and the one command that fixes it.
func localImageMissing(cluster string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "no local backend image (%s) in Docker or on the kind node %q.\n",
		strings.Join(localNativeCandidates, " or "), cluster)
	b.WriteString("  A local deploy runs the backend you built, not a published image — and\n")
	b.WriteString("  the chart pins it with pullPolicy: Never, so there is nothing to pull.\n")
	b.WriteString("  Build it once (a native compile, several minutes, ~8GB for the compiler):\n\n")
	fmt.Fprintf(&b, "    %s\n\n", localNativeMake)
	b.WriteString("  Then run kates deploy again.")
	return b.String()
}

// katesChartValues returns the values files the backend chart is installed
// with. On kind the native-local overlay comes last so its image, resources
// and probes win over the kind overlay, while the kind overlay's NodePort
// service, tolerations and disabled production features survive.
func (dc *deployContext) katesChartValues() []string {
	args := []string{"-f", dc.valuesFile, "-f", dc.chartOverlay("charts/kates")}
	if dc.isKind {
		args = append(args, "-f", localNativeValues)
	}
	return args
}
