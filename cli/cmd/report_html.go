package cmd

import (
	"fmt"
	"strings"

	"github.com/klster/kates-cli/client"
)

func renderHTMLReport(id string, r *client.Report) string {
	var b strings.Builder

	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Kates Report — ` + truncID(id) + `</title>
<style>
  :root {
    --bg: #0f1117; --surface: #161b22; --border: #30363d;
    --text: #e6edf3; --dim: #8b949e; --accent: #58a6ff;
    --green: #3fb950; --red: #f85149; --yellow: #d29922;
    --gradient-start: #7c3aed; --gradient-end: #06b6d4;
  }
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif;
    background: var(--bg); color: var(--text); padding: 2rem; line-height: 1.6;
  }
  .container { max-width: 900px; margin: 0 auto; }
  .header {
    background: linear-gradient(135deg, var(--gradient-start), var(--gradient-end));
    border-radius: 12px; padding: 2rem; margin-bottom: 2rem; text-align: center;
  }
  .header h1 { font-size: 1.8rem; font-weight: 700; margin-bottom: 0.25rem; }
  .header .subtitle { opacity: 0.85; font-size: 0.95rem; }
  .card {
    background: var(--surface); border: 1px solid var(--border);
    border-radius: 10px; padding: 1.5rem; margin-bottom: 1.25rem;
  }
  .card h2 {
    font-size: 1.1rem; color: var(--accent); margin-bottom: 1rem;
    padding-bottom: 0.5rem; border-bottom: 1px solid var(--border);
  }
  table { width: 100%; border-collapse: collapse; font-size: 0.9rem; }
  th {
    text-align: left; padding: 0.6rem 0.8rem; color: var(--dim);
    font-weight: 600; border-bottom: 2px solid var(--border); font-size: 0.8rem;
    text-transform: uppercase; letter-spacing: 0.05em;
  }
  td { padding: 0.6rem 0.8rem; border-bottom: 1px solid var(--border); }
  tr:last-child td { border-bottom: none; }
  tr:hover { background: rgba(88,166,255,0.04); }
  .metric-value { font-weight: 600; font-variant-numeric: tabular-nums; }
  .bar-container {
    width: 120px; height: 8px; background: var(--border);
    border-radius: 4px; overflow: hidden; display: inline-block; vertical-align: middle;
    margin-left: 0.5rem;
  }
  .bar-fill {
    height: 100%; border-radius: 4px;
    background: linear-gradient(90deg, var(--green), var(--yellow), var(--red));
  }
  .badge {
    display: inline-block; padding: 0.15rem 0.6rem; border-radius: 12px;
    font-size: 0.8rem; font-weight: 600;
  }
  .badge-pass { background: rgba(63,185,80,0.15); color: var(--green); }
  .badge-fail { background: rgba(248,81,73,0.15); color: var(--red); }
  .kv-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0.75rem; }
  .kv-item { }
  .kv-label { font-size: 0.8rem; color: var(--dim); text-transform: uppercase; letter-spacing: 0.04em; }
  .kv-value { font-size: 1.4rem; font-weight: 700; font-variant-numeric: tabular-nums; }
  .footer {
    text-align: center; color: var(--dim); font-size: 0.8rem;
    margin-top: 2rem; padding-top: 1rem; border-top: 1px solid var(--border);
  }
</style>
</head>
<body>
<div class="container">
`)

	b.WriteString(`<div class="header">
  <h1>Performance Report</h1>
  <div class="subtitle">Test ID: ` + id + `</div>
</div>
`)

	if s := r.Summary; s != nil {
		b.WriteString(`<div class="card">
  <h2>Throughput</h2>
  <div class="kv-grid">
    <div class="kv-item">
      <div class="kv-label">Total Records</div>
      <div class="kv-value">` + fmtNum(s.TotalRecords) + `</div>
    </div>
    <div class="kv-item">
      <div class="kv-label">Avg Throughput</div>
      <div class="kv-value">` + fmtNum(s.AvgThroughputRecPerSec) + ` <span style="font-size:0.7em;color:var(--dim)">rec/s</span></div>
    </div>
    <div class="kv-item">
      <div class="kv-label">Peak Throughput</div>
      <div class="kv-value">` + fmtNum(s.PeakThroughputRecPerSec) + ` <span style="font-size:0.7em;color:var(--dim)">rec/s</span></div>
    </div>
    <div class="kv-item">
      <div class="kv-label">Avg Bandwidth</div>
      <div class="kv-value">` + fmtFloat(s.AvgThroughputMBPerSec, 2) + ` <span style="font-size:0.7em;color:var(--dim)">MB/s</span></div>
    </div>
  </div>
