package migrate

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRowNames(t *testing.T) {
	// The scripts recorded exactly these eighteen assertions, in this order.
	want := []string{
		"cluster reachable", "Strimzi CRDs", "target Kafka Ready", "target credentials",
		"legacy source deployed", "legacy source serving",
		"corpus produced", "source end offsets", "source consumer group",
		"MirrorMaker 2 installed", "CR Ready", "connectors RUNNING",
		"replicated topic exists", "record count", "record content", "offset translation",
		"cutover applied", "cutover froze the target",
	}
	if len(RowNames) != len(want) {
		t.Fatalf("RowNames has %d entries, want %d", len(RowNames), len(want))
	}
	for i := range want {
		if RowNames[i] != want[i] {
			t.Errorf("RowNames[%d] = %q, want %q", i, RowNames[i], want[i])
		}
	}
}

func TestReportRowsAndCounts(t *testing.T) {
	r := NewReport(map[string]string{"source": "Kafka 2.8.2 (legacy, zookeeper)"})
	if r.Started.IsZero() || r.Failed() {
		t.Error("a fresh report is started and not failed")
	}
	r.Pass(RowClusterReachable, "kind-kates")
	r.Pass(RowStrimziCRDs, "kafkamirrormaker2s.kafka.strimzi.io present")
	r.Skip(RowOffsetTranslation, "no consumer group committed")
	if r.Failed() {
		t.Error("skips are not failures")
	}
	r.Fail(RowRecordCount, "only 150 distinct of 200 arrived (150 lines)")
	r.Add(Row{Name: RowRecordContent, Status: StatusFail, Detail: "missing from the target: 151 152 153 154 155..."})
	if !r.Failed() {
		t.Error("a failed row fails the report")
	}
	pass, fail, skip := r.Counts()
	if pass != 2 || fail != 2 || skip != 1 {
		t.Errorf("Counts = %d %d %d", pass, fail, skip)
	}
	if row, ok := r.Row(RowRecordCount); !ok || row.Status != StatusFail {
		t.Errorf("Row = %+v, %v", row, ok)
	}
	if _, ok := r.Row(RowCutoverFroze); ok {
		t.Error("an unrecorded row is not found")
	}
	r.Pass(RowRecordCount, "second attempt")
	if row, _ := r.Row(RowRecordCount); row.Detail != "second attempt" {
		t.Error("Row returns the last record under a name")
	}
}

func TestReportTable(t *testing.T) {
	r := &Report{Header: map[string]string{"target": "Kafka 4.3.0 (krafter in kafka)", "policy": "identity"}, Started: time.Unix(0, 0)}
	r.Pass(RowClusterReachable, "kind-kates")
	r.Fail(RowCutoverFroze, "offsets moved 200 → 250 — the source connector did not stop")
	r.Skip(RowOffsetTranslation, "")
	got := r.Table()
	want := "" +
		"  policy  identity\n" +
		"  target  Kafka 4.3.0 (krafter in kafka)\n" +
		"\n" +
		"  RESULT  ASSERTION                 DETAIL\n" +
		"  ------  ------------------------  ------\n" +
		"  PASS    cluster reachable         kind-kates\n" +
		"  FAIL    cutover froze the target  offsets moved 200 → 250 — the source connector did not stop\n" +
		"  SKIP    offset translation        \n" +
		"\n" +
		"  1 passed, 1 failed, 1 skipped\n"
	if got != want {
		t.Errorf("Table:\n%s\nwant:\n%s", got, want)
	}
	for _, r := range got {
		if r == '\x1b' {
			t.Fatal("Table carries an escape sequence")
		}
	}
	empty := NewReport(nil).Table()
	if !strings.Contains(empty, "RESULT  ASSERTION  DETAIL") || !strings.Contains(empty, "0 passed, 0 failed, 0 skipped") {
		t.Errorf("empty table:\n%s", empty)
	}
	if strings.HasPrefix(empty, "\n") {
		t.Error("no header block without a header")
	}
}

func TestReportJSON(t *testing.T) {
	r := &Report{Header: map[string]string{"pair": "2.8.2 → 4.3.0"}, Started: time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)}
	r.Pass(RowCRReady, "Connect workers are up")
	r.Fail(RowConnectorsRunning, "only 1 of 2 running after 600s")
	data, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Header  map[string]string `json:"header"`
		Started time.Time         `json:"started"`
		Rows    []Row             `json:"rows"`
		Summary struct {
			Pass, Fail, Skip int
			Failed           bool
		} `json:"summary"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("JSON does not parse: %v\n%s", err, data)
	}
	if got.Header["pair"] != "2.8.2 → 4.3.0" || !got.Started.Equal(r.Started) {
		t.Errorf("header/started = %v %v", got.Header, got.Started)
	}
	if len(got.Rows) != 2 || got.Rows[1].Name != RowConnectorsRunning || got.Rows[1].Status != StatusFail {
		t.Errorf("rows = %+v", got.Rows)
	}
	if got.Summary.Pass != 1 || got.Summary.Fail != 1 || got.Summary.Skip != 0 || !got.Summary.Failed {
		t.Errorf("summary = %+v", got.Summary)
	}
	for _, key := range []string{`"name"`, `"status"`, `"detail"`, `"summary"`, `"failed": true`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("JSON lacks %s:\n%s", key, data)
		}
	}

	empty, err := (&Report{}).JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(empty), `"rows": []`) || !strings.Contains(string(empty), `"header": {}`) {
		t.Errorf("an empty report renders empty collections, not null:\n%s", empty)
	}
}
