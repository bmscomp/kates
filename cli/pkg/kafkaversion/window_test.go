package kafkaversion

import (
	"slices"
	"strings"
	"testing"
)

// imageMap110 is templates/_kafka_image_map.tpl from the vendored
// strimzi-kafka-operator-1.1.0.tgz, verbatim.
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

// imageMapFuture is a synthetic template for a later operator with four
// entries, listed out of order and with a duplicate, whose Connect map
// carries an extra version that must not leak into the Kafka window.
const imageMapFuture = `{{- define "strimzi.kafka.image.map" }}
            - name: STRIMZI_DEFAULT_KAFKA_EXPORTER_IMAGE
              value: {{ template "strimzi.image" (merge . (dict "key" "kafkaExporter" "tagSuffix" "-kafka-4.5.0")) }}
            - name: STRIMZI_KAFKA_IMAGES
              value: |
                4.4.0={{ template "strimzi.image" (merge . (dict "key" "kafka" "tagSuffix" "-kafka-4.4.0")) }}
                4.3.1={{ template "strimzi.image" (merge . (dict "key" "kafka" "tagSuffix" "-kafka-4.3.1")) }}
                4.5.0={{ template "strimzi.image" (merge . (dict "key" "kafka" "tagSuffix" "-kafka-4.5.0")) }}
                4.3.1={{ template "strimzi.image" (merge . (dict "key" "kafka" "tagSuffix" "-kafka-4.3.1")) }}
                4.4.1={{ template "strimzi.image" (merge . (dict "key" "kafka" "tagSuffix" "-kafka-4.4.1")) }}
            - name: STRIMZI_KAFKA_CONNECT_IMAGES
              value: |
                9.9.9={{ template "strimzi.image" (merge . (dict "key" "kafkaConnect" "tagSuffix" "-kafka-9.9.9")) }}
{{- end -}}
`

// deploymentEnvValue is STRIMZI_KAFKA_IMAGES as the live Deployment carries
// it: the bare value, no marker.
const deploymentEnvValue = "4.2.0=quay.io/strimzi/kafka:1.1.0-kafka-4.2.0\n4.2.1=quay.io/strimzi/kafka:1.1.0-kafka-4.2.1\n4.3.0=quay.io/strimzi/kafka:1.1.0-kafka-4.3.0\n"

// renderedManifest is the env block of a rendered Deployment (helm template
// / kubectl get -o yaml), where the block ends at the next env entry.
const renderedManifest = `        env:
        - name: STRIMZI_NAMESPACE
          value: '*'
        - name: STRIMZI_KAFKA_IMAGES
          value: |
            4.2.0=quay.io/strimzi/kafka:1.1.0-kafka-4.2.0
            4.2.1=quay.io/strimzi/kafka:1.1.0-kafka-4.2.1
            4.3.0=quay.io/strimzi/kafka:1.1.0-kafka-4.3.0
        - name: STRIMZI_KAFKA_CONNECT_IMAGES
          value: |
            8.8.8=quay.io/strimzi/kafka:1.1.0-kafka-8.8.8
`

func versions(t *testing.T, ss ...string) []Version {
	t.Helper()
	out := make([]Version, len(ss))
	for i, s := range ss {
		out[i] = MustParse(s)
	}
	return out
}

func TestNewWindow(t *testing.T) {
	w := NewWindow(versions(t, "4.3.0", "4.2.0", "4.2.1", "4.2.0", "4.3.0")...)
	if got := w.Strings(); !slices.Equal(got, []string{"4.2.0", "4.2.1", "4.3.0"}) {
		t.Fatalf("NewWindow = %v", got)
	}
	if got := NewWindow(); len(got) != 0 {
		t.Fatalf("NewWindow() = %v, want empty", got)
	}
}

