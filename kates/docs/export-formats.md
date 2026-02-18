# Export Formats

Kates can export test results in three formats: CSV for spreadsheets, JUnit XML for CI/CD integration, and JSON heatmap data for latency visualization. This document describes each format, its structure, and how to use it.

## CSV Export

The `CsvExporter` produces a comma-separated file with one header row and one data row per test result. A summary section is appended at the end.

### Column Reference

| Column | Type | Description |
|--------|------|-------------|
| `runId` | String | Unique test run identifier |
| `testType` | String | Test type (LOAD, STRESS, SPIKE, etc.) |
| `backend` | String | Execution backend (native, trogdor) |
| `phase` | String | Phase name (for multi-phase tests like STRESS/SPIKE) |
| `recordsSent` | long | Total records produced |
| `throughputRecPerSec` | double | Records per second |
| `throughputMBPerSec` | double | Megabytes per second |
| `avgLatencyMs` | double | Average produce latency |
| `p50LatencyMs` | double | Median latency |
| `p95LatencyMs` | double | 95th percentile latency |
| `p99LatencyMs` | double | 99th percentile latency |
| `maxLatencyMs` | double | Maximum observed latency |
| `error` | String | Error message (empty if successful) |

### Sample Output

```csv
runId,testType,backend,phase,recordsSent,throughputRecPerSec,throughputMBPerSec,avgLatencyMs,p50LatencyMs,p95LatencyMs,p99LatencyMs,maxLatencyMs,error
abc123,STRESS,native,ramp-25%,250000,25000.1234,24.4141,3.2100,2.1000,8.5000,15.3000,45.0000,
abc123,STRESS,native,ramp-50%,250000,48500.5678,47.3633,4.5600,3.2000,12.0000,22.0000,65.0000,
abc123,STRESS,native,ramp-75%,250000,71200.9012,69.5322,6.8900,5.0000,18.0000,35.0000,120.0000,

# Summary
totalRecords,750000
avgThroughputRecPerSec,48233.5308
peakThroughputRecPerSec,71200.9012
avgLatencyMs,4.8867
p99LatencyMs,24.1000
errorRate,0.0000
```

The summary section uses a `#` prefix so CSV parsers that ignore comments will skip it automatically, while humans reading the file can see the aggregate metrics.

### Use Cases

- Import into Excel or Google Sheets for custom charting
- Archive test results for historical trend analysis
- Feed into data pipelines for automated regression detection

---

## JUnit XML Export

The `JunitXmlExporter` produces a JUnit-compatible XML file that maps each test result to a `<testcase>` element. SLA violations are emitted as `<failure>` elements. This format is natively supported by every major CI/CD platform.

### Structure

```xml
<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="LOAD" tests="3" failures="1" errors="0">
  <testcase name="produce-phase" classname="kates.LOAD" time="120.500"/>
  <testcase name="consume-phase" classname="kates.LOAD" time="118.200"/>
  <testcase name="round-trip-phase" classname="kates.LOAD" time="119.800">
    <failure message="Timeout during consume" type="Error"/>
  </testcase>
  <testcase name="SLA-p99LatencyMs" classname="kates.sla">
    <failure message="p99LatencyMs threshold=100.00 actual=150.00" type="SlaViolation"/>
  </testcase>
</testsuite>
```

### Mapping Rules

| JUnit Element | Kates Mapping |
|---------------|---------------|
| `<testsuite name>` | Test type (LOAD, STRESS, etc.) |
| `<testsuite tests>` | Number of `TestResult` objects |
| `<testsuite failures>` | Number of SLA violations |
| `<testcase name>` | Phase name or task ID |
| `<testcase classname>` | `kates.{testType}` |
| `<testcase time>` | Duration in seconds (computed from start/end timestamps) |
| `<failure>` on testcase | Error message from the test result |
| SLA `<testcase>` | One per SLA violation, named `SLA-{metric}` |
| SLA `<failure>` | Shows threshold vs actual value |

### CI/CD Integration

Place the JUnit XML output in your CI's test results directory:

**GitHub Actions:**

```yaml
- name: Run Kates performance test
  run: curl -s http://kates:8080/api/tests -d '{"type":"LOAD"}' | ...

- name: Upload test results
  uses: actions/upload-artifact@v3
  with:
    name: kates-results
    path: test-results/kates-junit.xml
```

**Jenkins:**

```groovy
junit 'test-results/kates-junit.xml'
```

The key advantage of JUnit XML is that SLA violations appear as test failures in your CI dashboard. A regression in P99 latency shows up the same way as a failing unit test — immediately visible and blocking the pipeline.

---

## Latency Heatmap Export

The `HeatmapExporter` produces latency distribution data suitable for heatmap visualization. Each row represents a 1-second sampling window, with counts for each latency bucket.

### Data Structure

The `LatencyHeatmapData` record contains:

| Field | Type | Description |
|-------|------|-------------|
| `runId` | String | Test run identifier |
| `testType` | String | Test type |
| `bucketLabels` | `List<String>` | Human-readable labels for each latency bucket (e.g., "0ms-1ms", "1ms-5ms") |
| `bucketBoundaries` | `List<double[]>` | Min/max pairs for each bucket in milliseconds |
| `rows` | `List<HeatmapRow>` | Time-series data points |

Each `HeatmapRow` contains:

| Field | Type | Description |
|-------|------|-------------|
| `timestampMs` | long | Unix timestamp in milliseconds |
| `phase` | String | Test phase name (for multi-phase tests) |
| `counts` | `long[]` | Number of requests in each latency bucket for this time window |

### JSON Format

The JSON export is designed for Grafana heatmap panels:

```json
{
  "runId": "abc123",
  "testType": "LOAD",
  "bucketLabels": ["0ms-1ms", "1ms-5ms", "5ms-10ms", "10ms-50ms", "50ms-100ms", "100ms-500ms", "500ms-1s", "1s+"],
  "bucketBoundaries": [[0, 1], [1, 5], [5, 10], [10, 50], [50, 100], [100, 500], [500, 1000], [1000, 10000]],
  "rows": [
    {"timestampMs": 1707800000000, "phase": "produce", "counts": [1200, 3500, 800, 200, 50, 10, 0, 0]},
    {"timestampMs": 1707800001000, "phase": "produce", "counts": [1150, 3600, 750, 180, 40, 15, 2, 0]},
    {"timestampMs": 1707800002000, "phase": "produce", "counts": [1100, 3400, 900, 250, 80, 30, 5, 1]}
  ]
}
```

### CSV Format

The CSV export uses columns for latency buckets and rows for time windows:

```csv
timestamp,phase,0ms-1ms,1ms-5ms,5ms-10ms,10ms-50ms,50ms-100ms,100ms-500ms,500ms-1s,1s+
1707800000000,produce,1200,3500,800,200,50,10,0,0
1707800001000,produce,1150,3600,750,180,40,15,2,0
1707800002000,produce,1100,3400,900,250,80,30,5,1
```

### Visualization

The heatmap data is designed for two visualization targets:

1. **Grafana heatmap panel** — import the JSON data into a Grafana datasource. The bucket boundaries allow Grafana to correctly render the latency distribution as a color-coded heatmap where each cell represents the count of requests in a latency bucket during a 1-second window.

2. **Spreadsheet conditional formatting** — import the CSV and apply conditional formatting to create a visual heatmap. Higher counts get darker colors, making it easy to see where latency concentrations shift during test phases.

The heatmap is especially valuable for identifying latency bimodality — where requests split into two distinct latency groups (e.g., cached vs uncached paths) — which is invisible in simple percentile aggregates but immediately obvious in a heatmap.
