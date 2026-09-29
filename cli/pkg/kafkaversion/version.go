// Package kafkaversion parses and orders Apache Kafka (and Strimzi) release
// numbers and works with the support windows a Strimzi operator publishes for
// Kafka.
//
// Nothing here carries a table of versions. A Window always comes from an
// operator's own STRIMZI_KAFKA_IMAGES statement — the chart template
// (_kafka_image_map.tpl), a rendered manifest, or the live Deployment — and
// every rule (metadata version, provider, legacy mode) is a pure function of
// a version and a window.
package kafkaversion

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Version is a release number in x.y.z form.
type Version struct {
	Major, Minor, Patch int
}

// ErrInvalidVersion is wrapped by Parse when the input is not x.y.z.
var ErrInvalidVersion = errors.New("invalid version")

// Parse reads a version in x.y.z form. Surrounding whitespace and a leading
// "v" are tolerated; anything else (two components, pre-release or build
// suffixes, signs) is rejected with an error wrapping ErrInvalidVersion.
func Parse(s string) (Version, error) {
	raw := s
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("%w %q: want x.y.z", ErrInvalidVersion, raw)
	}
	var nums [3]int
	for i, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return Version{}, fmt.Errorf("%w %q: want x.y.z", ErrInvalidVersion, raw)
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return Version{}, fmt.Errorf("%w %q: %w", ErrInvalidVersion, raw, err)
		}
		nums[i] = n
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2]}, nil
}

// MustParse is Parse for literals; it panics on an invalid input.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// String renders the version as x.y.z.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// MajorMinor renders the version as x.y — the short form Kafka accepts for a
// metadata version and Strimzi uses in its kafka-versions.yaml.
func (v Version) MajorMinor() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// Compare orders two versions numerically: -1 when a < b, 0 when equal, 1
// when a > b.
func Compare(a, b Version) int {
	switch {
	case a.Major != b.Major:
		return cmpInt(a.Major, b.Major)
	case a.Minor != b.Minor:
		return cmpInt(a.Minor, b.Minor)
	default:
		return cmpInt(a.Patch, b.Patch)
	}
}

// Less reports whether v is older than o.
func (v Version) Less(o Version) bool {
	return Compare(v, o) < 0
}

// AtLeast reports whether v is o or newer.
func (v Version) AtLeast(o Version) bool {
	return Compare(v, o) >= 0
}

// SameMinor reports whether v and o share a major.minor line.
func (v Version) SameMinor(o Version) bool {
	return v.Major == o.Major && v.Minor == o.Minor
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
