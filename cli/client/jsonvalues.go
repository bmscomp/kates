package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Typed JSON values for the fields the Kates API sends in more than one form:
// ids, ports, offsets and counts that arrive as a number or a numeric string,
// and durations that arrive as seconds, "1234ms", "PT1.5S" or "N/A". Each is
// read once, when the response is decoded, so callers get an int64 or
// milliseconds instead of a type switch over interface{}.
//
// Each keeps the value as the backend sent it, and writes it back exactly as
// json.Marshal wrote the interface{} these fields used to be: a number as a
// float64 renders, a string as a string, null as null. The CLI prints API
// objects with -o json, and that output does not change. IsZero reports an
// absent or null value, so a field tagged omitzero is left out where an
// omitempty interface{} was.

// reemit writes raw the way json.Marshal writes the interface{} that
// json.Unmarshal decodes it into. HTML characters are left unescaped here:
// the encoder that calls MarshalJSON escapes them as it would have escaped
// the interface{} value.
func reemit(raw json.RawMessage) ([]byte, error) {
	if raw == nil {
		return []byte("null"), nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// keepRaw copies b, or returns nil for an empty or null value.
func keepRaw(b []byte) json.RawMessage {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return append(json.RawMessage(nil), b...)
}

// display is raw as fmt's %v showed the interface{} it used to decode into,
// and "" for an absent or null value.
func display(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	return fmt.Sprint(v)
}

// Int is an integer the backend sends as a JSON number or as a numeric
// string: a broker id or port, a partition, an offset, a timestamp in
// milliseconds, a count. A value that is neither, or a number with a
// fraction, is kept for -o json but has no integer (Int64 reports false).
type Int struct {
	raw json.RawMessage
	n   int64
	ok  bool
}

// IntOf is the Int the backend sends as the JSON number n.
func IntOf(n int64) Int {
	return Int{raw: json.RawMessage(strconv.FormatInt(n, 10)), n: n, ok: true}
}

func (v *Int) UnmarshalJSON(b []byte) error {
	*v = Int{raw: keepRaw(b)}
	if v.raw == nil {
		return nil
	}
	text := string(v.raw)
	if v.raw[0] == '"' {
		var s string
		if json.Unmarshal(v.raw, &s) != nil {
			return nil
		}
		text = strings.TrimSpace(s)
	}
	if n, err := strconv.ParseInt(text, 10, 64); err == nil {
		v.n, v.ok = n, true
		return nil
	}
	// A whole number in exponent form (1e3), as some serialisers write one.
	if f, err := strconv.ParseFloat(text, 64); err == nil && f == math.Trunc(f) && math.Abs(f) < 1<<53 {
		v.n, v.ok = int64(f), true
	}
	return nil
}

func (v Int) MarshalJSON() ([]byte, error) { return reemit(v.raw) }

// IsZero reports a value the backend left out or sent as null.
func (v Int) IsZero() bool { return v.raw == nil }

// Int64 is the value, and whether there is one.
func (v Int) Int64() (int64, bool) { return v.n, v.ok }

// Int is the value, or 0 when there is none.
func (v Int) Int() int { return int(v.n) }

// String is the value in decimal; a value that is not an integer shows as
// the backend sent it, and an absent one as "".
func (v Int) String() string {
	if v.ok {
		return strconv.FormatInt(v.n, 10)
	}
	return display(v.raw)
}

// Duration is a java.time.Duration in any of the forms the backend sends one:
// a number of seconds (Jackson's default, fractions carrying the
// nanoseconds), "1234ms" as the timeline endpoints format it, an ISO-8601
// string, or "N/A" when there is none. It is read with ParseBackendDuration's
// rules when the response is decoded; anything it cannot read has no
// milliseconds (Millis reports false) and is kept for -o json.
type Duration struct {
	raw json.RawMessage
	d   BackendDuration
}

// DurationOf is the Duration of a raw JSON value, such as `"1500ms"` or `3.5`.
// It panics on text that is not JSON; it is meant for literals.
func DurationOf(rawJSON string) Duration {
	var d Duration
	if err := json.Unmarshal([]byte(rawJSON), &d); err != nil {
		panic(fmt.Sprintf("client.DurationOf(%q): %v", rawJSON, err))
	}
	return d
}

func (v *Duration) UnmarshalJSON(b []byte) error {
	*v = Duration{raw: keepRaw(b)}
	if v.raw == nil {
		return nil
	}
	return v.d.UnmarshalJSON(v.raw)
}

func (v Duration) MarshalJSON() ([]byte, error) { return reemit(v.raw) }

// IsZero reports a value the backend left out or sent as null.
func (v Duration) IsZero() bool { return v.raw == nil }

// Millis is the duration in milliseconds, and whether it could be read.
func (v Duration) Millis() (int64, bool) { return v.d.Millis, v.d.Set }

// Backend is the duration as the MCP types hold it.
func (v Duration) Backend() BackendDuration { return v.d }

// String is the value as the backend sent it ("1234ms", "N/A", or seconds),
// and "" when it sent none.
func (v Duration) String() string { return display(v.raw) }

// PartitionProblem is one partition the health check reports as offline or
// under-replicated (GET /api/cluster/check, partitionHealth.problems). It is
// written back as the backend sent it, every key kept.
type PartitionProblem struct {
	Topic     string
	Partition Int
	Issue     string
	ISR       Int
	Replicas  Int
	raw       json.RawMessage
}

func (p *PartitionProblem) UnmarshalJSON(b []byte) error {
	var fields struct {
		Topic     any `json:"topic"`
		Partition Int `json:"partition"`
		Issue     any `json:"issue"`
		ISR       Int `json:"isr"`
		Replicas  Int `json:"replicas"`
	}
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	topic, _ := fields.Topic.(string)
	issue, _ := fields.Issue.(string)
	*p = PartitionProblem{Topic: topic, Partition: fields.Partition, Issue: issue, ISR: fields.ISR, Replicas: fields.Replicas}
	p.raw = keepRaw(b)
	return nil
}

func (p PartitionProblem) MarshalJSON() ([]byte, error) {
	if p.raw != nil {
		return reemit(p.raw)
	}
	// Built in Go rather than decoded: the keys the backend sends.
	return json.Marshal(map[string]any{
		"topic": p.Topic, "partition": p.Partition, "issue": p.Issue, "isr": p.ISR, "replicas": p.Replicas,
	})
}
