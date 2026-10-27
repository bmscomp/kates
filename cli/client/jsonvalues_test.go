package client

import (
	"bytes"
	"encoding/json"
	"maps"
	"testing"
)

// The types below are the API objects as they were before their ids, ports,
// counts and durations were typed: every such field an interface{}. -o json
// prints these objects, so each typed object must marshal to the same bytes
// as its old form decoded from the same body.

type oldBrokerNode struct {
	ID   interface{} `json:"id"`
	Host string      `json:"host"`
	Port interface{} `json:"port"`
	Rack string      `json:"rack"`
}

type oldClusterInfo struct {
	ClusterID   string          `json:"clusterId"`
	BrokerCount interface{}     `json:"brokerCount"`
	Controller  *oldBrokerNode  `json:"controller,omitempty"`
	Brokers     []oldBrokerNode `json:"brokers,omitempty"`
}

type oldStepReport struct {
	StepName            string      `json:"stepName"`
	TimeToFirstReady    interface{} `json:"timeToFirstReady,omitempty"`
	TimeToAllReady      interface{} `json:"timeToAllReady,omitempty"`
	StrimziRecoveryTime interface{} `json:"strimziRecoveryTime,omitempty"`
	IsrMetrics          *struct {
		TimeToFullIsr interface{} `json:"timeToFullIsr,omitempty"`
		MinIsrDepth   int         `json:"minIsrDepth"`
	} `json:"isrMetrics,omitempty"`
	LagMetrics *struct {
		BaselineLag       int64       `json:"baselineLag"`
		TimeToLagRecovery interface{} `json:"timeToLagRecovery,omitempty"`
	} `json:"lagMetrics,omitempty"`
}

type newStepReport struct {
	StepName            string   `json:"stepName"`
	TimeToFirstReady    Duration `json:"timeToFirstReady,omitzero"`
	TimeToAllReady      Duration `json:"timeToAllReady,omitzero"`
	StrimziRecoveryTime Duration `json:"strimziRecoveryTime,omitzero"`
	IsrMetrics          *struct {
		TimeToFullIsr Duration `json:"timeToFullIsr,omitzero"`
		MinIsrDepth   int      `json:"minIsrDepth"`
	} `json:"isrMetrics,omitempty"`
	LagMetrics *struct {
		BaselineLag       int64    `json:"baselineLag"`
		TimeToLagRecovery Duration `json:"timeToLagRecovery,omitzero"`
	} `json:"lagMetrics,omitempty"`
}

type oldSummary struct {
	WorstRecovery    interface{} `json:"worstRecovery,omitempty"`
	WorstIsrRecovery interface{} `json:"worstIsrRecovery,omitempty"`
}

type oldTimeline struct {
	Step             string      `json:"step"`
	Type             string      `json:"type"`
	Events           []PodEvent  `json:"events"`
	TimeToFirstReady interface{} `json:"timeToFirstReady"`
	TimeToAllReady   interface{} `json:"timeToAllReady"`
}

type oldKafkaTopic struct {
	Name              string      `json:"name"`
	Internal          bool        `json:"internal"`
	Partitions        int         `json:"partitions"`
	ReplicationFactor int         `json:"replicationFactor"`
	UnderReplicated   interface{} `json:"underReplicated"`
}

type oldKafkaRecord struct {
	Offset    interface{} `json:"offset"`
	Partition interface{} `json:"partition"`
	Timestamp interface{} `json:"timestamp"`
	Key       interface{} `json:"key"`
	Value     interface{} `json:"value"`
}

type oldProduceMeta struct {
	Topic     string      `json:"topic"`
	Partition interface{} `json:"partition"`
	Offset    interface{} `json:"offset"`
	Timestamp interface{} `json:"timestamp"`
}

type oldPartitionHealth struct {
	UnderReplicated int                      `json:"underReplicated"`
	Offline         int                      `json:"offline"`
	Problems        []map[string]interface{} `json:"problems,omitempty"`
}

// sameJSON decodes body into old and typed, and fails unless both marshal
// to the same bytes, both compact and as output.JSON indents them.
func sameJSON[O, N any](t *testing.T, name, body string) {
	t.Helper()
	var o O
	var n N
	if err := json.Unmarshal([]byte(body), &o); err != nil {
		t.Fatalf("%s: old decode: %v", name, err)
	}
	if err := json.Unmarshal([]byte(body), &n); err != nil {
		t.Fatalf("%s: typed decode: %v", name, err)
	}
	for _, indent := range []bool{false, true} {
		marshal := json.Marshal
		if indent {
			marshal = func(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
		}
		want, err := marshal(o)
		if err != nil {
			t.Fatal(err)
		}
		got, err := marshal(n)
		if err != nil {
			t.Fatalf("%s: typed marshal: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: -o json changed:\n got %s\nwant %s", name, got, want)
		}
	}
}

// sameKeys checks that the client type itself writes keys as old did: an
// object of the full type has fields old does not list.
func sameKeys[O, N any](t *testing.T, body string, keys ...string) {
	t.Helper()
	project := func(v any) map[string]string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, k := range keys {
			if raw, ok := m[k]; ok {
				out[k] = string(raw)
			}
		}
		return out
	}
	var o O
	var n N
	if json.Unmarshal([]byte(body), &o) != nil || json.Unmarshal([]byte(body), &n) != nil {
		t.Fatalf("cannot decode %s", body)
	}
	if got, want := project(n), project(o); !maps.Equal(got, want) {
		t.Errorf("%s: got %v, want %v", body, got, want)
	}
}

