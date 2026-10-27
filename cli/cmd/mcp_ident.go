package cmd

import (
	"fmt"
	"regexp"

	"github.com/google/jsonschema-go/jsonschema"
)

// The identifiers and enums more than one kates mcp tool checks, and the
// input-schema helpers every tool with constraints a struct tag cannot carry
// uses. One copy of each, so that the tools cannot drift apart.

// mcpKafkaTopicPattern is Kafka's rule for topic names: 1 to 249 of these
// characters (org.apache.kafka.common.internals.Topic, kafka-clients 3.9.2).
// "." and ".." match it and are refused separately where a topic names a
// request path (mcpValidateKafkaTopic). It is also the rule TestSpec's bean
// validation applies to a spec's topic (domain/TestSpec.java:36).
const mcpKafkaTopicPattern = `^[a-zA-Z0-9._-]{1,249}$`

// The values TestSpec's bean validation accepts for acks and compressionType
// (domain/TestSpec.java:50,61-63).
var (
	mcpKafkaTopicRE  = regexp.MustCompile(mcpKafkaTopicPattern)
	mcpAcksRE        = regexp.MustCompile(`^(all|-1|0|1)$`)
	mcpCompressionRE = regexp.MustCompile(`^(none|gzip|snappy|lz4|zstd)$`)
)

// mcpValidateKafkaTopic refuses anything Kafka would not accept as a topic name,
// so a topic argument can carry neither prose nor a path.
func mcpValidateKafkaTopic(field, topic string) error {
	if !mcpKafkaTopicRE.MatchString(topic) || topic == "." || topic == ".." {
		return mcpInvalidArgument(field+" must be a Kafka topic name: 1 to 249 letters, digits, '.', '_' or '-', "+
			"and not \".\" or \"..\".", topic)
	}
	return nil
}

// The test types and statuses the backend knows (domain/TestType.java,
// domain/TestResult.java:25-31). A test checks them against the Java enums.
var (
	mcpTestTypes = []string{
		"LOAD", "STRESS", "SPIKE", "ENDURANCE", "VOLUME", "CAPACITY", "ROUND_TRIP", "INTEGRITY",
		"TUNE_REPLICATION", "TUNE_ACKS", "TUNE_BATCHING", "TUNE_COMPRESSION", "TUNE_PARTITIONS", "INTEGRATION_CDC",
	}
	mcpRunStatuses = []string{"PENDING", "RUNNING", "STOPPING", "DONE", "FAILED"}
)

// mcpInputSchema is the input schema addReadTool would infer for In, with
// edit applied: the constraints a struct tag cannot carry (a pattern, an
// enum, bounds), so that the SDK refuses a bad argument before any Kates code
// runs. The handler checks the same things again. name is the tool's, for the
// panic a broken schema raises at registration.
func mcpInputSchema[In any](name string, edit func(s *jsonschema.Schema)) *jsonschema.Schema {
	s, err := mcpSchemaFor[In]()
	if err != nil {
		panic(fmt.Sprintf("kates mcp: tool %q input schema: %v", name, err))
	}
	edit(s)
	return s
}

// mcpBounds sets a numeric property's inclusive minimum and maximum. A
// property missing from its schema is a bug in the tool's In type.
func mcpBounds(s *jsonschema.Schema, lo, hi float64) {
	mcpAtLeast(s, lo)
	s.Maximum = &hi
}

// mcpAtLeast sets a numeric property's inclusive minimum only.
func mcpAtLeast(s *jsonschema.Schema, lo float64) {
	if s == nil {
		panic("kates mcp: a bounded input property is missing from its schema")
	}
	s.Minimum = &lo
}

// mcpAnys is vals as a schema enum.
func mcpAnys[T any](vals []T) []any {
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = v
	}
	return out
}
