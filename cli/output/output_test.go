package output

import (
	"strings"
	"testing"
)

func TestSparkline_Empty(t *testing.T) {
	if got := Sparkline(nil); got != "" {
		t.Errorf("Sparkline(nil) = %q, want empty", got)
	}
	if got := Sparkline([]float64{}); got != "" {
		t.Errorf("Sparkline([]) = %q, want empty", got)
	}
}

func TestSparkline_SingleValue(t *testing.T) {
	got := Sparkline([]float64{5.0})
	if got == "" {
		t.Error("Sparkline with single value should not be empty")
	}
}

func TestSparkline_Ascending(t *testing.T) {
	got := Sparkline([]float64{1, 2, 3, 4, 5, 6, 7, 8})
	if got == "" {
		t.Error("Sparkline ascending should not be empty")
	}
}

func TestSparkline_AllSame(t *testing.T) {
	got := Sparkline([]float64{5, 5, 5, 5})
	if got == "" {
		t.Error("Sparkline all-same should not be empty")
	}
}

func TestSparklineColored_HigherIsBetter(t *testing.T) {
	values := []float64{10, 50, 90, 20, 80}
	got := SparklineColored(values, true)
	if got == "" {
		t.Error("SparklineColored should not be empty")
	}
}

func TestSparklineColored_LowerIsBetter(t *testing.T) {
	values := []float64{10, 50, 90, 20, 80}
	got := SparklineColored(values, false)
	if got == "" {
		t.Error("SparklineColored should not be empty")
	}
}

func TestSparklineColored_Empty(t *testing.T) {
	if got := SparklineColored(nil, true); got != "" {
		t.Errorf("SparklineColored(nil) = %q, want empty", got)
	}
}

func TestStatusBadge(t *testing.T) {
	up := StatusBadge("UP")
	if up == "" {
		t.Error("StatusBadge(UP) should not be empty")
	}

	down := StatusBadge("DOWN")
	if down == "" {
		t.Error("StatusBadge(DOWN) should not be empty")
	}
}

func TestPanel(t *testing.T) {
	got := Panel("Title", "Content here", 40)
	if got == "" {
		t.Error("Panel should not be empty")
	}
	if !strings.Contains(got, "Title") {
		t.Error("Panel should contain the title")
	}
}
