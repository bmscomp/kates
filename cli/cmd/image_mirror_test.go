package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/output"
)

func TestWithImageMirrorSetsEachChartsOwnKey(t *testing.T) {
	charts := t.TempDir()
	strimzi := filepath.Join(charts, "strimzi-operator")
	if err := os.MkdirAll(strimzi, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strimzi, "values.yaml"), []byte("testImages:\n  kubectl: ghcr.io/bmscomp/kates-tester:1.29.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const m = "registry.internal:5000/kates"
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"upgrade", "--install", "kates", filepath.Join(charts, "kates"), "-n", "kates"},
			"--set global.imageRegistry=" + m},
		{[]string{"upgrade", "--install", "strimzi-operator", strimzi, "-n", "strimzi-operator"},
			"--set strimzi-kafka-operator.defaultImageRegistry=" + m + "/quay.io --set testImages.kubectl=" + m + "/ghcr.io/bmscomp/kates-tester:1.29.3"},
		{[]string{"upgrade", "--install", "kyverno", "/b/third-party/kyverno-3.6.4.tgz", "-n", "kyverno"},
			"--set global.image.registry=" + m + "/reg.kyverno.io --set test.image.registry=" + m + "/curlimages --set webhooksCleanup.image.registry=" + m + "/registry.k8s.io"},
		{[]string{"upgrade", "--install", "cert-manager", "jetstack/cert-manager", "--version", "v1.13.3"},
			"--set image.registry=" + m + " --set webhook.image.registry=" + m},
		{[]string{"template", "kates-bundle", filepath.Join(charts, "kafka-ui")},
			"--set global.imageRegistry=" + m},
	} {
		got := strings.Join(withImageMirror(m, c.args), " ")
		if !strings.HasPrefix(got, strings.Join(c.args, " ")+" ") || !strings.Contains(got, c.want) {
			t.Errorf("%v:\n got %s\nwant it followed by %s", c.args, got, c.want)
		}
	}
	// A key whose default the chart does not have is left alone.
	if got := withImageMirror(m, []string{"upgrade", "--install", "chaos", filepath.Join(charts, "kates-chaos")}); strings.Contains(strings.Join(got, " "), "litmus-core") {
		t.Errorf("no values.yaml, yet %v", got)
	}
	for _, args := range [][]string{
		{"repo", "add", "jetstack", "https://charts.jetstack.io"},
		{"upgrade", "--install", "x", filepath.Join(charts, "not-ours")},
		{"status", "kates", "-n", "kates"},
	} {
		if got := withImageMirror(m, args); !slices.Equal(got, args) {
			t.Errorf("%v changed to %v", args, got)
		}
	}
	if args := []string{"upgrade", "--install", "kates", "charts/kates"}; !slices.Equal(withImageMirror("", args), args) {
		t.Error("no mirror, yet the args changed")
	}
}

func TestMirrorNames(t *testing.T) {
	for ref, want := range map[string][]string{
		"quay.io/strimzi/operator:1.2.0": {"m/quay.io/strimzi/operator:1.2.0", "m/strimzi/operator:1.2.0"},
		"localhost/x:1":                  {"m/localhost/x:1", "m/x:1"},
		"litmuschaos/go-runner:3.28.0":   {"m/litmuschaos/go-runner:3.28.0"},
		"postgres:16":                    {"m/postgres:16"},
	} {
		if got := mirrorNames("m", ref); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", ref, got, want)
		}
	}
}

// fakeDocker answers docker image inspect for the images in local, and
// helm template with rendered.
func fakeDocker(t *testing.T, local []string, rendered func(args []string) string) {
	t.Helper()
	origOutput := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = origOutput })
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch {
		case name == "docker" && args[0] == "image":
			if slices.Contains(local, args[len(args)-1]) {
				return []byte("linux/amd64\n"), nil
			}
			return nil, errors.New("no such image")
		case name == "helm" && args[0] == "template":
			return []byte(rendered(args)), nil
		}
		return nil, errors.New("unexpected " + name)
	}
}