</div>
`)

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

		b.WriteString(`<div class="card">
  <h2>Latency Distribution</h2>
  <table>
    <thead><tr><th>Percentile</th><th>Value (ms)</th><th>Distribution</th></tr></thead>
    <tbody>
`)
		for _, l := range latencies {
			pct := l.value / maxLatency * 100
			if pct > 100 {
				pct = 100
			}
			b.WriteString(fmt.Sprintf(`    <tr>
      <td>%s</td>
      <td class="metric-value">%.2f</td>
      <td><div class="bar-container"><div class="bar-fill" style="width:%.0f%%"></div></div></td>
    </tr>
`, l.label, l.value, pct))
		}
		b.WriteString(`    </tbody>
  </table>
</div>
`)

		errorColor := "var(--green)"
		if s.ErrorRate > 0.001 {
			errorColor = "var(--red)"
		} else if s.ErrorRate > 0 {
			errorColor = "var(--yellow)"
		}
		b.WriteString(fmt.Sprintf(`<div class="card">
  <h2>Reliability</h2>
  <div class="kv-grid">
    <div class="kv-item">
      <div class="kv-label">Error Rate</div>
      <div class="kv-value" style="color:%s">%.4f%%</div>
    </div>
    <div class="kv-item">
      <div class="kv-label">Total Records</div>
      <div class="kv-value">%s</div>
    </div>
  </div>
</div>
`, errorColor, s.ErrorRate*100, fmtNum(s.TotalRecords)))
	}

	if v := r.OverallSlaVerdict; v != nil {
		if v.Passed {
			b.WriteString(`<div class="card">
  <h2>SLA Verdict</h2>
  <p><span class="badge badge-pass">✓ PASSED</span> All SLA thresholds met</p>
</div>
`)
		} else {
			b.WriteString(`<div class="card">
  <h2>SLA Verdict</h2>
  <p style="margin-bottom:1rem"><span class="badge badge-fail">✖ FAILED</span> SLA violations detected</p>
`)
			if len(v.Violations) > 0 {
				b.WriteString(`  <table>
    <thead><tr><th>Metric</th><th>Threshold</th><th>Actual</th><th>Status</th></tr></thead>
    <tbody>
`)
				for _, viol := range v.Violations {
					b.WriteString(fmt.Sprintf(`    <tr>
      <td>%s</td>
      <td class="metric-value">%.2f</td>
      <td class="metric-value" style="color:var(--red)">%.2f</td>
      <td><span class="badge badge-fail">FAIL</span></td>
    </tr>
`, viol.Metric, viol.Threshold, viol.Actual))
				}
				b.WriteString(`    </tbody>
  </table>
`)
			}
			b.WriteString(`</div>
`)
		}
	}

	if len(r.Phases) > 0 {
		b.WriteString(`<div class="card">
  <h2>Phase Breakdown</h2>
  <table>
    <thead><tr><th>Phase</th><th>Status</th><th>Records</th><th>Throughput</th><th>P99 (ms)</th><th>Errors</th></tr></thead>
    <tbody>
`)
		for _, p := range r.Phases {
			phaseName := p.Name
			if phaseName == "" {
				phaseName = "main"
			}
			statusBadge := `<span class="badge badge-pass">` + p.Status + `</span>`
			if strings.EqualFold(p.Status, "FAILED") || strings.EqualFold(p.Status, "ERROR") {
				statusBadge = `<span class="badge badge-fail">` + p.Status + `</span>`
			}
			b.WriteString(fmt.Sprintf(`    <tr>
      <td>%s</td>
      <td>%s</td>
      <td class="metric-value">%s</td>
      <td class="metric-value">%s rec/s</td>
      <td class="metric-value">%s</td>
      <td class="metric-value">%.4f%%</td>
    </tr>
`, phaseName, statusBadge, fmtNum(p.RecordsSent), fmtFloat(p.ThroughputRecPerSec, 1), fmtFloat(p.P99LatencyMs, 2), p.ErrorRate*100))
		}
		b.WriteString(`    </tbody>
  </table>
</div>
`)
	}

	b.WriteString(fmt.Sprintf(`<div class="footer">
  Generated by <strong>kates report export %s --format html</strong>
</div>
</div>
</body>
</html>
`, id))

	return b.String()
}
