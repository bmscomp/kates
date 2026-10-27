package cmd

import (
	_ "embed"
	"html/template"
	"strings"

	"github.com/bmscomp/kates/cli/client"
)

//go:embed report_html.tmpl
var reportHTMLSource string

// reportHTMLPage is the report export page. html/template escapes every value
// by the context it lands in, so names and reasons need no escaping by hand.
var reportHTMLPage = template.Must(template.New("report").Parse(reportHTMLSource))

// htmlReportView is what the page prints: every number already formatted, so
// the template only places text.
type htmlReportView struct {
	ID, ShortID string
	Summary     *htmlSummaryView
	SLA         *htmlSLAView
	Phases      []htmlPhaseRow
}

type htmlSummaryView struct {
	TotalRecords, AvgRecPerSec, PeakRecPerSec, AvgMBPerSec string
	Latencies                                              []htmlLatencyRow
	// ErrorColor is one of the page's own colour variables, never input.
	ErrorColor template.CSS
	ErrorRate  string
}

type htmlLatencyRow struct{ Label, Value, Width string }

type htmlSLAView struct {
	Passed     bool
	Violations []htmlViolationRow
}

type htmlViolationRow struct{ Metric, Threshold, Actual string }

type htmlPhaseRow struct {
	Name                                string
	HasSLA, SLAPassed                   bool
	Records, Throughput, P99, ErrorRate string
}

// renderHTMLReport renders a run's report as a self-contained HTML page.
func renderHTMLReport(id string, r *client.Report) (string, error) {
	var b strings.Builder
	if err := reportHTMLPage.Execute(&b, buildHTMLReportView(id, r)); err != nil {
		return "", err
	}
	return b.String(), nil
}

func buildHTMLReportView(id string, r *client.Report) htmlReportView {
	v := htmlReportView{ID: id, ShortID: truncID(id)}
	if s := r.Summary; s != nil {
		v.Summary = htmlSummary(s)
	}
	if verdict := r.OverallSlaVerdict; verdict != nil {
		v.SLA = &htmlSLAView{Passed: verdict.Passed}
		for _, viol := range verdict.Violations {
			threshold, actual := violationCells(viol)
			v.SLA.Violations = append(v.SLA.Violations, htmlViolationRow{Metric: viol.Metric, Threshold: threshold, Actual: actual})
		}
	}
	for _, p := range r.Phases {
		v.Phases = append(v.Phases, htmlPhase(p))
	}
	return v
}

func htmlSummary(s *client.ReportSummary) *htmlSummaryView {
	maxLatency := s.MaxLatencyMs
	if maxLatency == 0 {
		maxLatency = 1
	}
	latencies := []struct {
		label string
		value float64
	}{
		{"Average", s.AvgLatencyMs},
		{"P50", s.P50LatencyMs},
		{"P95", s.P95LatencyMs},
		{"P99", s.P99LatencyMs},
		{"Max", s.MaxLatencyMs},
	}
	view := &htmlSummaryView{
		TotalRecords:  fmtNum(s.TotalRecords),
		AvgRecPerSec:  fmtNum(s.AvgThroughputRecPerSec),
		PeakRecPerSec: fmtNum(s.PeakThroughputRecPerSec),
		AvgMBPerSec:   fmtFloat(s.AvgThroughputMBPerSec, 2),
		ErrorColor:    errorRateColor(s.ErrorRate),
		ErrorRate:     fmtFloat(s.ErrorRate*100, 4),
	}
	for _, l := range latencies {
		pct := min(l.value/maxLatency*100, 100)
		view.Latencies = append(view.Latencies, htmlLatencyRow{Label: l.label, Value: fmtFloat(l.value, 2), Width: fmtFloat(pct, 0)})
	}
	return view
}

// errorRateColor is green with no errors, yellow up to 0.1% and red above.
func errorRateColor(rate float64) template.CSS {
	switch {
	case rate > 0.001:
		return "var(--red)"
	case rate > 0:
		return "var(--yellow)"
	default:
		return "var(--green)"
	}
}

func htmlPhase(p client.ReportPhase) htmlPhaseRow {
	m := client.ReportSummary{}
	if p.Metrics != nil {
		m = *p.Metrics
	}
	row := htmlPhaseRow{
		Name:       p.Name,
		Records:    fmtNum(m.TotalRecords),
		Throughput: fmtFloat(m.AvgThroughputRecPerSec, 1),
		P99:        fmtFloat(m.P99LatencyMs, 2),
		ErrorRate:  fmtFloat(m.ErrorRate*100, 4),
	}
	if p.SlaVerdict != nil {
		row.HasSLA, row.SLAPassed = true, p.SlaVerdict.Passed
	}
	return row
}
