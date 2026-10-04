package kafkaversion

import (
	"errors"
	"fmt"
)

// MinMirrorSource is the oldest Kafka a 4.x client — and so a 4.x
// MirrorMaker 2 — can read (KIP-896 removed the older protocol versions).
// Versions below it are refused as a source or an additional cluster.
var MinMirrorSource = Version{Major: 2, Minor: 1, Patch: 0}

// ErrBelowMirrorFloor is wrapped by ProviderFor for versions older than
// MinMirrorSource.
var ErrBelowMirrorFloor = errors.New("below the KIP-896 floor")

// Provider is who runs a Kafka cluster.
type Provider string

const (
	// ProviderStrimzi is a Kafka reconciled by a Strimzi Cluster Operator.
	ProviderStrimzi Provider = "strimzi"
	// ProviderLegacy is a Kafka run by the legacy-kafka chart (plain
	// StatefulSets, no operator).
	ProviderLegacy Provider = "legacy"
)

// LegacyMode is how the legacy-kafka chart runs a given Kafka version.
type LegacyMode string

const (
	// LegacyZooKeeper runs the image built by Dockerfile.legacy-kafka with a
	// ZooKeeper ensemble (Kafka < 3.3.0).
	LegacyZooKeeper LegacyMode = "zookeeper"
	// LegacyKRaftBuilt runs the built image in KRaft mode (3.3.0 ≤ Kafka <
	// 3.7.0, before an official apache/kafka image existed).
	LegacyKRaftBuilt LegacyMode = "kraft-built"
	// LegacyKRaftOfficial runs the official apache/kafka image in KRaft mode
	// (Kafka ≥ 3.7.0).
	LegacyKRaftOfficial LegacyMode = "kraft-official"
)

// DefaultLegacyRegistry is where the built legacy images live when no
// registry is given.
const DefaultLegacyRegistry = "ghcr.io/bmscomp"

var (
	firstKRaft         = Version{Major: 3, Minor: 3, Patch: 0}
	firstOfficialImage = Version{Major: 3, Minor: 7, Patch: 0}
)

// LegacyModeFor picks the legacy-kafka mode for a version: ZooKeeper below
// 3.3.0, the built KRaft image below 3.7.0, the official image from there.
func LegacyModeFor(v Version) LegacyMode {
	switch {
	case v.Less(firstKRaft):
		return LegacyZooKeeper
	case v.Less(firstOfficialImage):
		return LegacyKRaftBuilt
	default:
		return LegacyKRaftOfficial
	}
}

// LegacyImageFor returns the image the legacy-kafka chart runs for v:
// apache/kafka:<v> from 3.7.0, otherwise <registry>/kates-legacy-kafka:<v>
// with registry defaulting to DefaultLegacyRegistry.
func LegacyImageFor(v Version, registry string) string {
	if v.AtLeast(firstOfficialImage) {
		return "apache/kafka:" + v.String()
	}
	if registry == "" {
		registry = DefaultLegacyRegistry
	}
	return registry + "/kates-legacy-kafka:" + v.String()
}

// ProviderFor decides who runs Kafka v given the primary operator's window:
// Strimzi when the window has it, the legacy chart (with its mode) when not.
// Versions older than MinMirrorSource are refused with an error wrapping
// ErrBelowMirrorFloor.
func ProviderFor(v Version, primary Window) (Provider, LegacyMode, error) {
	if v.Less(MinMirrorSource) {
		return "", "", fmt.Errorf("version %s is %w: %s is the oldest broker a Kafka 4.x client (and MirrorMaker 2) can read, KIP-896 removed the older protocol versions",
			v, ErrBelowMirrorFloor, MinMirrorSource)
	}
	if primary.Supports(v) {
		return ProviderStrimzi, "", nil
	}
	return ProviderLegacy, LegacyModeFor(v), nil
}
