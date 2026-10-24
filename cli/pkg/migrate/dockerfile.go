package migrate

import _ "embed"

// LegacyDockerfileName is the repository's Dockerfile for the legacy Kafka
// images, at the repository root.
const LegacyDockerfileName = "Dockerfile.legacy-kafka"

// LegacyDockerfile is LegacyDockerfileName built into the binary, so kates
// migrate builds a legacy image outside a checkout too: the Dockerfile
// copies nothing from the repository. dockerfile_test.go holds it equal.
//
//go:embed legacy-kafka.Dockerfile
var LegacyDockerfile []byte