func TestParseImageMap(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		want    []string
		wantErr bool
	}{
		{name: "vendored 1.1.0 template", text: imageMap110, want: []string{"4.2.0", "4.2.1", "4.3.0"}},
		{name: "future template, four entries, unordered, connect ignored", text: imageMapFuture, want: []string{"4.3.1", "4.4.0", "4.4.1", "4.5.0"}},
		{name: "bare deployment env value", text: deploymentEnvValue, want: []string{"4.2.0", "4.2.1", "4.3.0"}},
		{name: "bare value without trailing newline", text: strings.TrimSpace(deploymentEnvValue), want: []string{"4.2.0", "4.2.1", "4.3.0"}},
		{name: "bare value with CRLF", text: strings.ReplaceAll(deploymentEnvValue, "\n", "\r\n"), want: []string{"4.2.0", "4.2.1", "4.3.0"}},
		{name: "rendered manifest, block ends at next env entry", text: renderedManifest, want: []string{"4.2.0", "4.2.1", "4.3.0"}},
		{name: "single entry", text: "4.2.0=quay.io/strimzi/kafka:1.0.1-kafka-4.2.0", want: []string{"4.2.0"}},
		{name: "empty", text: "", wantErr: true},
		{name: "marker with nothing under it", text: "- name: STRIMZI_KAFKA_IMAGES\n  value: |\n- name: OTHER\n  value: 4.2.0=x\n", wantErr: true},
		{name: "only connect map", text: "- name: STRIMZI_KAFKA_CONNECT_IMAGES\n  value: |\n    4.2.0=x\n", want: []string{"4.2.0"}},
		{name: "not a map", text: "hello\nworld\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseImageMap(tt.text)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseImageMap = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseImageMap: %v", err)
			}
			if !slices.Equal(got.Strings(), tt.want) {
				t.Fatalf("ParseImageMap = %v, want %v", got.Strings(), tt.want)
			}
		})
	}
}

// The "only connect map" case above documents a limit rather than a
// feature: with no STRIMZI_KAFKA_IMAGES marker the whole text is read as a
// bare value, so a caller must pass either a bare value or a text that
// contains the Kafka marker.

const deploymentJSON = `{
  "apiVersion": "apps/v1",
  "kind": "Deployment",
  "metadata": {"name": "strimzi-cluster-operator", "namespace": "strimzi-operator"},
  "spec": {
    "template": {
      "spec": {
        "containers": [
          {
            "name": "strimzi-cluster-operator",
            "image": "quay.io/strimzi/operator:1.1.0",
            "env": [
              {"name": "STRIMZI_NAMESPACE", "value": "*"},
              {"name": "STRIMZI_KAFKA_IMAGES", "value": "4.2.0=quay.io/strimzi/kafka:1.1.0-kafka-4.2.0\n4.2.1=quay.io/strimzi/kafka:1.1.0-kafka-4.2.1\n4.3.0=quay.io/strimzi/kafka:1.1.0-kafka-4.3.0\n"},
              {"name": "STRIMZI_KAFKA_CONNECT_IMAGES", "value": "7.7.7=quay.io/strimzi/kafka:1.1.0-kafka-7.7.7\n"}
            ]
          }
        ]
      }
    }
  }
}`

