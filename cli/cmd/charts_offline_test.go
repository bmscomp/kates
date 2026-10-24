package cmd

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/output"
)

func TestImagesInManifests(t *testing.T) {
	manifests := `
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      initContainers:
        - name: init
          image: busybox:1.36
      containers:
        - name: operator
          image: quay.io/strimzi/operator:1.2.0
          env:
            - name: STRIMZI_DEFAULT_TOPIC_OPERATOR_IMAGE
              value: quay.io/strimzi/operator:1.2.0
            - name: STRIMZI_DEFAULT_KANIKO_EXECUTOR_IMAGE
              value: quay.io/strimzi/kaniko-executor:1.2.0
            - name: STRIMZI_KAFKA_IMAGES
              value: |
                4.2.0=quay.io/strimzi/kafka:1.2.0-kafka-4.2.0
                4.3.1=quay.io/strimzi/kafka:1.2.0-kafka-4.3.1
---
apiVersion: v1
kind: ConfigMap
data:
  KATES_CHAOS_STRESS_IMAGE: "docker.io/polinux/stress:1.0.4"
  pod-delete.yaml: |
    apiVersion: litmuschaos.io/v1alpha1
    kind: ChaosExperiment
    spec:
      definition:
        image: litmuschaos/go-runner:3.28.0
  NOT_AN_IMAGE: "hello world"
---
spec:
  image: "{{ .Values.image }}"
  containers:
    - image: ghcr.io/bmscomp/kates:1.29.3-native@sha256:` + strings.Repeat("a", 64) + `
`
	got, err := imagesInManifests([]byte(manifests), "4.3.1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"busybox:1.36",
		"docker.io/polinux/stress:1.0.4",
		"ghcr.io/bmscomp/kates:1.29.3-native@sha256:" + strings.Repeat("a", 64),
		"litmuschaos/go-runner:3.28.0",
		"quay.io/strimzi/kafka:1.2.0-kafka-4.3.1",
		"quay.io/strimzi/operator:1.2.0",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("images:\n got %v\nwant %v\n(the Kafka image of the platform's version only, no Kaniko, no template leftovers)", got, want)
	}
}

func TestABundleCarriesTheThirdPartyChartsWithTheirChecksums(t *testing.T) {
	withChartBuild(t, allChartVersions)
	calls := fakeHelmPull(t)
	var helmRan [][]string
	runHelmFn = func(_ context.Context, args ...string) error {
		helmRan = append(helmRan, args)
		return nil
	}
	bundle := filepath.Join(t.TempDir(), "kates-charts")
	if err := runChartsPull(context.Background(), bundle, false, ""); err != nil {
		t.Fatal(err)
	}
	if got := callsOf(*calls, "helm pull cert-manager --repo https://charts.jetstack.io --version v1.13.3"); len(got) != 1 {
		t.Fatalf("cert-manager was not pulled at deploy's pin: %v", *calls)
	}
	if _, err := os.Stat(filepath.Join(bundle, "third-party", "SHA256SUMS")); err != nil {
		t.Fatal(err)
	}

	// Outside bundle mode, deploy installs from the repository as before.
	args, err := thirdPartyChartArgs(context.Background(), "cert-manager", true)
	if err != nil || strings.Join(args, " ") != "jetstack/cert-manager --version v1.13.3" || len(helmRan) != 2 {
		t.Fatalf("repository mode: %v %v, helm %v", args, err, helmRan)
	}

	deployChartBundle = bundle
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err != nil {
		t.Fatal(err)
	}
	helmRan = nil
	args, err = thirdPartyChartArgs(context.Background(), "kyverno", true)
	if err != nil || len(args) != 1 || args[0] != filepath.Join(bundle, "third-party", "kyverno-3.6.4.tgz") || len(helmRan) != 0 {
		t.Fatalf("bundle mode: %v %v, helm %v; want the tarball and no repository", args, err, helmRan)
	}
	setDeployFlags(t, func() { deployWithCertManager, deployWithKyverno = true, true })
	if outside := chartsOutsideBundle(); len(outside) != 0 {
		t.Fatalf("the bundle holds both, yet %v are reported outside it", outside)
	}

	if err := os.WriteFile(args[0], []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := thirdPartyChartArgs(context.Background(), "kyverno", true); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a tarball that changed on its way across must be refused: %v", err)
	}
}

