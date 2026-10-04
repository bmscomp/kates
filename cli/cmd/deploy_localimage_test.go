package cmd

import (
	"context"
	"strings"
	"testing"
)

func TestKindClusterName(t *testing.T) {
	for ctxName, want := range map[string]string{
		"kind-panda": "panda",
		"kind-kates": "kates",
		" kind-x ":   "x",
	} {
		if got := kindClusterName(ctxName); got != want {
			t.Errorf("kindClusterName(%q) = %q, want %q", ctxName, got, want)
		}
	}
}

// A local deploy must run the image you built. The published tag is right for
// a real cluster and wrong for kind — charts/kates asks for
// ghcr.io/bmscomp/kates:<appVersion>, and when appVersion is ahead of the
// registry (as 1.22.0 was) that is an ImagePullBackOff on a tag that does not
// exist.
func TestKatesChartValuesUsesTheLocalNativeImageOnKind(t *testing.T) {
	dc := &deployContext{
		valuesFile:   "values-dev.yaml",
		chartOverlay: func(dir string) string { return dir + "/values-kind.yaml" },
		isKind:       true,
	}
	got := strings.Join(dc.katesChartValues(), " ")
	if !strings.Contains(got, localNativeValues) {
		t.Errorf("a kind deploy must use %s: %s", localNativeValues, got)
	}
	// Last wins in Helm: the native overlay's image and sizing must override
	// the kind overlay's, not the other way round.
	if strings.Index(got, "values-kind.yaml") > strings.Index(got, localNativeValues) {
		t.Errorf("the native overlay must come after the kind overlay: %s", got)
	}

	// Anywhere else, the published image stands.
	dc.isKind = false
	dc.chartOverlay = func(dir string) string { return dir + "/values-generic.yaml" }
	if got := strings.Join(dc.katesChartValues(), " "); strings.Contains(got, localNativeValues) {
		t.Errorf("a non-kind deploy must keep the published image: %s", got)
	}
}

func TestEnsureLocalNativeImage(t *testing.T) {
	origOut, origExec := runExecOutputFn, runExecFn
	defer func() { runExecOutputFn, runExecFn = origOut, origExec }()

	var loaded []string
	runExecFn = func(_ context.Context, name string, args ...string) error {
		loaded = append(loaded, name+" "+strings.Join(args, " "))
		return nil
	}
	// hostHas answers `docker image inspect` for exactly one image.
	hostHas := func(image string) func(context.Context, string, ...string) ([]byte, error) {
		return func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if len(args) > 2 && args[0] == "image" && args[2] == image {
				return []byte("sha256:abc"), nil
			}
			return nil, context.Canceled
		}
	}

	// The working-tree build is preferred and loaded onto the node.
	runExecOutputFn = hostHas(localNativeImage)
	got, err := ensureLocalNativeImage(context.Background(), "kind-panda")
	if err != nil {
		t.Fatalf("image present should not fail: %v", err)
	}
	if got != localNativeImage {
		t.Errorf("chose %q, want %q", got, localNativeImage)
	}
	if len(loaded) != 1 || !strings.Contains(loaded[0], "kind load docker-image "+localNativeImage+" --name panda") {
		t.Errorf("should have loaded the image into the named cluster: %v", loaded)
	}

	// Only `make kates-native` was run: that image is native and local too,
	// and which target you happened to use must not decide whether the
	// backend starts.
	loaded = nil
	runExecOutputFn = hostHas("kates:native")
	got, err = ensureLocalNativeImage(context.Background(), "kind-panda")
	if err != nil {
		t.Fatalf("kates:native should be accepted: %v", err)
	}
	if got != "kates:native" {
		t.Errorf("chose %q, want kates:native", got)
	}

	// Not in Docker but already on the node → accepted, nothing loaded.
	loaded = nil
	runExecOutputFn = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "exec" {
			return []byte("abc123\n"), nil
		}
		return nil, context.Canceled
	}
	if _, err := ensureLocalNativeImage(context.Background(), "kind-panda"); err != nil {
		t.Fatalf("image on the node should not fail: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("nothing to load when the node already has it: %v", loaded)
	}

	// Nowhere → refused before Helm, naming the command.
	runExecOutputFn = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, context.Canceled
	}
	_, err = ensureLocalNativeImage(context.Background(), "kind-panda")
	if err == nil {
		t.Fatal("a missing image must refuse rather than install a chart that cannot pull")
	}
	for _, want := range []string{localNativeImage, "panda", localNativeMake, "pullPolicy: Never"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must mention %q:\n%s", want, err)
		}
	}
}

// The tag the chart is pinned to must be the one that exists, not the one the
// overlay hard-codes: an ErrImagePull means something asked a registry for an
// image that was supposed to be local, and pullPolicy is what prevents that.
func TestLocalImageArgs(t *testing.T) {
	got := strings.Join(localImageArgs("kates:native"), " ")
	for _, want := range []string{
		"image.repository=kates",
		"image.tag=native",
		"image.pullPolicy=Never",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
}
