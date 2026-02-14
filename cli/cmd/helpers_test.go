package cmd

import (
	"testing"
)

func TestMapStr(t *testing.T) {
	m := map[string]interface{}{"key": "value", "nil_key": nil}

	if got := mapStr(m, "key"); got != "value" {
		t.Errorf("mapStr(key) = %q, want %q", got, "value")
	}
	if got := mapStr(m, "missing"); got != "—" {
		t.Errorf("mapStr(missing) = %q, want %q", got, "—")
	}
	if got := mapStr(m, "nil_key"); got != "—" {
		t.Errorf("mapStr(nil_key) = %q, want %q", got, "—")
	}
}

func TestMapStrEmpty(t *testing.T) {
	m := map[string]interface{}{"key": "value", "nil_key": nil}

	if got := mapStrEmpty(m, "key"); got != "value" {
		t.Errorf("mapStrEmpty(key) = %q, want %q", got, "value")
	}
	if got := mapStrEmpty(m, "missing"); got != "" {
		t.Errorf("mapStrEmpty(missing) = %q, want %q", got, "")
	}
	if got := mapStrEmpty(m, "nil_key"); got != "" {
		t.Errorf("mapStrEmpty(nil_key) = %q, want %q", got, "")
	}
}

func TestNumVal(t *testing.T) {
	m := map[string]interface{}{"count": 42.0, "text": "hello"}

	if got := numVal(m, "count"); got != 42.0 {
		t.Errorf("numVal(count) = %f, want 42.0", got)
	}
	if got := numVal(m, "missing"); got != 0 {
		t.Errorf("numVal(missing) = %f, want 0", got)
	}
	if got := numVal(m, "text"); got != 0 {
		t.Errorf("numVal(text) = %f, want 0", got)
	}
}

func TestFmtNum(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{500, "500"},
		{1500, "1.5K"},
		{1_500_000, "1.5M"},
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{1_000_000, "1.0M"},
	}
	for _, tt := range tests {
		if got := fmtNum(tt.input); got != tt.want {
			t.Errorf("fmtNum(%g) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFmtFloat(t *testing.T) {
	if got := fmtFloat(3.14159, 2); got != "3.14" {
		t.Errorf("fmtFloat(3.14159, 2) = %q, want %q", got, "3.14")
	}
	if got := fmtFloat(100.0, 0); got != "100" {
		t.Errorf("fmtFloat(100, 0) = %q, want %q", got, "100")
	}
}

func TestTruncID(t *testing.T) {
	if got := truncID("abcdef123456789"); got != "abcdef123456…" {
		t.Errorf("truncID(long) = %q, want %q", got, "abcdef123456…")
	}
	if got := truncID("short"); got != "short" {
		t.Errorf("truncID(short) = %q, want %q", got, "short")
	}
	if got := truncID("exactly12chr"); got != "exactly12chr" {
		t.Errorf("truncID(12) = %q, want %q", got, "exactly12chr")
	}
}

func TestFormatTime(t *testing.T) {
	if got := formatTime("2025-01-15T10:30:45.123Z"); got != "2025-01-15 10:30:45" {
		t.Errorf("formatTime = %q, want %q", got, "2025-01-15 10:30:45")
	}
	if got := formatTime("short-ts"); got != "short-ts" {
		t.Errorf("formatTime(short) = %q, want %q", got, "short-ts")
	}
}

func TestFormatMetricVal(t *testing.T) {
	if got := formatMetricVal(0.0012, "errorRate", "%"); got != "0.1200%" {
		t.Errorf("formatMetricVal(errorRate) = %q, want %q", got, "0.1200%")
	}
	if got := formatMetricVal(5000, "throughput", "rec/s"); got != "5.0K rec/s" {
		t.Errorf("formatMetricVal(5K) = %q, want %q", got, "5.0K rec/s")
	}
	if got := formatMetricVal(2_500_000, "throughput", "rec/s"); got != "2.5M rec/s" {
		t.Errorf("formatMetricVal(2.5M) = %q, want %q", got, "2.5M rec/s")
	}
	if got := formatMetricVal(42.5, "latency", "ms"); got != "42.50 ms" {
		t.Errorf("formatMetricVal(42.5) = %q, want %q", got, "42.50 ms")
	}
}

func TestPadLeftN(t *testing.T) {
	if got := padLeftN("abc", 6); got != "   abc" {
		t.Errorf("padLeftN = %q, want %q", got, "   abc")
	}
	if got := padLeftN("abcdef", 3); got != "abcdef" {
		t.Errorf("padLeftN(longer) = %q, want %q", got, "abcdef")
	}
}

func TestDescribeType(t *testing.T) {
	if got := describeType("LOAD"); got == "" {
		t.Error("describeType(LOAD) should not be empty")
	}
	if got := describeType("UNKNOWN"); got != "" {
		t.Errorf("describeType(UNKNOWN) = %q, want empty", got)
	}
}

func TestCountStatuses(t *testing.T) {
	tests := []map[string]interface{}{
		{"status": "RUNNING"},
		{"status": "RUNNING"},
		{"status": "PENDING"},
		{"status": "DONE"},
		{"status": "COMPLETED"},
		{"status": "FAILED"},
		{"status": "ERROR"},
	}
	c := CountStatuses(tests)
	if c.Running != 2 {
		t.Errorf("Running = %d, want 2", c.Running)
	}
	if c.Pending != 1 {
		t.Errorf("Pending = %d, want 1", c.Pending)
	}
	if c.Done != 2 {
		t.Errorf("Done = %d, want 2", c.Done)
	}
	if c.Failed != 2 {
		t.Errorf("Failed = %d, want 2", c.Failed)
	}
}

func TestCountStatuses_Empty(t *testing.T) {
	c := CountStatuses(nil)
	if c.Running != 0 || c.Pending != 0 || c.Done != 0 || c.Failed != 0 {
		t.Error("CountStatuses(nil) should return all zeros")
	}
}

func TestParsePaged(t *testing.T) {
	data := []byte(`{"content":[{"id":"abc"}],"page":0,"size":20,"totalItems":1,"totalPages":1}`)
	p, err := ParsePaged(data)
	if err != nil {
		t.Fatalf("ParsePaged error: %v", err)
	}
	if len(p.Content) != 1 {
		t.Errorf("Content len = %d, want 1", len(p.Content))
	}
	if p.TotalItems != 1 {
		t.Errorf("TotalItems = %d, want 1", p.TotalItems)
	}
}

func TestSpinnerFrame(t *testing.T) {
	f0 := spinnerFrame(0)
	f1 := spinnerFrame(1)
	if f0 == "" {
		t.Error("spinnerFrame(0) should not be empty")
	}
	if f0 == f1 {
		t.Error("spinnerFrame(0) and spinnerFrame(1) should differ")
	}
	// Should wrap around
	f10 := spinnerFrame(10)
	if f10 != f0 {
		t.Errorf("spinnerFrame(10) should equal spinnerFrame(0) (wrap)")
	}
}