func TestParseDeploymentEnv(t *testing.T) {
	w, tag, err := ParseDeploymentEnv([]byte(deploymentJSON))
	if err != nil {
		t.Fatalf("ParseDeploymentEnv: %v", err)
	}
	if !slices.Equal(w.Strings(), []string{"4.2.0", "4.2.1", "4.3.0"}) {
		t.Errorf("window = %v", w)
	}
	if tag != "1.1.0" {
		t.Errorf("tag = %q, want 1.1.0", tag)
	}

	t.Run("sidecar first, registry port and digest", func(t *testing.T) {
		j := strings.Replace(deploymentJSON,
			`"containers": [`,
			`"containers": [{"name": "sidecar", "image": "busybox:1.36", "env": []},`, 1)
		j = strings.Replace(j, `"quay.io/strimzi/operator:1.1.0"`, `"localhost:5000/strimzi/operator:1.0.1@sha256:0123456789abcdef"`, 1)
		w, tag, err := ParseDeploymentEnv([]byte(j))
		if err != nil {
			t.Fatalf("ParseDeploymentEnv: %v", err)
		}
		if len(w) != 3 || tag != "1.0.1" {
			t.Errorf("window = %v, tag = %q", w, tag)
		}
	})

	errCases := map[string]string{
		"not json":          `{"spec": `,
		"no such container": strings.ReplaceAll(deploymentJSON, `"name": "strimzi-cluster-operator",`, `"name": "other",`),
		"no images env":     strings.Replace(deploymentJSON, `STRIMZI_KAFKA_IMAGES`, `STRIMZI_KAFKA_IMAGEZ`, 1),
		"empty images env":  strings.Replace(deploymentJSON, `"value": "4.2.0=quay.io/strimzi/kafka:1.1.0-kafka-4.2.0\n4.2.1=quay.io/strimzi/kafka:1.1.0-kafka-4.2.1\n4.3.0=quay.io/strimzi/kafka:1.1.0-kafka-4.3.0\n"`, `"value": ""`, 1),
	}
	for name, j := range errCases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ParseDeploymentEnv([]byte(j)); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestImageTag(t *testing.T) {
	tests := map[string]string{
		"quay.io/strimzi/operator:1.1.0":                     "1.1.0",
		"quay.io/strimzi/operator:1.1.0@sha256:abcdef":       "1.1.0",
		"quay.io/strimzi/operator@sha256:abcdef":             "",
		"localhost:5000/strimzi/operator:1.0.1":              "1.0.1",
		"localhost:5000/strimzi/operator":                    "",
		"operator:latest":                                    "latest",
		"operator":                                           "",
		"quay.io/strimzi/kafka:1.1.0-kafka-4.3.0":            "1.1.0-kafka-4.3.0",
		"registry.example.com:443/ns/sub/operator:1.2.0-rc1": "1.2.0-rc1",
		"": "",
	}
	for ref, want := range tests {
		if got := ImageTag(ref); got != want {
			t.Errorf("ImageTag(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestWindowAccessors(t *testing.T) {
	w := NewWindow(versions(t, "4.2.0", "4.2.1", "4.3.0")...)
	newest, ok := w.Newest()
	if !ok || newest != MustParse("4.3.0") {
		t.Errorf("Newest = %v, %v", newest, ok)
	}
	if _, ok := (Window{}).Newest(); ok {
		t.Error("empty window has a newest")
	}
	if _, ok := (Window)(nil).Newest(); ok {
		t.Error("nil window has a newest")
	}
	for _, s := range []string{"4.2.0", "4.2.1", "4.3.0"} {
		if !w.Supports(MustParse(s)) {
			t.Errorf("Supports(%s) = false", s)
		}
	}
	for _, s := range []string{"4.1.2", "4.2.2", "4.4.0", "3.9.1"} {
		if w.Supports(MustParse(s)) {
			t.Errorf("Supports(%s) = true", s)
		}
	}
	if got := w.String(); got != "4.2.0 4.2.1 4.3.0" {
		t.Errorf("String = %q", got)
	}
	if got := (Window{}).String(); got != "" {
		t.Errorf("empty String = %q", got)
	}
	if got := w.Strings(); !slices.Equal(got, []string{"4.2.0", "4.2.1", "4.3.0"}) {
		t.Errorf("Strings = %v", got)
	}
}

func TestMetadataFor(t *testing.T) {
	tests := []struct {
		window []string
		v      string
		want   string
	}{
		// the pin's window
		{[]string{"4.2.0", "4.2.1", "4.3.0"}, "4.3.0", "4.2"},
		{[]string{"4.2.0", "4.2.1", "4.3.0"}, "4.2.1", "4.2"},
		{[]string{"4.2.0", "4.2.1", "4.3.0"}, "4.2.0", "4.2"},
		// 1.0.x's window
		{[]string{"4.1.0", "4.1.1", "4.1.2", "4.2.0"}, "4.2.0", "4.1"},
		{[]string{"4.1.0", "4.1.1", "4.1.2", "4.2.0"}, "4.1.2", "4.1"},
		{[]string{"4.1.0", "4.1.1", "4.1.2", "4.2.0"}, "4.1.0", "4.1"},
		// a future window
		{[]string{"4.3.0", "4.4.0"}, "4.4.0", "4.3"},
		{[]string{"4.3.0", "4.4.0"}, "4.3.0", "4.3"},
		// a gap of more than one minor picks the highest line below
		{[]string{"4.1.0", "4.3.0"}, "4.3.0", "4.1"},
		// a major boundary
		{[]string{"3.9.1", "4.0.0"}, "4.0.0", "3.9"},
		// a version outside the window still gets the rule applied
		{[]string{"4.2.0", "4.2.1", "4.3.0"}, "4.4.0", "4.3"},
		{[]string{"4.2.0", "4.2.1", "4.3.0"}, "4.1.2", "4.1"},
		// empty window: the version's own line
		{nil, "4.3.0", "4.3"},
	}
	for _, tt := range tests {
		w := NewWindow(versions(t, tt.window...)...)
		if got := MetadataFor(MustParse(tt.v), w); got != tt.want {
			t.Errorf("MetadataFor(%s, %v) = %q, want %q", tt.v, w, got, tt.want)
		}
	}
}
