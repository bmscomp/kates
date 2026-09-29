package migrate

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRecords(t *testing.T) {
	got := Records("m282-430", 3)
	if len(got) != 3 {
		t.Fatalf("Records = %d lines", len(got))
	}
	if !strings.HasPrefix(got[0], `{"id":1,"lab":"m282-430","payload":"`) || !strings.HasSuffix(got[0], `"}`) {
		t.Errorf("record 1 = %s", got[0])
	}
	for i, line := range got {
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("record %d is not JSON: %v", i+1, err)
		}
		if r.ID != i+1 || r.Lab != "m282-430" || len(r.Payload) != 16 {
			t.Errorf("record %d = %+v", i+1, r)
		}
		if strings.ContainsAny(line, "\n\r") {
			t.Errorf("record %d spans lines", i+1)
		}
	}
	if !reflect.DeepEqual(Records("m282-430", 3), got) {
		t.Error("Records is not deterministic")
	}
	if reflect.DeepEqual(Records("m391-430", 3), got) {
		t.Error("another lab's corpus must differ")
	}
	if Records("x", 0) != nil || Records("x", -1) != nil {
		t.Error("a non-positive count yields no records")
	}
	if len(Records("x", 200)) != 200 {
		t.Error("200 records expected")
	}
}

func TestParseConsumedAndParseRecord(t *testing.T) {
	corpus := Records("m282-430", 3)
	output := strings.Join([]string{
		"[2026-09-08 10:20:01,001] WARN [Consumer clientId=console-consumer, groupId=kates-migration-verify-m282-430] Error while fetching metadata (org.apache.kafka.clients.NetworkClient)",
		corpus[0] + "\r",
		"",
		corpus[1],
		"   " + corpus[2] + "   ",
		corpus[1], // a duplicate: at-least-once delivery
		`{"id":7,"lab":"other-lab","payload":"deadbeef00000000"}`,
		`{"not": "ours"}`,
		"migrate-3",
		"Processed a total of 5 messages",
		"[2026-09-08 10:22:01,001] ERROR Unknown error (kafka.tools.ConsoleConsumer$)",
	}, "\n")
	got := ParseConsumed(output)
	want := []string{corpus[0], corpus[1], corpus[2], corpus[1], `{"id":7,"lab":"other-lab","payload":"deadbeef00000000"}`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseConsumed = %q\nwant %q", got, want)
	}
	if Distinct(got) != 4 {
		t.Errorf("Distinct = %d, want 4", Distinct(got))
	}
	if ParseConsumed("") != nil {
		t.Error("empty output yields nil")
	}
	for _, bad := range []string{"", "migrate-1", `{"id":0,"lab":"x","payload":"p"}`, `{"id":1}`, `{"id":"1","lab":"x"}`, "{broken"} {
		if _, ok := ParseRecord(bad); ok {
			t.Errorf("ParseRecord(%q) accepted", bad)
		}
	}
	if r, ok := ParseRecord(" " + corpus[2] + "\r\n"); !ok || r.ID != 3 {
		t.Errorf("ParseRecord(record 3) = %+v, %v", r, ok)
	}
}

func TestDiffAndIDs(t *testing.T) {
	expected := Records("m282-430", 5)
	actual := []string{expected[4], expected[0], expected[0], "  " + expected[2], "", `{"id":9,"lab":"m282-430","payload":"x"}`, `{"id":9,"lab":"m282-430","payload":"x"}`}
	missing, extra := Diff(expected, actual)
	if !reflect.DeepEqual(missing, []string{expected[1], expected[3]}) {
		t.Errorf("missing = %q", missing)
	}
	if !reflect.DeepEqual(extra, []string{`{"id":9,"lab":"m282-430","payload":"x"}`}) {
		t.Errorf("extra = %q", extra)
	}
	if !reflect.DeepEqual(IDs(missing), []int{2, 4}) {
		t.Errorf("IDs(missing) = %v", IDs(missing))
	}
	missing, extra = Diff(expected, expected)
	if missing != nil || extra != nil {
		t.Errorf("identical corpora differ: %q %q", missing, extra)
	}
	missing, extra = Diff(expected, nil)
	if len(missing) != 5 || extra != nil {
		t.Errorf("nothing received: missing %d extra %q", len(missing), extra)
	}
	if Distinct(nil) != 0 || Distinct([]string{"", " ", "a", "a "}) != 1 {
		t.Error("Distinct miscounts")
	}
}