func TestSaveAndLoadBundleImages(t *testing.T) {
	withChartBuild(t, allChartVersions)
	calls := fakeHelmPull(t)
	root := t.TempDir()
	if err := saveBundleImages(context.Background(), root, []string{"busybox:1.36", "postgres:16"}, "linux/amd64"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(callsOf(*calls, "docker pull")[1], " "); got != "docker pull --quiet --platform linux/amd64 postgres:16" {
		t.Fatalf("pull: %s", got)
	}
	if got := strings.Join(callsOf(*calls, "docker save")[0], " "); got != "docker save --platform linux/amd64 -o "+filepath.Join(root, "images.tar")+" busybox:1.36 postgres:16" {
		t.Fatalf("save: %s", got)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "images.txt")); string(data) != "busybox:1.36\npostgres:16\n" {
		t.Fatalf("images.txt: %q", data)
	}

	if err := os.WriteFile(filepath.Join(root, "images.tar"), []byte("tar"), 0o644); err != nil {
		t.Fatal(err)
	}
	chartRoot, chartsPublished, deployChartBundle = root, true, root
	n := len(*calls)
	if err := loadBundleImages(context.Background(), "kind-panda", true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join((*calls)[n], " "); got != "kind load image-archive "+filepath.Join(root, "images.tar")+" --name panda" {
		t.Fatalf("load: %s", got)
	}
	out := output.ResetForTesting()
	if err := loadBundleImages(context.Background(), "prod", false); err != nil || !strings.Contains(out.String(), "images.txt") || len(*calls) != n+1 {
		t.Fatalf("another cluster: %v %q; it must say which images to mirror and load nothing", err, out.String())
	}
}

func TestHarvestImagesRendersEveryWayDeployInstalls(t *testing.T) {
	withChartBuild(t, allChartVersions)
	fakeHelmPull(t)
	bundle := filepath.Join(t.TempDir(), "b")
	if err := runChartsPull(context.Background(), bundle, false, ""); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"charts/kates/values-native.yaml", "charts/kates/values-kind.yaml", "charts/kafka-cluster/Chart.yaml"} {
		if err := os.WriteFile(filepath.Join(bundle, f), []byte("name: kafka-cluster\nappVersion: \"4.3.1\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	origOutput := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = origOutput })
	var renders []string
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		renders = append(renders, strings.Join(args, " "))
		return []byte("spec:\n  containers:\n    - image: busybox:1.36\n"), nil
	}
	images, err := harvestImages(context.Background(), bundle)
	if err != nil || !slices.Equal(images, []string{"busybox:1.36"}) {
		t.Fatalf("images %v, %v", images, err)
	}
	all := strings.Join(renders, "\n")
	for _, want := range []string{
		"template kates-bundle " + filepath.Join(bundle, "charts", "kates") + " -f " + filepath.Join(bundle, "charts", "kates", "values-native.yaml"),
		"template kates-bundle " + filepath.Join(bundle, "charts", "kates") + " -f " + filepath.Join(bundle, "charts", "kates", "values-kind.yaml"),
		"template kates-bundle " + filepath.Join(bundle, "third-party", "cert-manager-v1.13.3.tgz") + " --set installCRDs=true",
		"template kates-bundle " + filepath.Join(bundle, "charts", "cdc-postgresql"),
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no render %q in:\n%s", want, all)
		}
	}
}

// Without --platform, the images are the Docker server's platform, pulled
// and saved for that one platform only.
func TestSaveBundleImagesDefaultsToTheDockerServersPlatform(t *testing.T) {
	withChartBuild(t, allChartVersions)
	calls := fakeHelmPull(t)
	origOutput := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = origOutput })
	runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) { return []byte("linux/arm64\n"), nil }
	root := t.TempDir()
	if err := saveBundleImages(context.Background(), root, []string{"busybox:1.36"}, ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(callsOf(*calls, "docker save")[0], " "); !strings.HasPrefix(got, "docker save --platform linux/arm64 ") {
		t.Fatalf("save: %s", got)
	}
}