func TestTypedJSONReemitsTheOldBytes(t *testing.T) {
	for _, body := range []string{
		`{"clusterId":"c","brokerCount":3,"controller":{"id":1,"host":"k-1","port":9092,"rack":"a"},"brokers":[{"id":0,"host":"k-0","port":9092,"rack":""},{"id":"2","host":"k-2","port":"9093","rack":"b"}]}`,
		`{"clusterId":"c","brokerCount":null,"brokers":[{"id":1e3,"host":"h","port":9092.0}]}`,
		`{"clusterId":"c"}`,
	} {
		sameJSON[oldClusterInfo, ClusterInfo](t, "ClusterInfo", body)
	}
	for _, body := range []string{
		`{"stepName":"s","timeToFirstReady":4.200000000,"timeToAllReady":"1234ms","strimziRecoveryTime":"PT1.5S"}`,
		`{"stepName":"s","timeToFirstReady":null,"timeToAllReady":"N/A","isrMetrics":{"timeToFullIsr":12,"minIsrDepth":1},"lagMetrics":{"baselineLag":5}}`,
		`{"stepName":"s","timeToAllReady":"","isrMetrics":{"minIsrDepth":2},"lagMetrics":{"baselineLag":0,"timeToLagRecovery":0}}`,
		`{"stepName":"<s&>","timeToAllReady":"<1ms>"}`,
	} {
		sameJSON[oldStepReport, newStepReport](t, "StepReport", body)
		sameKeys[oldStepReport, StepReport](t, body, "timeToFirstReady", "timeToAllReady", "strimziRecoveryTime")
	}
	for _, body := range []string{`{"worstRecovery":12.75,"worstIsrRecovery":"3000ms"}`, `{}`, `{"worstRecovery":null}`} {
		sameJSON[oldSummary, struct {
			WorstRecovery    Duration `json:"worstRecovery,omitzero"`
			WorstIsrRecovery Duration `json:"worstIsrRecovery,omitzero"`
		}](t, "DisruptionSummary", body)
	}
	for _, body := range []string{`{"step":"a","timeToFirstReady":"1234ms","timeToAllReady":"N/A"}`, `{"step":"a"}`} {
		sameJSON[oldTimeline, DisruptionTimeline](t, "DisruptionTimeline", body)
	}
	sameJSON[[]oldKafkaTopic, []KafkaTopic](t, "KafkaTopic", `[{"name":"a","underReplicated":0},{"name":"b","underReplicated":3},{"name":"c"}]`)
	sameJSON[[]oldKafkaRecord, []KafkaRecord](t, "KafkaRecord",
		`[{"offset":12345678901,"partition":0,"timestamp":1727600000000,"key":null,"value":{"a":1}},{"offset":"7","partition":"1","timestamp":null,"key":"k","value":"v"}]`)
	sameJSON[oldProduceMeta, ProduceMeta](t, "ProduceMeta", `{"topic":"t","partition":2,"offset":41,"timestamp":1727600000000}`)
	sameJSON[oldPartitionHealth, PartitionHealthReport](t, "PartitionHealthReport",
		`{"underReplicated":1,"offline":1,"problems":[{"topic":"t","partition":0,"issue":"OFFLINE","isr":0,"replicas":3,"extra":"<kept>"},{"partition":"2"}]}`)
}

func TestIntReadsNumbersAndNumericStrings(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int64
		ok   bool
		text string
	}{
		{`9092`, 9092, true, "9092"},
		{`"9092"`, 9092, true, "9092"},
		{`" 7 "`, 7, true, "7"},
		{`1e3`, 1000, true, "1000"},
		{`12345678901234`, 12345678901234, true, "12345678901234"},
		{`1.5`, 0, false, "1.5"},
		{`"abc"`, 0, false, "abc"},
		{`null`, 0, false, ""},
		{`true`, 0, false, "true"},
	} {
		var v Int
		if err := json.Unmarshal([]byte(tc.in), &v); err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if n, ok := v.Int64(); n != tc.n || ok != tc.ok || v.String() != tc.text {
			t.Errorf("Int(%s) = %d, %v, %q; want %d, %v, %q", tc.in, n, ok, v.String(), tc.n, tc.ok, tc.text)
		}
	}
	if b, _ := json.Marshal(IntOf(42)); string(b) != "42" {
		t.Errorf("IntOf(42) marshals as %s", b)
	}
}

func TestDurationReadsEveryBackendForm(t *testing.T) {
	for _, tc := range []struct {
		in     string
		millis int64
		ok     bool
	}{
		{`"1500ms"`, 1500, true},
		{`"PT2S"`, 2000, true},
		{`"PT1.5S"`, 1500, true},
		{`3.0`, 3000, true},
		{`4.200000000`, 4200, true},
		{`"N/A"`, 0, false},
		{`null`, 0, false},
		{`""`, 0, false},
		{`{}`, 0, false},
	} {
		d := DurationOf(tc.in)
		if millis, ok := d.Millis(); millis != tc.millis || ok != tc.ok {
			t.Errorf("Duration(%s) = %d, %v; want %d, %v", tc.in, millis, ok, tc.millis, tc.ok)
		}
		if d.Backend() != (BackendDuration{Millis: tc.millis, Set: tc.ok}) {
			t.Errorf("Duration(%s).Backend() = %+v", tc.in, d.Backend())
		}
	}
	if !DurationOf(`null`).IsZero() || DurationOf(`"N/A"`).IsZero() {
		t.Error("IsZero must hold for null only, not for a value that cannot be read")
	}
}
