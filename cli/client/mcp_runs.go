package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Reads the kates mcp test-run tools need that the client had no method or
// type for. They are kept apart from the CLI's own methods, whose types drop
// fields these tools depend on: TestSpec has no "throughput", TestRun no
// labels, ReportSummary no totalErrors, and DisruptionList sends a "limit"
// parameter the backend does not read (it pages with "page" and "size").

// MCPRun is a test run as GET /api/tests/{id} and the /api/tests list return
// it (domain/TestRun.java). Spec is kept raw: it is the request merged with
// the test type's defaults, and a tool compares whole specs, so no field may
// be dropped by a struct that does not name it. The list returns runs
// without results (EntityMapper.toDomainSummary).
type MCPRun struct {
	ID           string            `json:"id"`
	TestType     string            `json:"testType"`
	Status       string            `json:"status"`
	Backend      string            `json:"backend"`
	ScenarioName string            `json:"scenarioName"`
	CreatedAt    string            `json:"createdAt"`
	Labels       map[string]string `json:"labels,omitempty"`
	Spec         json.RawMessage   `json:"spec,omitempty"`
	Results      []MCPRunTask      `json:"results,omitempty"`
}

// MCPRunTask is one task of a run (domain/TestResult.java), as stored: the
// integrity result is not persisted, so it is not read here.
type MCPRunTask struct {
	TaskID                  string  `json:"taskId"`
	PhaseName               string  `json:"phaseName"`
	Status                  string  `json:"status"`
	RecordsSent             int64   `json:"recordsSent"`
	ThroughputRecordsPerSec float64 `json:"throughputRecordsPerSec"`
	ThroughputMBPerSec      float64 `json:"throughputMBPerSec"`
	AvgLatencyMs            float64 `json:"avgLatencyMs"`
	P50LatencyMs            float64 `json:"p50LatencyMs"`
	P95LatencyMs            float64 `json:"p95LatencyMs"`
	P99LatencyMs            float64 `json:"p99LatencyMs"`
	MaxLatencyMs            float64 `json:"maxLatencyMs"`
	StartTime               string  `json:"startTime"`
	EndTime                 string  `json:"endTime"`
	Error                   string  `json:"error"`
}

// MCPRunsPage is one page of GET /api/tests (api/PagedResponse.java). The
// backend sends no page count.
type MCPRunsPage struct {
	Items []MCPRun `json:"items"`
	Page  int      `json:"page"`
	Size  int      `json:"size"`
	Total int64    `json:"total"`
	Count int      `json:"count"`
}

// MCPRunSummary is report/ReportSummary.java, totalErrors included.
type MCPRunSummary struct {
	TotalRecords            int64   `json:"totalRecords"`
	AvgThroughputRecPerSec  float64 `json:"avgThroughputRecPerSec"`
	PeakThroughputRecPerSec float64 `json:"peakThroughputRecPerSec"`
	AvgThroughputMBPerSec   float64 `json:"avgThroughputMBPerSec"`
	AvgLatencyMs            float64 `json:"avgLatencyMs"`
	P50LatencyMs            float64 `json:"p50LatencyMs"`
	P95LatencyMs            float64 `json:"p95LatencyMs"`
	P99LatencyMs            float64 `json:"p99LatencyMs"`
	MaxLatencyMs            float64 `json:"maxLatencyMs"`
	TotalErrors             int64   `json:"totalErrors"`
	ErrorRate               float64 `json:"errorRate"`
}

// MCPRunsComparison is GET /api/tests/reports/compare
// (report/ComparisonReport.java): a summary per run, and the percentage change
// of a few metrics from the first run named to the last.
type MCPRunsComparison struct {
	BaselineRunID string             `json:"baselineRunId"`
	Runs          []MCPComparedRun   `json:"runs"`
	Deltas        map[string]float64 `json:"deltas"`
}

type MCPComparedRun struct {
	RunID   string         `json:"runId"`
	Summary *MCPRunSummary `json:"summary"`
}

// MCPAdvisorRecommendation is one rule the backend's advisor fired
// (service/AdvisorService.java). Fix and evidence may be absent.
type MCPAdvisorRecommendation struct {
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Fix      string `json:"fix"`
	Evidence string `json:"evidence"`
}

// MCPActivityDisruption is one row of GET /api/disruptions.
type MCPActivityDisruption struct {
	ID        string `json:"id"`
	PlanName  string `json:"planName"`
	Status    string `json:"status"`
	SlaGrade  string `json:"slaGrade"`
	CreatedAt string `json:"createdAt"`
}