func TestChartsPushPushesBothNamesAndChecksCoverage(t *testing.T) {
	withChartBuild(t, allChartVersions)
	calls := fakeHelmPull(t)
	bundle := filepath.Join(t.TempDir(), "b")
	if err := runChartsPull(context.Background(), bundle, false, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "charts", "kafka-cluster", "Chart.yaml"), []byte("name: kafka-cluster\nappVersion: \"4.3.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "charts", "apicurio-registry", "values.yaml"), []byte("image:\n  registry: quay.io\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "images.tar"), []byte("tar"), 0o644); err != nil {
		t.Fatal(err)
	}
	images := "alpine:3.19\nbusybox:1.36\nbusybox:1.37\ndocker.io/polinux/stress:1.0.4\nquay.io/strimzi/operator:1.2.0\n"
	if err := os.WriteFile(filepath.Join(bundle, "images.txt"), []byte(images), 0o644); err != nil {
		t.Fatal(err)
	}
	const m = "registry.internal:5000/kates"
	// The archive lacks polinux/stress (another platform's), and every chart
	// renders the operator under the mirror's first name.
	fakeDocker(t, []string{"alpine:3.19", "busybox:1.36", "busybox:1.37", "quay.io/strimzi/operator:1.2.0"}, func(args []string) string {
		if !slices.Contains(args, "--set") {
			t.Errorf("rendered without the mirror: %v", args)
		}
		return "spec:\n  containers:\n    - image: " + m + "/quay.io/strimzi/operator:1.2.0\n"
	})
	stderr := output.ResetForTesting()
	*calls = nil
	if err := runChartsPush(context.Background(), bundle, "oci://"+m+"/"); err != nil {
		t.Fatal(err)
	}
	var pushed []string
	for _, c := range callsOf(*calls, "docker push") {
		pushed = append(pushed, c[len(c)-1])
	}
	want := []string{m + "/alpine:3.19", m + "/busybox:1.36", m + "/busybox:1.37", m + "/quay.io/strimzi/operator:1.2.0", m + "/strimzi/operator:1.2.0"}
	if !slices.Equal(pushed, want) {
		t.Fatalf("pushed\n %v\nwant\n %v", pushed, want)
	}
	if got := strings.Join(callsOf(*calls, "docker push")[0], " "); got != "docker push --quiet --platform linux/amd64 "+m+"/alpine:3.19" {
		t.Fatalf("push the archive's one platform: %s", got)
	}
	if got := callsOf(*calls, "docker load"); len(got) != 1 {
		t.Fatalf("docker load: %v", got)
	}
	if !strings.Contains(stderr.String(), "polinux/stress") {
		t.Errorf("the image the archive lacks is not named: %q", stderr.String())
	}

	// A chart that takes the mirror some other way is caught here.
	fakeDocker(t, []string{"quay.io/strimzi/operator:1.2.0"}, func([]string) string {
		return "spec:\n  containers:\n    - image: quay.io/jetstack/cert-manager-controller:v1.13.3\n    - image: " + m + "/kyverno/kyverno:v1.16.4\n"
	})
	err := runChartsPush(context.Background(), bundle, m)
	if err == nil || !strings.Contains(err.Error(), "quay.io/jetstack/cert-manager-controller:v1.13.3") || !strings.Contains(err.Error(), m+"/kyverno/kyverno:v1.16.4") {
		t.Fatalf("an image outside the mirror and one it lacks must both be refused: %v", err)
	}
}

func TestLegacyKafkaImagesBuildOnlyWhatNoRegistryHas(t *testing.T) {
	withChartBuild(t, allChartVersions)
	calls := fakeHelmPull(t)
	if err := runChartsPull(context.Background(), t.TempDir(), false, "", []string{"2.8.2"}); err == nil || !strings.Contains(err.Error(), "--images") {
		t.Fatalf("--legacy-kafka without --images: %v", err)
	}
	origProc := runProcFn
	t.Cleanup(func() { runProcFn = origProc })
	var docker []string
	runProcFn = func(_ context.Context, _, name string, args ...string) (string, error) {
		docker = append(docker, name+" "+strings.Join(args, " "))
		if slices.Contains(args, "ls") {
			return "kafka_2.13-2.8.2.jar\n", nil
		}
		return "", nil
	}
	images, built, err := legacyKafkaImages(context.Background(), []string{"3.9.1", "2.8.2"}, "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(images, []string{"apache/kafka:3.9.1", "ghcr.io/bmscomp/kates-legacy-kafka:2.8.2"}) || !slices.Equal(built, []string{"ghcr.io/bmscomp/kates-legacy-kafka:2.8.2"}) {
		t.Fatalf("images %v, built %v", images, built)
	}
	if i := slices.IndexFunc(docker, func(c string) bool { return strings.HasPrefix(c, "docker build") }); i < 0 || !strings.Contains(docker[i], "--platform linux/amd64") || !strings.Contains(docker[i], "KAFKA_VERSION=2.8.2") {
		t.Fatalf("docker build for 2.8.2 on the bundle's platform: %v", docker)
	}

	// saveBundleImages pulls the rest, not what was built here.
	fakeDocker(t, nil, nil)
	runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) { return []byte("linux/amd64\n"), nil }
	*calls = nil
	if err := saveBundleImages(context.Background(), t.TempDir(), images, built, "linux/amd64"); err != nil {
		t.Fatal(err)
	}
	if got := callsOf(*calls, "docker pull"); len(got) != 1 || got[0][len(got[0])-1] != "apache/kafka:3.9.1" {
		t.Fatalf("pulls %v, want apache/kafka:3.9.1 only", got)
	}
	if got := callsOf(*calls, "docker save"); len(got) != 1 || !slices.Contains(got[0], "ghcr.io/bmscomp/kates-legacy-kafka:2.8.2") {
		t.Fatalf("save %v", got)
	}
}

func TestPushImageCopiesAnImageDockerWillNotPush(t *testing.T) {
	origCombined, origStdin := runExecCombinedFn, runExecStdinFn
	t.Cleanup(func() { runExecCombinedFn, runExecStdinFn = origCombined, origStdin })
	var pushes []string
	runExecCombinedFn = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		pushes = append(pushes, strings.Join(args, " "))
		if len(pushes) == 1 {
			return []byte("image with reference m/curl was found but does not provide the specified platform (linux/arm64)"), errors.New("exit status 1")
		}
		return nil, nil
	}
	var build string
	runExecStdinFn = func(_ context.Context, _ string, args []string, stdin string) error {
		build = strings.Join(args, " ") + " < " + stdin
		return nil
	}
	if err := pushImage(context.Background(), "quay.io/curl/curl:8.7.1", "m/quay.io/curl/curl:8.7.1", "linux/arm64"); err != nil {
		t.Fatal(err)
	}
	if build != "build --quiet --platform linux/arm64 --pull=false -t m/quay.io/curl/curl:8.7.1 - < FROM quay.io/curl/curl:8.7.1\n" || len(pushes) != 2 || pushes[1] != "push --quiet m/quay.io/curl/curl:8.7.1" {
		t.Fatalf("build %q, pushes %v", build, pushes)
	}
	runExecCombinedFn = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("denied"), errors.New("exit status 1")
	}
	if err := pushImage(context.Background(), "a:1", "m/a:1", "linux/arm64"); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("any other failure is the error: %v", err)
	}
}
