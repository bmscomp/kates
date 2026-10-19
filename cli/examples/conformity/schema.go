// Package conformity holds the KafkaReference JSON Schema and examples, so
// the CLI checks a reference against the same file editors validate with.
package conformity

import _ "embed"

// Schema is kafkareference.v1alpha1.schema.json.
//
//go:embed kafkareference.v1alpha1.schema.json
var Schema []byte
