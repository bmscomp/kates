# Export Formats

After running a performance test, you have results. Raw numbers stored in a database. But results are only useful if they reach the people and systems that need them — your CI pipeline, your Grafana dashboard, your spreadsheet where you track regression trends, your audit report that proves compliance. Getting data from Kates into these targets requires export.

Kates supports three export formats, each optimized for a different workflow: CSV for human analysis in spreadsheets, JUnit XML for CI/CD pipeline integration, and JSON/CSV heatmap data for latency visualization. This chapter explains not just the format of each export, but the reasoning behind the design decisions and when to use each one.

## CSV: The Universal Data Format

### Why CSV?

CSV (Comma-Separated Values) is the lingua franca of data exchange. It is readable by every spreadsheet application, every data analysis tool, every programming language, and every human with a text editor. When you need maximum compatibility and long-term archival, CSV is the right choice.

Kates' CSV export is designed for two primary use cases: importing into spreadsheets for ad-hoc analysis and charting, and feeding into data pipelines for automated regression detection. The format is intentionally simple — a header row followed by one data row per test result — because simplicity is what makes CSV ubiquitous.

### The Column Schema

Each row represents a single `TestResult` from the test run. For multi-phase tests (STRESS, SPIKE), this means one row per phase, making it easy to chart throughput or latency progression across ramp-up steps.

| Column | Type | What It Measures |
|--------|------|------------------|
| `runId` | String | Unique test run identifier — ties all rows to the same run |
| `testType` | String | Test type (LOAD, STRESS, SPIKE, etc.) |
| `backend` | String | Which execution backend was used (native, trogdor) |
| `phase` | String | Phase name within multi-phase tests |
| `recordsSent` | long | Total records produced in this phase |
| `throughputRecPerSec` | double | Records per second achieved |
| `throughputMBPerSec` | double | Megabytes per second achieved |
| `avgLatencyMs` | double | Average produce latency |
| `p50LatencyMs` | double | Median latency (50th percentile) |
| `p95LatencyMs` | double | 95th percentile latency |
| `p99LatencyMs` | double | 99th percentile latency |
| `maxLatencyMs` | double | Maximum observed latency (worst case) |
| `error` | String | Error message (empty string if successful) |

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

Notice the summary section at the end, prefixed with `#`. This is a deliberate design choice: CSV parsers that respect the `#` comment convention will skip these lines, while humans reading the file can see the aggregate metrics at a glance. The summary includes the total record count, average and peak throughput, average and P99 latency, and the overall error rate.

### Working with CSV Exports

**In Excel or Google Sheets:** Import the CSV, then create a line chart with the `phase` column as the X-axis and `throughputRecPerSec` or `p99LatencyMs` as the Y-axis. For STRESS tests, this produces the classic throughput ramp curve that shows where the cluster starts saturating.

**For automated regression detection:** Write a script that compares the current test's CSV against a baseline CSV. Flag any run where P99 latency exceeds the baseline by more than 20% or throughput drops by more than 10%. This simple heuristic catches most regressions without requiring sophisticated statistical analysis.

**For historical trending:** Append each test run's CSV to a master CSV file, then use a tool like pandas or R to analyze trends over time. This is how you answer questions like "has our P99 latency been creeping up over the last 6 months?"

---

## JUnit XML: Making CI/CD Pipelines Kafka-Aware

### The Problem with CI/CD Integration

CI/CD pipelines understand pass and fail. They run unit tests, integration tests, and lint checks, and if anything fails, the build is red. But performance tests exist in a gray area — they do not have a clear pass/fail boundary. Is a P99 latency of 15ms a pass? 50ms? 100ms? It depends on your SLA.

JUnit XML solves this by mapping Kafka performance results to the JUnit test report format that every CI/CD system already understands. Each test result becomes a `<testcase>`, and SLA violations become `<failure>` elements. This means a performance regression shows up in your CI dashboard the same way a failing unit test does — immediately visible and blocking the pipeline.

### How the Mapping Works

The `JunitXmlExporter` translates Kates concepts to JUnit concepts:

| Kates Concept | JUnit Element | Purpose |
|---------------|---------------|---------|
| Test type (LOAD, STRESS, ...) | `<testsuite name>` | Groups related test cases |
| Each `TestResult` | `<testcase>` | One testcase per execution phase |
| Phase name or task ID | `<testcase name>` | Human-readable test case name |
| Duration (seconds) | `<testcase time>` | Execution time for the phase |
| Error message | `<failure>` on testcase | Error details if the phase failed |
| Each SLA violation | Separate `<testcase>` | Named `SLA-{metricName}` |
| Threshold vs actual | `<failure>` on SLA testcase | Shows expected vs observed value |

### Sample Output

```xml
<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="LOAD" tests="3" failures="1" errors="0">
  <testcase name="produce-phase" classname="kates.LOAD" time="120.500"/>
  <testcase name="consume-phase" classname="kates.LOAD" time="118.200"/>
  <testcase name="SLA-p99LatencyMs" classname="kates.sla">
    <failure message="p99LatencyMs threshold=100.00 actual=150.00"
            type="SlaViolation"/>
  </testcase>
</testsuite>
```

In this example, the produce and consume phases completed successfully, but the P99 latency SLA was violated (threshold was 100ms, actual was 150ms). In your CI dashboard, this appears as one test failure — making it immediately obvious that the latest change caused a performance regression.

### Integrating with CI/CD Platforms

The power of JUnit XML is its universality. Here is how to integrate it with the most common platforms:

**GitHub Actions:**

