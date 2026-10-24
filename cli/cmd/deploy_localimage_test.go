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

func TestKatesChartValuesDoesNotChooseTheKindBackendImage(t *testing.T) {
	dc := &deployContext{
		valuesFile:   "values-dev.yaml",
		chartOverlay: func(dir string) string { return dir + "/values-kind.yaml" },
		isKind:       true,
	}
	got := strings.Join(dc.katesChartValues(), " ")
	if strings.Contains(got, localNativeValues) || strings.Contains(got, publishedNativeValues) {
		t.Errorf("base chart values should not choose the kind backend image: %s", got)
	}
	if !strings.Contains(got, "values-kind.yaml") {
		t.Errorf("a kind deploy must still include the kind overlay: %s", got)
	}

	// Anywhere else, the published image stands.
	dc.isKind = false
	dc.chartOverlay = func(dir string) string { return dir + "/values-generic.yaml" }
	if got := strings.Join(dc.katesChartValues(), " "); strings.Contains(got, localNativeValues) {
		t.Errorf("a non-kind deploy must keep the published image: %s", got)
	}
}

func installKindExecSpies(t *testing.T) (*[]string, func(func(context.Context, string, ...string) ([]byte, error))) {
	t.Helper()
	origOut, origExec := runExecOutputFn, runExecFn
	t.Cleanup(func() { runExecOutputFn, runExecFn = origOut, origExec })

	loaded := []string{}
	runExecFn = func(_ context.Context, name string, args ...string) error {
		loaded = append(loaded, name+" "+strings.Join(args, " "))
		return nil
	}
	return &loaded, func(fn func(context.Context, string, ...string) ([]byte, error)) {
		runExecOutputFn = fn
	}
}

func hostHasImage(image string) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 2 && args[0] == "image" && args[2] == image {
			return []byte("sha256:abc"), nil
		}
		return nil, context.Canceled
	}
}

func TestKindBackendImageArgsPrefersWorkingTreeBuild(t *testing.T) {
	loaded, setOutput := installKindExecSpies(t)
	setOutput(hostHasImage(localNativeImage))

	got, err := kindBackendImageArgs(context.Background(), "kind-panda")
	if err != nil {
		t.Fatalf("image present should not fail: %v", err)
	}
	gotArgs := strings.Join(got, " ")
	for _, want := range []string{localNativeValues, "image.repository=kates", "image.tag=native-local", "image.pullPolicy=Never"} {
		if !strings.Contains(gotArgs, want) {
			t.Errorf("local image args missing %q: %s", want, gotArgs)
		}
	}
	if len(*loaded) != 1 || !strings.Contains((*loaded)[0], "kind load docker-image "+localNativeImage+" --name panda") {
		t.Errorf("should have loaded the image into the named cluster: %v", *loaded)
	}
}

func TestKindBackendImageArgsAcceptsKatesNativeImage(t *testing.T) {
	_, setOutput := installKindExecSpies(t)
	setOutput(hostHasImage("kates:native"))

	got, err := kindBackendImageArgs(context.Background(), "kind-panda")
	if err != nil {
		t.Fatalf("kates:native should be accepted: %v", err)
	}
	if gotArgs := strings.Join(got, " "); !strings.Contains(gotArgs, "image.tag=native") {
		t.Errorf("chose %q, want kates:native overrides", gotArgs)
	}
}

func TestKindBackendImageArgsUsesNodeLocalImageWhenPresentEverywhere(t *testing.T) {
	loaded, setOutput := installKindExecSpies(t)
	setOutput(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "get nodes --name panda" {
			return []byte("panda-control-plane\npanda-worker\n"), nil
		}
		if len(args) > 4 && args[0] == "exec" && args[2] == "crictl" && args[3] == "inspecti" {
			return []byte("{}"), nil
		}
		return nil, context.Canceled
	})

	got, err := kindBackendImageArgs(context.Background(), "kind-panda")
	if err != nil {
		t.Fatalf("image on every node should not fail: %v", err)
	}
	if gotArgs := strings.Join(got, " "); !strings.Contains(gotArgs, localNativeValues) || !strings.Contains(gotArgs, "image.pullPolicy=Never") {
		t.Errorf("node-local image args missing expected overrides: %s", gotArgs)
	}
	if len(*loaded) != 0 {
		t.Errorf("nothing to load when every node already has it: %v", *loaded)
	}
}

func TestKindBackendImageArgsFallsBackWhenOnlySomeNodesHaveImage(t *testing.T) {
	_, setOutput := installKindExecSpies(t)
	setOutput(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "get nodes --name panda" {
			return []byte("panda-control-plane\npanda-worker\n"), nil
		}
		if len(args) > 4 && args[0] == "exec" && args[1] == "panda-control-plane" && args[2] == "crictl" && args[3] == "inspecti" {
			return []byte("{}"), nil
		}
		return nil, context.Canceled
	})

	got, err := kindBackendImageArgs(context.Background(), "kind-panda")
	if err != nil {
		t.Fatalf("partial node image should fall back to published native image: %v", err)
	}
	if gotArgs := strings.Join(got, " "); gotArgs != "-f "+publishedNativeValues {
		t.Fatalf("partial node image args = %q, want published native values", gotArgs)
	}
}

func TestKindBackendImageArgsFallsBackWhenImageMissingLocally(t *testing.T) {
	_, setOutput := installKindExecSpies(t)
	setOutput(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "kind" && strings.Join(args, " ") == "get nodes --name panda" {
			return []byte("panda-control-plane\n"), nil
		}
		return nil, context.Canceled
	})

	got, err := kindBackendImageArgs(context.Background(), "kind-panda")
	if err != nil {
		t.Fatalf("missing local image should fall back to published native image: %v", err)
	}
	if gotArgs := strings.Join(got, " "); gotArgs != "-f "+publishedNativeValues {
		t.Fatalf("missing local image args = %q, want published native values", gotArgs)
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

// Behind a mirror the local build is not used: global.imageRegistry would
// rename it, and the nodes hold it under its bare name only.
func TestKindBackendImageArgsUsesThePublishedImageBehindAMirror(t *testing.T) {
	loaded, setOutput := installKindExecSpies(t)
	setOutput(hostHasImage(localNativeImage))
	t.Setenv("KATES_IMAGE_REGISTRY", "registry.internal/kates")

	got, err := kindBackendImageArgs(context.Background(), "kind-panda")
	if err != nil || strings.Join(got, " ") != "-f "+chartRootPath(publishedNativeValues) || len(*loaded) != 0 {
		t.Fatalf("args %v, %v, loads %v; want the published native image and no load", got, err, *loaded)
	}
}
