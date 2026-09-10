package kafkaversion

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	// EnvKafkaImages is the Cluster Operator environment variable that lists
	// the Kafka versions it supports, one "x.y.z=image" per line.
	EnvKafkaImages = "STRIMZI_KAFKA_IMAGES"
	// OperatorContainerName is the name of the Cluster Operator container in
	// its Deployment.
	OperatorContainerName = "strimzi-cluster-operator"
)

// Window is the set of Kafka versions an operator supports: always sorted
// ascending, never with duplicates. Build one with NewWindow or one of the
// parsers rather than by hand.
type Window []Version

// NewWindow builds a Window from any versions, sorting and deduplicating them.
func NewWindow(vs ...Version) Window {
	w := make(Window, 0, len(vs))
	w = append(w, vs...)
	slices.SortFunc(w, Compare)
	return slices.Compact(w)
}

var (
	// imagesMarker finds the STRIMZI_KAFKA_IMAGES env entry; the word boundary
	// keeps STRIMZI_KAFKA_CONNECT_IMAGES and the MirrorMaker 2 map out.
	imagesMarker = regexp.MustCompile(`\bSTRIMZI_KAFKA_IMAGES\b`)
	// envEntryLine is the start of the next env entry in a manifest or
	// template, which ends the block.
	envEntryLine = regexp.MustCompile(`^\s*-\s*name:`)
	// versionLine is one "x.y.z=<image>" line of the map, in either shape
	// (a rendered image or a Helm template expression after the "=").
	versionLine = regexp.MustCompile(`^\s*v?(\d+\.\d+\.\d+)\s*=`)
)

// ParseImageMap extracts the Kafka versions from a STRIMZI_KAFKA_IMAGES
// statement. Two shapes are accepted:
//
//   - the bare env value as the live Deployment carries it, one
//     "4.2.0=quay.io/strimzi/kafka:1.1.0-kafka-4.2.0" per line;
//   - a manifest or the chart's templates/_kafka_image_map.tpl, where the
//     block starts at the "- name: STRIMZI_KAFKA_IMAGES" entry and ends at
//     the next "- name:" entry (or the end of the text). The Connect and
//     MirrorMaker 2 maps that follow in the same file are not read.
func ParseImageMap(text string) (Window, error) {
	lines := strings.Split(text, "\n")
	start, end := 0, len(lines)
	for i, l := range lines {
		if !imagesMarker.MatchString(l) {
			continue
		}
		start = i + 1
		for j := start; j < len(lines); j++ {
			if envEntryLine.MatchString(lines[j]) {
				end = j
				break
			}
		}
		break
	}
	var vs []Version
	for _, l := range lines[start:end] {
		m := versionLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		v, err := Parse(m[1])
		if err != nil {
			return nil, err
		}
		vs = append(vs, v)
	}
	if len(vs) == 0 {
		return nil, errors.New("no Kafka versions found under " + EnvKafkaImages)
	}
	return NewWindow(vs...), nil
}

// ParseDeploymentEnv reads an apps/v1 Deployment (as "kubectl get deployment
// -o json" prints it) and returns the Kafka window from the
// strimzi-cluster-operator container's STRIMZI_KAFKA_IMAGES env and the tag
// of that container's image (the operator version).
func ParseDeploymentEnv(deploymentJSON []byte) (Window, string, error) {
	var d struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Name  string `json:"name"`
						Image string `json:"image"`
						Env   []struct {
							Name  string `json:"name"`
							Value string `json:"value"`
						} `json:"env"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(deploymentJSON, &d); err != nil {
		return nil, "", fmt.Errorf("parse Deployment JSON: %w", err)
	}
	for _, c := range d.Spec.Template.Spec.Containers {
		if c.Name != OperatorContainerName {
			continue
		}
		for _, e := range c.Env {
			if e.Name != EnvKafkaImages {
				continue
			}
			w, err := ParseImageMap(e.Value)
			if err != nil {
				return nil, "", fmt.Errorf("container %s: %w", c.Name, err)
			}
			return w, ImageTag(c.Image), nil
		}
		return nil, "", fmt.Errorf("container %s has no %s env", c.Name, EnvKafkaImages)
	}
	return nil, "", fmt.Errorf("no container named %s in Deployment", OperatorContainerName)
}

// ImageTag returns the tag of an image reference ("1.1.0" for
// "quay.io/strimzi/operator:1.1.0"), ignoring any digest and a registry
// port. It is empty when the reference has no tag.
func ImageTag(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon < 0 || colon < slash {
		return ""
	}
	return ref[colon+1:]
}

// Newest returns the highest version in the window; ok is false when the
// window is empty.
func (w Window) Newest() (Version, bool) {
	if len(w) == 0 {
		return Version{}, false
	}
	return w[len(w)-1], true
}

// Supports reports whether v is in the window.
func (w Window) Supports(v Version) bool {
	return slices.Contains(w, v)
}

// Strings renders every version in the window, oldest first.
func (w Window) Strings() []string {
	out := make([]string, len(w))
	for i, v := range w {
		out[i] = v.String()
	}
	return out
}

// String renders the window as space-separated versions ("4.2.0 4.2.1
// 4.3.0"), the form the CLI's messages use.
func (w Window) String() string {
	return strings.Join(w.Strings(), " ")
}

// MetadataFor returns the KRaft metadata version to pin for Kafka v under an
// operator with window w, in the short "x.y" form: the major.minor of the
// highest release in w whose minor line is strictly below v's, so that the
// metadata version stays one step behind and a Kafka rollback remains
// possible; when the window has no older line, v's own major.minor.
//
// Window [4.2.0 4.2.1 4.3.0]: 4.3.0 → "4.2", 4.2.1 → "4.2".
// Window [4.1.0 4.1.1 4.1.2 4.2.0]: 4.2.0 → "4.1", 4.1.2 → "4.1".
func MetadataFor(v Version, w Window) string {
	var best Version
	found := false
	for _, x := range w {
		if x.Major > v.Major || (x.Major == v.Major && x.Minor >= v.Minor) {
			continue
		}
		if !found || best.Less(x) {
			best, found = x, true
		}
	}
	if !found {
		return v.MajorMinor()
	}
	return best.MajorMinor()
}
