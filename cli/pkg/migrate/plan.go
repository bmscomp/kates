package migrate

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// Phase is one step of a verify or run. Run does the work and records its
// own rows in the State's Report; an error stops the sequence unless the
// phase is Optional, in which case the error is kept in State.Errors and
// the next phase runs.
type Phase struct {
	Name     string
	Run      func(ctx context.Context, s *State) error
	Optional bool
}

// PhaseError is the error RunPhases returns: the phase that failed and why.
type PhaseError struct {
	Phase string
	Err   error
}

// Error implements error.
func (e *PhaseError) Error() string {
	return fmt.Sprintf("phase %q: %v", e.Phase, e.Err)
}

// Unwrap returns the phase's own error.
func (e *PhaseError) Unwrap() error {
	return e.Err
}

// OffsetSum is an end-offset reading: the sum over partitions, and whether
// it was measured at all. An unmeasured reading is not zero — the scripts'
// "the tool failed" and "the topic is empty" must stay distinguishable.
type OffsetSum struct {
	Sum      int64
	Measured bool
}

// String renders the reading, or "<no reading>".
func (o OffsetSum) String() string {
	if !o.Measured {
		return "<no reading>"
	}
	return strconv.FormatInt(o.Sum, 10)
}

// Measured makes a measured OffsetSum.
func Measured(sum int64) OffsetSum {
	return OffsetSum{Sum: sum, Measured: true}
}

// State is the bag the phases share: the lab, the report they write, and
// the few measurements one phase takes for a later one to judge.
type State struct {
	Lab    *Lab
	Report *Report
	// Values holds whatever the cmd layer needs to carry between phases
	// that is not a measurement: paths of generated values files, the
	// client pods, timeouts.
	Values map[string]any

	// Produced is the corpus written to the source; Consumed what came back
	// off the target.
	Produced, Consumed []string
	// SourceEndOffsets is the source's end-offset sum after seeding;
	// TargetOffsetsBefore and TargetOffsetsAfter bracket the cutover
	// rehearsal.
	SourceEndOffsets, TargetOffsetsBefore, TargetOffsetsAfter OffsetSum
	// GroupPartitions is how many partitions the source consumer group
	// committed on.
	GroupPartitions int
	// Errors collects every phase error, optional or not, in order.
	Errors []PhaseError
}

// NewState opens a State for a lab with a fresh report.
func NewState(l *Lab, header map[string]string) *State {
	return &State{Lab: l, Report: NewReport(header), Values: map[string]any{}}
}

// RunPhases runs the phases in order. It stops at the first error of a
// non-optional phase and returns it as a *PhaseError; an optional phase's
// error is recorded in s.Errors and the run continues. A cancelled context
// stops before the next phase starts. The report is the phases' business:
// RunPhases records nothing itself.
func RunPhases(ctx context.Context, phases []Phase, s *State) error {
	if s == nil {
		return errors.New("state is required")
	}
	if s.Report == nil {
		s.Report = NewReport(nil)
	}
	if s.Values == nil {
		s.Values = map[string]any{}
	}
	for _, p := range phases {
		if err := ctx.Err(); err != nil {
			return &PhaseError{Phase: p.Name, Err: err}
		}
		if p.Run == nil {
			return &PhaseError{Phase: p.Name, Err: errors.New("phase has no Run function")}
		}
		if err := p.Run(ctx, s); err != nil {
			pe := PhaseError{Phase: p.Name, Err: err}
			s.Errors = append(s.Errors, pe)
			if !p.Optional {
				return &pe
			}
		}
	}
	return nil
}