```yaml
- name: Run Kates performance gate
  run: |
    RESULT=$(curl -s http://kates:8080/api/tests -d @test-spec.json)
    echo "$RESULT" | jq -r '.junitXml' > test-results/kates-junit.xml

- name: Publish test results
  uses: dorny/test-reporter@v1
  with:
    name: Kafka Performance
    path: test-results/kates-junit.xml
    reporter: java-junit
```

**Jenkins:**

```groovy
stage('Performance Gate') {
    steps {
        sh 'curl -s http://kates:8080/api/tests -d @test-spec.json > result.json'
        sh 'cat result.json | jq -r .junitXml > test-results/kates-junit.xml'
        junit 'test-results/kates-junit.xml'
    }
}
```

The key insight is that SLA violations appear as test failures. This means you can gate deployments on performance criteria — if P99 latency exceeds your threshold, the build fails and the deploy does not proceed. This transforms performance testing from an afterthought into a first-class quality gate, equivalent to unit test coverage or linting.

---

## Latency Heatmap: Seeing What Percentiles Hide

### The Limitation of Percentile Metrics

Percentile metrics (P50, P95, P99) are the standard way to summarize latency distributions. They are useful because they compress a complex distribution into a few numbers that are easy to compare. But they also hide important information.

Consider two scenarios that produce identical P99 latency:

1. **Uniform distribution:** 99% of requests complete in 1-10ms, with the slowest 1% completing in 50ms.
2. **Bimodal distribution:** 50% of requests complete in 1ms (cache hits), 49% complete in 20ms (cache misses), and 1% complete in 50ms (cache miss + GC pause).

Both have a P99 of 50ms. But the underlying behavior is fundamentally different. Scenario 1 is a healthy system with a long tail. Scenario 2 has a cache problem that affects half of all requests. A latency heatmap makes this difference immediately visible: scenario 1 shows a dense band near the bottom with a thin tail, while scenario 2 shows two distinct bands.

### The Heatmap Data Structure

The `HeatmapExporter` produces a time-series of latency distributions. Each row represents a 1-second sampling window, and each column represents a latency bucket. The cell value is the count of requests that fell into that bucket during that second.

The `LatencyHeatmapData` record contains:

| Field | Type | What It Represents |
|-------|------|-------------------|
| `runId` | String | Test run identifier |
| `testType` | String | Test type |
| `bucketLabels` | `List<String>` | Human-readable bucket names ("0ms-1ms", "1ms-5ms", ...) |
| `bucketBoundaries` | `List<double[]>` | Min/max pairs for each bucket in milliseconds |
| `rows` | `List<HeatmapRow>` | One row per 1-second sampling window |

Each `HeatmapRow` contains a Unix timestamp, within the test phase, and an array of counts — one count per bucket:

```json
{
  "timestampMs": 1707800000000,
  "phase": "produce",
  "counts": [1200, 3500, 800, 200, 50, 10, 0, 0]
}
```

This row says: during the 1-second window starting at timestamp 1707800000000, 1,200 requests completed in 0-1ms, 3,500 in 1-5ms, 800 in 5-10ms, and so on. The fact that most requests fall in the 1-5ms bucket tells you that's the expected latency for this workload.

### JSON Export for Grafana

The JSON format is designed for Grafana heatmap panels:

```json
{
  "runId": "abc123",
  "testType": "LOAD",
  "bucketLabels": ["0ms-1ms", "1ms-5ms", "5ms-10ms", "10ms-50ms",
                    "50ms-100ms", "100ms-500ms", "500ms-1s", "1s+"],
  "bucketBoundaries": [[0,1], [1,5], [5,10], [10,50],
                        [50,100], [100,500], [500,1000], [1000,10000]],
  "rows": [
    {"timestampMs": 1707800000000, "phase": "produce",
     "counts": [1200, 3500, 800, 200, 50, 10, 0, 0]},
    {"timestampMs": 1707800001000, "phase": "produce",
     "counts": [1150, 3600, 750, 180, 40, 15, 2, 0]},
    {"timestampMs": 1707800002000, "phase": "produce",
     "counts": [1100, 3400, 900, 250, 80, 30, 5, 1]}
  ]
}
```

### CSV Export for Spreadsheets

The CSV format uses latency buckets as columns:

```csv
timestamp,phase,0ms-1ms,1ms-5ms,5ms-10ms,10ms-50ms,50ms-100ms,100ms-500ms,500ms-1s,1s+
1707800000000,produce,1200,3500,800,200,50,10,0,0
1707800001000,produce,1150,3600,750,180,40,15,2,0
1707800002000,produce,1100,3400,900,250,80,30,5,1
```

### Reading a Heatmap: What to Look For

**Healthy workload:** A dense horizontal band at low latency, thinning gradually toward higher latency. This shows a consistent, predictable latency distribution.

**Latency spike during disruption:** A sudden vertical stripe where requests shift into higher-latency buckets. In a resilience test, you will see this stripe at the moment the fault is injected, followed by a gradual return to the baseline band as the cluster recovers.

**Bimodal distribution:** Two distinct horizontal bands, indicating that requests follow two different latency paths. This often indicates a cache hit/miss pattern or separate fast-path/slow-path code paths in your Kafka producers.

**Progressive degradation:** The dense band gradually shifts upward over time. This is the signature of a STRESS test approaching cluster saturation — as throughput increases, latency increases until the system cannot keep up.

The heatmap is the single most information-dense visualization Kates produces. While percentile metrics tell you "how bad was the worst 1%?", the heatmap shows you the complete picture: how every request behaved at every moment during the test.
