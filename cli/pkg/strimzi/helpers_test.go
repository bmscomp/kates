package strimzi

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vendoredChart is the pin's tarball relative to this package directory,
// used by the tests that can run against the real thing.
const vendoredChart = "../../../charts/strimzi-operator/charts/strimzi-kafka-operator-1.1.0.tgz"

// imageMap110 is templates/_kafka_image_map.tpl of the vendored 1.1.0 chart.
const imageMap110 = `{{/* vim: set filetype=mustache: */}}

{{/* This file is generated in helm-charts/Makefile */}}
{{/* DO NOT EDIT BY HAND                            */}}

{{/* Generate the kafka image map */}}
{{- define "strimzi.kafka.image.map" }}
            - name: STRIMZI_DEFAULT_KAFKA_EXPORTER_IMAGE
              value: {{ template "strimzi.image" (merge . (dict "key" "kafkaExporter" "tagSuffix" "-kafka-4.3.0")) }}
            - name: STRIMZI_DEFAULT_CRUISE_CONTROL_IMAGE
              value: {{ template "strimzi.image" (merge . (dict "key" "cruiseControl" "tagSuffix" "-kafka-4.3.0")) }}
            - name: STRIMZI_KAFKA_IMAGES
              value: |
                4.2.0={{ template "strimzi.image" (merge . (dict "key" "kafka" "tagSuffix" "-kafka-4.2.0")) }}
                4.2.1={{ template "strimzi.image" (merge . (dict "key" "kafka" "tagSuffix" "-kafka-4.2.1")) }}
                4.3.0={{ template "strimzi.image" (merge . (dict "key" "kafka" "tagSuffix" "-kafka-4.3.0")) }}
            - name: STRIMZI_KAFKA_CONNECT_IMAGES
              value: |
                4.2.0={{ template "strimzi.image" (merge . (dict "key" "kafkaConnect" "tagSuffix" "-kafka-4.2.0")) }}
                4.2.1={{ template "strimzi.image" (merge . (dict "key" "kafkaConnect" "tagSuffix" "-kafka-4.2.1")) }}
                4.3.0={{ template "strimzi.image" (merge . (dict "key" "kafkaConnect" "tagSuffix" "-kafka-4.3.0")) }}
            - name: STRIMZI_KAFKA_MIRROR_MAKER_2_IMAGES
              value: |
                4.2.0={{ template "strimzi.image" (merge . (dict "key" "kafkaMirrorMaker2" "tagSuffix" "-kafka-4.2.0")) }}
                4.2.1={{ template "strimzi.image" (merge . (dict "key" "kafkaMirrorMaker2" "tagSuffix" "-kafka-4.2.1")) }}
                4.3.0={{ template "strimzi.image" (merge . (dict "key" "kafkaMirrorMaker2" "tagSuffix" "-kafka-4.3.0")) }}
{{- end -}}
`

// imageMapFor renders a template of the 1.1.0 shape for any set of Kafka
// versions.
func imageMapFor(versions ...string) string {
	var b strings.Builder
	b.WriteString("{{- define \"strimzi.kafka.image.map\" }}\n")
	for _, block := range []struct{ env, key string }{
		{"STRIMZI_KAFKA_IMAGES", "kafka"},
		{"STRIMZI_KAFKA_CONNECT_IMAGES", "kafkaConnect"},
		{"STRIMZI_KAFKA_MIRROR_MAKER_2_IMAGES", "kafkaMirrorMaker2"},
	} {
		fmt.Fprintf(&b, "            - name: %s\n              value: |\n", block.env)
		for _, v := range versions {
			fmt.Fprintf(&b, "                %s={{ template \"strimzi.image\" (merge . (dict \"key\" \"%s\" \"tagSuffix\" \"-kafka-%s\")) }}\n", v, block.key, v)
		}
	}
	b.WriteString("{{- end -}}\n")
	return b.String()
}

// crdVersion is one entry of a CRD's spec.versions.
type crdVersion struct {
	name            string
	served, storage bool
}

// kafkaCRDFor renders a crds/040-Crd-kafka.yaml with the given versions.
func kafkaCRDFor(name string, versions ...crdVersion) string {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: %s\n  labels:\n    app: strimzi\nspec:\n  group: kafka.strimzi.io\n  names:\n    kind: Kafka\n    plural: kafkas\n  scope: Namespaced\n  versions:\n", name)
	for _, v := range versions {
		fmt.Fprintf(&b, "    - name: %s\n      served: %v\n      storage: %v\n      subresources:\n        status: {}\n      schema:\n        openAPIV3Schema:\n          type: object\n", v.name, v.served, v.storage)
	}
	return b.String()
}

