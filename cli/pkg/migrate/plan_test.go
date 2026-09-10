package migrate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRunPhasesOrderAndStop(t *testing.T) {
	var ran []string
	step := func(name string, err error, optional bool) Phase {
		return Phase{Name: name, Optional: optional, Run: func(_ context.Context, s *State) error {
			ran = append(ran, name)
			if err != nil {
				s.Report.Fail(name, err.Error())
				return err
			}
			s.Report.Pass(name, "ok")
			return nil
		}}
	}
	boom := errors.New("the producer did not report success")
	phases := []Phase{
		step("preflight", nil, false),
		step("seed", boom, false),
		step("group", nil, false),
	}
	s := NewState(legacyLab(t), map[string]string{"pair": "2.8.2 → 4.3.0"})
	err := RunPhases(context.Background(), phases, s)
	var pe *PhaseError
	if !errors.As(err, &pe) || pe.Phase != "seed" || !errors.Is(err, boom) {
		t.Fatalf("RunPhases = %v", err)
	}
	if err.Error() != `phase "seed": the producer did not report success` {
		t.Errorf("error text = %q", err.Error())
	}
	if !reflect.DeepEqual(ran, []string{"preflight", "seed"}) {
		t.Errorf("ran %v", ran)
	}
	if len(s.Errors) != 1 || s.Errors[0].Phase != "seed" {
		t.Errorf("Errors = %+v", s.Errors)
	}
	if !s.Report.Failed() || len(s.Report.Rows) != 2 {
		t.Errorf("report = %+v", s.Report.Rows)
	}

	// An optional phase's failure is kept and the run goes on.
	ran = nil
	phases = []Phase{
		step("preflight", nil, false),
		step("translation", boom, true),
		step("cutover", nil, false),
	}
	s = NewState(legacyLab(t), nil)
	if err := RunPhases(context.Background(), phases, s); err != nil {
		t.Fatalf("RunPhases with an optional failure = %v", err)
	}
	if !reflect.DeepEqual(ran, []string{"preflight", "translation", "cutover"}) {
		t.Errorf("ran %v", ran)
	}
	if len(s.Errors) != 1 || s.Errors[0].Phase != "translation" || !errors.Is(&s.Errors[0], boom) {
		t.Errorf("Errors = %+v", s.Errors)
	}
}

func TestRunPhasesContextAndValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var ran []string
	phases := []Phase{
		{Name: "first", Run: func(context.Context, *State) error { ran = append(ran, "first"); cancel(); return nil }},
		{Name: "second", Run: func(context.Context, *State) error { ran = append(ran, "second"); return nil }},
	}
	err := RunPhases(ctx, phases, &State{})
	var pe *PhaseError
	if !errors.As(err, &pe) || pe.Phase != "second" || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled run = %v", err)
	}
	if !reflect.DeepEqual(ran, []string{"first"}) {
		t.Errorf("ran %v", ran)
	}

	if err := RunPhases(context.Background(), nil, nil); err == nil {
		t.Error("nil state must fail")
	}
	s := &State{}
	err = RunPhases(context.Background(), []Phase{{Name: "empty"}}, s)
	if err == nil || !strings.Contains(err.Error(), "no Run function") {
		t.Errorf("phase without Run = %v", err)
	}
	if s.Report == nil || s.Values == nil {
		t.Error("RunPhases must open a report and a values bag for a bare state")
	}
	if err := RunPhases(context.Background(), nil, &State{}); err != nil {
		t.Errorf("no phases = %v", err)
	}
}

func TestOffsetSum(t *testing.T) {
	if got := (OffsetSum{}).String(); got != "<no reading>" {
		t.Errorf("unmeasured = %q", got)
	}
	if got := Measured(0).String(); got != "0" {
		t.Errorf("measured zero = %q", got)
	}
	if got := Measured(200).String(); got != "200" {
		t.Errorf("measured = %q", got)
	}
	if Measured(0) == (OffsetSum{}) {
		t.Error("a measured zero must differ from no reading")
	}
}

func TestNewState(t *testing.T) {
	l := legacyLab(t)
	s := NewState(l, map[string]string{"pair": l.Describe()})
	if s.Lab != l || s.Report == nil || s.Values == nil || s.Report.Header["pair"] == "" {
		t.Errorf("NewState = %+v", s)
	}
}
