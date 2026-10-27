package grade

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Grade
	}{
		{"A", A}, {"b", B}, {" C ", C}, {"d", D}, {"F", F}, {"f\n", F},
	} {
		got, err := Parse(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("Parse(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
		if got.String() != strings.ToUpper(strings.TrimSpace(tc.in)) {
			t.Errorf("Parse(%q).String() = %q", tc.in, got.String())
		}
	}
	// The API's "-" for a run it could not grade, the A+/B- forms an older
	// flow accepted, and E, which no scale here has.
	for _, bad := range []string{"", "-", "A+", "B-", "E", "AA", "pass", "N/A"} {
		got, err := Parse(bad)
		if err == nil || got != 0 {
			t.Errorf("Parse(%q) = %v, %v; want the zero Grade and an error", bad, got, err)
		}
		if err != nil && !strings.Contains(err.Error(), Letters) {
			t.Errorf("Parse(%q) error %q does not name the scale", bad, err)
		}
	}
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b Grade
		want int
	}{
		{A, A, 0}, {A, B, 1}, {B, A, -1}, {F, D, -1}, {D, F, 1}, {C, C, 0}, {A, F, 1},
	} {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%v, %v) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	// Scale is best first and strictly ordered.
	for i := 1; i < len(Scale); i++ {
		if Compare(Scale[i-1], Scale[i]) <= 0 {
			t.Errorf("Scale[%d]=%v is not better than Scale[%d]=%v", i-1, Scale[i-1], i, Scale[i])
		}
	}
}

func TestAtLeast(t *testing.T) {
	for _, tc := range []struct {
		got, min Grade
		want     bool
	}{
		{A, B, true}, {B, B, true}, {C, B, false}, {F, F, true}, {D, F, true}, {F, D, false},
		// The zero Grade, what Parse gives for an unknown grade, meets no
		// gate and is no gate: both used to rank as the lowest grade.
		{0, D, false}, {A, 0, false}, {0, 0, false},
	} {
		if got := AtLeast(tc.got, tc.min); got != tc.want {
			t.Errorf("AtLeast(%v, %v) = %v, want %v", tc.got, tc.min, got, tc.want)
		}
	}
}

// The gate's ladder as kates gate printed it before the scale moved here: a
// run must clear both numbers of a rung.
func TestForGate_Golden(t *testing.T) {
	for _, tc := range []struct {
		throughput, p99 float64
		want            Grade
	}{
		{60000, 4, A}, {50000, 4.99, A},
		{60000, 5, B}, {31000, 12.5, B}, {30000, 19.99, B},
		{29999, 1, C}, {15000, 49, C},
		{15000, 50, D}, {5000, 199, D},
		{4999, 1, F}, {100000, 200, F}, {0, 0, F},
	} {
		if got := ForGate(tc.throughput, tc.p99); got != tc.want {
			t.Errorf("ForGate(%v, %v) = %v, want %v", tc.throughput, tc.p99, got, tc.want)
		}
	}
}

// The benchmark's score and grade as kates benchmark printed them before the
// scale moved here: half the score for throughput, half for latency.
func TestForBenchmark_Golden(t *testing.T) {
	for _, tc := range []struct {
		throughput, p99 float64
		score           float64
		want            Grade
	}{
		{50000, 2, 90, A}, {45000, 1, 90, A}, {50000, 0, 100, A},
		{50000, 4, 80, B},
		{40000, 4, 70, C}, {50000, 6, 70, C}, {30000, 2, 70, C},
		{50000, 8, 60, D},
		{25000, 4, 55, F}, {50000, 10, 50, F}, {100000, 10, 50, F}, {10000, 1, 55, F}, {0, 0, 50, F},
	} {
		if score := BenchmarkScore(tc.throughput, tc.p99); score != tc.score {
			t.Errorf("BenchmarkScore(%v, %v) = %v, want %v", tc.throughput, tc.p99, score, tc.score)
		}
		if got := ForBenchmark(tc.throughput, tc.p99); got != tc.want {
			t.Errorf("ForBenchmark(%v, %v) = %v, want %v", tc.throughput, tc.p99, got, tc.want)
		}
	}
	// The overall grade averages scores: (90 + 55) / 2 = 72.5 is a C.
	if got := FromScore((90 + 55) / 2.0); got != C {
		t.Errorf("FromScore(72.5) = %v, want C", got)
	}
}

func TestThresholdsText(t *testing.T) {
	gate := GateThresholds()
	for _, want := range []string{"A  throughput >= 50,000 rec/s and p99 < 5 ms", "D  throughput >= 5,000 rec/s and p99 < 200 ms", "F  anything below D"} {
		if !strings.Contains(gate, want) {
			t.Errorf("GateThresholds lacks %q:\n%s", want, gate)
		}
	}
	bench := BenchmarkThresholds()
	for _, want := range []string{"50 x min(throughput / 50,000, 1)", "50 - 5 x p99 ms", "A  score >= 90", "D  score >= 60"} {
		if !strings.Contains(bench, want) {
			t.Errorf("BenchmarkThresholds lacks %q:\n%s", want, bench)
		}
	}
}
