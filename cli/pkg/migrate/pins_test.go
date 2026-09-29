package migrate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// pinsFixture is versions.env as the repository carries it: quoted values,
// comments, and KAFKA_IMAGE built from an earlier pin.
const pinsFixture = `# Centralized chart and component version pins.
# Source this file from scripts that need version info.

STRIMZI_VERSION="1.1.0"
STRIMZI_KAFKA_VERSION="1.1.0-kafka-4.3.0"
LITMUS_CHART_VERSION="3.28.0"
KAFKA_IMAGE="quay.io/strimzi/kafka:${STRIMZI_KAFKA_VERSION}"
BOOTSTRAP="krafter-kafka-bootstrap.kafka.svc:9092"

# Local-cluster toolchain.
KIND_VERSION="v0.33.0"
`

func TestReadPins(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, PinsFile), []byte(pinsFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := ReadPins(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.StrimziVersion != "1.1.0" || p.KafkaVersion != "4.3.0" || p.KafkaImage != "quay.io/strimzi/kafka:1.1.0-kafka-4.3.0" {
		t.Errorf("pins = %+v", p)
	}
	if p.All["KIND_VERSION"] != "v0.33.0" || p.All["BOOTSTRAP"] != "krafter-kafka-bootstrap.kafka.svc:9092" || len(p.All) != 6 {
		t.Errorf("All = %v", p.All)
	}

	_, err = ReadPins(filepath.Join(root, "nowhere"))
	if err == nil || !strings.Contains(err.Error(), "versions.env not found") || !strings.Contains(err.Error(), "repository root") {
		t.Errorf("missing file error = %v", err)
	}
}

// TestReadPinsRepository reads the repository's own versions.env, so the
// parser is proven against the real file and not only a fixture.
func TestReadPinsRepository(t *testing.T) {
	root := "../../.."
	if _, err := os.Stat(filepath.Join(root, PinsFile)); err != nil {
		t.Skipf("repository pins not available: %v", err)
	}
	p, err := ReadPins(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.StrimziVersion == "" || p.KafkaVersion == "" || !strings.HasPrefix(p.KafkaImage, "quay.io/strimzi/kafka:") {
		t.Errorf("pins = %+v", p)
	}
	if !strings.HasSuffix(p.KafkaImage, "-kafka-"+p.KafkaVersion) || !strings.Contains(p.KafkaImage, ":"+p.StrimziVersion+"-") {
		t.Errorf("KAFKA_IMAGE %q does not agree with STRIMZI_VERSION %q / Kafka %q", p.KafkaImage, p.StrimziVersion, p.KafkaVersion)
	}
}

func TestParsePins(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		want    Pins
		wantErr string
	}{
		{
			name: "unquoted, export, single quotes, $VAR, trailing comment",
			text: "export STRIMZI_VERSION=1.0.1\nSTRIMZI_KAFKA_VERSION='1.0.1-kafka-4.2.0'\nKAFKA_IMAGE=quay.io/strimzi/kafka:$STRIMZI_KAFKA_VERSION # the client\n",
			want: Pins{StrimziVersion: "1.0.1", KafkaVersion: "4.2.0", KafkaImage: "quay.io/strimzi/kafka:1.0.1-kafka-4.2.0"},
		},
		{name: "empty", text: "\n# only comments\n", wantErr: "no pins"},
		{name: "not an assignment", text: "STRIMZI_VERSION\n", wantErr: "line 1"},
		{name: "forward reference", text: "KAFKA_IMAGE=\"quay.io/strimzi/kafka:${STRIMZI_KAFKA_VERSION}\"\nSTRIMZI_KAFKA_VERSION=\"1.1.0-kafka-4.3.0\"\n", wantErr: "not defined above it"},
		{name: "missing STRIMZI_VERSION", text: "STRIMZI_KAFKA_VERSION=\"1.1.0-kafka-4.3.0\"\nKAFKA_IMAGE=\"x\"\n", wantErr: "STRIMZI_VERSION is not set"},
		{name: "missing KAFKA_IMAGE", text: "STRIMZI_VERSION=\"1.1.0\"\nSTRIMZI_KAFKA_VERSION=\"1.1.0-kafka-4.3.0\"\n", wantErr: "KAFKA_IMAGE is not set"},
		{name: "bad kafka tag", text: "STRIMZI_VERSION=\"1.1.0\"\nSTRIMZI_KAFKA_VERSION=\"1.1.0\"\nKAFKA_IMAGE=\"x\"\n", wantErr: "-kafka-x.y.z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePins(tt.text)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParsePins = %+v, %v; want an error mentioning %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got.All = nil
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParsePins = %+v, want %+v", got, tt.want)
			}
		})
	}
}