func chartYAMLFor(version, appVersion string) string {
	return fmt.Sprintf("apiVersion: v2\nappVersion: %s\ndescription: 'Strimzi: Apache Kafka running on Kubernetes'\nname: strimzi-kafka-operator\nversion: %s\n", appVersion, version)
}

func valuesYAMLFor(tag string) string {
	return fmt.Sprintf("# Default values for strimzi-kafka-operator.\nwatchNamespaces: []\nwatchAnyNamespace: false\n\ndefaultImageRegistry: quay.io\ndefaultImageRepository: strimzi\ndefaultImageTag: %s\n\nimage:\n  registry: \"\"\n", tag)
}

// chart110Files is a synthetic tarball content shaped like the 1.1.0 chart.
func chart110Files() map[string]string {
	return map[string]string{
		"Chart.yaml":                      chartYAMLFor("1.1.0", "1.1.0"),
		"values.yaml":                     valuesYAMLFor("1.1.0"),
		"templates/_kafka_image_map.tpl":  imageMap110,
		"templates/_helpers.tpl":          "{{- define \"strimzi.name\" -}}strimzi{{- end -}}\n",
		"crds/040-Crd-kafka.yaml":         kafkaCRDFor(KafkaCRDName, crdVersion{"v1", true, true}),
		"crds/041-Crd-kafkaconnect.yaml":  kafkaCRDFor("kafkaconnects.kafka.strimzi.io", crdVersion{"v1", true, true}),
		"crds/043-Crd-kafkatopic.yaml":    kafkaCRDFor("kafkatopics.kafka.strimzi.io", crdVersion{"v1", true, true}),
		"files/grafana-dashboards/x.json": "{}",
		"README.md":                       "# strimzi\n",
	}
}

// chart051Files is a synthetic tarball shaped like the 0.51.0 chart: v1
// served, v1beta2 stored, window 4.1.0 4.1.1 4.2.0.
func chart051Files() map[string]string {
	return map[string]string{
		"Chart.yaml":                     chartYAMLFor("0.51.0", "0.51.0"),
		"values.yaml":                    valuesYAMLFor("0.51.0"),
		"templates/_kafka_image_map.tpl": imageMapFor("4.1.0", "4.1.1", "4.2.0"),
		"crds/040-Crd-kafka.yaml":        kafkaCRDFor(KafkaCRDName, crdVersion{"v1beta2", true, true}, crdVersion{"v1", true, false}),
		"crds/041-Crd-kafkaconnect.yaml": kafkaCRDFor("kafkaconnects.kafka.strimzi.io", crdVersion{"v1beta2", true, true}, crdVersion{"v1", true, false}),
	}
}

// buildChartTgz writes a chart tarball with every file under the
// strimzi-kafka-operator/ directory, as helm package produces it.
func buildChartTgz(t *testing.T, files map[string]string) string {
	t.Helper()
	return buildChartTgzNamed(t, filepath.Join(t.TempDir(), "strimzi-kafka-operator-test.tgz"), ChartName, files)
}

func buildChartTgzNamed(t *testing.T, path, rootDir string, files map[string]string) string {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	// a directory entry first, as helm package writes them
	if err := tw.WriteHeader(&tar.Header{Name: rootDir + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		hdr := &tar.Header{Name: rootDir + "/" + name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []interface{ Close() error }{tw, gz, f} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// fakeRunner serves canned output keyed by the full command line and records
// every call. A hook runs before the lookup, for side effects such as helm
// pull writing a file.
type fakeRunner struct {
	t       *testing.T
	outputs map[string]string
	errors  map[string]error
	hooks   map[string]func()
	calls   []string
}

func newFakeRunner(t *testing.T) *fakeRunner {
	return &fakeRunner{t: t, outputs: map[string]string{}, errors: map[string]error{}, hooks: map[string]func(){}}
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	key := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, key)
	if hook, ok := f.hooks[key]; ok {
		hook()
	}
	if err, ok := f.errors[key]; ok {
		return "", err
	}
	out, ok := f.outputs[key]
	if !ok {
		return "", fmt.Errorf("fakeRunner: unexpected command %q", key)
	}
	return out, nil
}

func (f *fakeRunner) assertCalls(want ...string) {
	f.t.Helper()
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		f.t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