// MCPActivityDisruptionsPage is one page of GET /api/disruptions, newest
// first. The backend sends no total.
type MCPActivityDisruptionsPage struct {
	Items []MCPActivityDisruption `json:"items"`
	Page  int                     `json:"page"`
	Size  int                     `json:"size"`
	Count int                     `json:"count"`
}

// MCPActivityAuditPage is one page of GET /api/audit, newest first. Total
// counts the rows the backend read, at most 500 (service/AuditService.java).
type MCPActivityAuditPage struct {
	Items []AuditEntry `json:"items"`
	Page  int          `json:"page"`
	Size  int          `json:"size"`
	Total int          `json:"total"`
	Count int          `json:"count"`
}

// MCPRun reads one run. The backend polls a run that is still active before
// answering and saves any change it finds (TestOrchestrator.refreshStatus).
func (c *Client) MCPRun(ctx context.Context, id string) (*MCPRun, error) {
	path, err := pathf("/api/tests/%s", id)
	if err != nil {
		return nil, err
	}
	return get[*MCPRun](c, ctx, path)
}

// MCPRunsPage reads one page of runs, newest first. The backend applies the
// type filter and ignores status when both are given (TestResource.listTests).
func (c *Client) MCPRunsPage(ctx context.Context, testType, status string, page, size int) (*MCPRunsPage, error) {
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("size", strconv.Itoa(size))
	if testType != "" {
		query.Set("type", testType)
	}
	if status != "" {
		query.Set("status", status)
	}
	return get[*MCPRunsPage](c, ctx, withQuery("/api/tests", query))
}

// MCPRunSummary reads a run's report summary.
func (c *Client) MCPRunSummary(ctx context.Context, id string) (*MCPRunSummary, error) {
	path, err := pathf("/api/tests/%s/report/summary", id)
	if err != nil {
		return nil, err
	}
	return get[*MCPRunSummary](c, ctx, path)
}

// MCPRunReportMarkdown reads a run's full report as the backend renders it in
// Markdown.
func (c *Client) MCPRunReportMarkdown(ctx context.Context, id string) (string, error) {
	path, err := pathf("/api/tests/%s/report/markdown", id)
	if err != nil {
		return "", err
	}
	data, err := c.getBytes(ctx, path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// MCPRunAdvisor reads the recommendations of the backend's rule engine for a
// run.
func (c *Client) MCPRunAdvisor(ctx context.Context, id string) ([]MCPAdvisorRecommendation, error) {
	path, err := pathf("/api/tests/%s/advisor", id)
	if err != nil {
		return nil, err
	}
	return get[[]MCPAdvisorRecommendation](c, ctx, path)
}

// MCPRunsCompare compares runs, in the order given: the backend's deltas run
// from the first to the last. The ids travel as one comma-separated query
// value, so an id that is empty or holds a comma is refused before anything
// is sent: it would name other runs, or none.
func (c *Client) MCPRunsCompare(ctx context.Context, ids []string) (*MCPRunsComparison, error) {
	if len(ids) < 2 {
		return nil, fmt.Errorf("compare needs at least two run ids, got %d", len(ids))
	}
	for _, id := range ids {
		if id == "" || strings.ContainsAny(id, ", ") {
			return nil, fmt.Errorf("run id %q is %w", id, ErrInvalidPathSegment)
		}
	}
	return get[*MCPRunsComparison](c, ctx, withQuery("/api/tests/reports/compare", url.Values{"ids": {strings.Join(ids, ",")}}))
}

// MCPActivityDisruptions reads one page of disruption reports, newest first.
func (c *Client) MCPActivityDisruptions(ctx context.Context, page, size int) (*MCPActivityDisruptionsPage, error) {
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("size", strconv.Itoa(size))
	return get[*MCPActivityDisruptionsPage](c, ctx, withQuery("/api/disruptions", query))
}

// MCPActivityAudit reads one page of audit rows written at or after since, an
// ISO-8601 instant the backend parses with Instant.parse; it ignores one it
// cannot parse and returns every row, so callers send a UTC "Z" timestamp.
func (c *Client) MCPActivityAudit(ctx context.Context, since string, page, size int) (*MCPActivityAuditPage, error) {
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("size", strconv.Itoa(size))
	if since != "" {
		query.Set("since", since)
	}
	return get[*MCPActivityAuditPage](c, ctx, withQuery("/api/audit", query))
}
