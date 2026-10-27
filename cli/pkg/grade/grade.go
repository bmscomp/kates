// Package grade is the one letter-grade scale the kates CLI shares with the
// Kates API: A, B, C, D and F, best first.
//
// The API grades a run's SLA verdict (SlaGrader) and a security audit
// (SecurityService.computeGrade) on this scale and ranks the letters A 5 … F 1
// (SecurityService.gradeRank). The CLI used to carry four copies of the scale,
// one of them with A+ and B- grades the API never sends, and two of them
// ranking an unknown grade as the lowest rather than refusing it. Every
// command that reads or compares a grade goes through Parse and Compare here.
//
// Two commands grade a run themselves, from its throughput and P99, and they
// do it differently: see ForGate and ForBenchmark.
package grade

import (
	"fmt"
	"math"
	"strings"
)

// Grade is a letter grade. Its ordinal is the API's rank: A is 5 and F is 1,
// so a higher Grade is a better one. The zero value is no grade at all.
type Grade int

const (
	// F is the lowest grade.
	F Grade = iota + 1
	D
	C
	B
	// A is the highest grade.
	A
)

// Scale lists every grade, best first, the order --help prints them in.
var Scale = []Grade{A, B, C, D, F}

var names = map[Grade]string{A: "A", B: "B", C: "C", D: "D", F: "F"}

// String is the grade's letter, or "" for the zero Grade.
func (g Grade) String() string { return names[g] }

// Letters names the scale for an error message or a flag's help: "A, B, C, D
// or F".
const Letters = "A, B, C, D or F"

// Parse reads a grade as a command line, a flow file or the API writes it:
// any case, surrounding space ignored. Anything else is an error that names
// the scale, including the API's "-" for a run it could not grade and the
// A+/B- forms an older flow accepted; the API never sends those, so a gate
// that named one could never be met.
func Parse(s string) (Grade, error) {
	key := strings.ToUpper(strings.TrimSpace(s))
	for g, name := range names {
		if name == key {
			return g, nil
		}
	}
	return 0, fmt.Errorf("unknown grade %q (use %s)", s, Letters)
}

// Compare orders two grades: negative when a is worse than b, zero when they
// are the same, positive when a is better.
func Compare(a, b Grade) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// AtLeast reports whether got meets min: the test every gate makes. The zero
// Grade meets nothing and no grade is at least the zero Grade, so a grade that
// failed to Parse never passes a gate.
func AtLeast(got, min Grade) bool {
	return got != 0 && min != 0 && Compare(got, min) >= 0
}

// The two formulas the CLI grades a run with. They are different on purpose
// and are kept apart here, with the difference stated, rather than merged
// into one that would change a documented output:
//
//   - ForGate judges one run against a ladder of throughput-and-latency
//     thresholds. A run must clear both numbers of a rung to earn it, so a
//     fast run with a poor P99 drops whole grades at once. It is what `kates
//     gate` has always printed and what CI pipelines compare --min-grade to.
//   - ForBenchmark scores a run from 0 to 100, half for throughput and half
//     for latency, and the score maps to a grade on the usual 90/80/70/60
//     cut-offs. The score, not only the grade, is part of `kates benchmark`'s
//     scorecard, and the overall grade averages the scores of the three
//     phases, which a ladder of thresholds could not do.
//
// Both take the run's throughput in records per second and its P99 latency
// in milliseconds.

// gateRung is one rung of the gate's ladder.
type gateRung struct {
	Grade      Grade
	Throughput float64 // at least, rec/s
	P99        float64 // below, ms
}

// gateLadder is the gate's ladder, best rung first; F is everything below D.
var gateLadder = []gateRung{
	{A, 50000, 5},
	{B, 30000, 20},
	{C, 15000, 50},
	{D, 5000, 200},
}

// ForGate grades a run as `kates gate` does: the best rung whose throughput
// the run reaches and whose P99 it stays under, F when it reaches none.
func ForGate(throughput, p99 float64) Grade {
	for _, r := range gateLadder {
		if throughput >= r.Throughput && p99 < r.P99 {
			return r.Grade
		}
	}
	return F
}

// GateThresholds describes the gate's ladder for --help, one rung per line.
func GateThresholds() string {
	var b strings.Builder
	for _, r := range gateLadder {
		fmt.Fprintf(&b, "  %s  throughput >= %s rec/s and p99 < %s ms\n", r.Grade, thousands(r.Throughput), trimFloat(r.P99))
	}
	b.WriteString("  F  anything below D")
	return b.String()
}

// benchmarkFullThroughput is the throughput that earns the whole throughput
// half of the benchmark score.
const benchmarkFullThroughput = 50000.0

// benchmarkLatencyPenalty is how many score points each millisecond of P99
// costs, out of the latency half of 50.
const benchmarkLatencyPenalty = 5.0

// BenchmarkScore scores a run from 0 to 100 as `kates benchmark` does: up to
// 50 points for throughput, the whole 50 at 50,000 rec/s, and 50 points for
// latency less 5 per millisecond of P99, so a P99 of 10 ms or more scores 0
// there. A run with no P99 keeps the full latency half, as it always has: the
// scorecard shows the P99 as a dash, so the gap is visible.
func BenchmarkScore(throughput, p99 float64) float64 {
	tScore := math.Min(throughput/benchmarkFullThroughput, 1.0) * 50
	lScore := 50.0
	if p99 > 0 {
		lScore = math.Max(0, 50.0-(p99*benchmarkLatencyPenalty))
	}
	return tScore + lScore
}

// scoreCut is the lowest score that earns a grade; F is everything below D.
var scoreCut = []struct {
	Grade Grade
	Score float64
}{{A, 90}, {B, 80}, {C, 70}, {D, 60}}

// FromScore maps a benchmark score to its grade.
func FromScore(score float64) Grade {
	for _, c := range scoreCut {
		if score >= c.Score {
			return c.Grade
		}
	}
	return F
}

// ForBenchmark grades a run as `kates benchmark` does: FromScore of its
// BenchmarkScore.
func ForBenchmark(throughput, p99 float64) Grade {
	return FromScore(BenchmarkScore(throughput, p99))
}

// BenchmarkThresholds describes the benchmark's score and its cut-offs for
// --help.
func BenchmarkThresholds() string {
	var b strings.Builder
	fmt.Fprintf(&b, "  score = 50 x min(throughput / %s, 1) + max(0, 50 - %s x p99 ms)\n",
		thousands(benchmarkFullThroughput), trimFloat(benchmarkLatencyPenalty))
	for _, c := range scoreCut {
		fmt.Fprintf(&b, "  %s  score >= %s\n", c.Grade, trimFloat(c.Score))
	}
	b.WriteString("  F  anything below D")
	return b.String()
}

// thousands prints a whole number with a comma every three digits.
func thousands(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// trimFloat prints a number without trailing zeros.
func trimFloat(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}
