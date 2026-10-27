package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerMCPRunTools registers the test-run tools of plan §4.1 (list_runs,
// get_run, assess_run, kates_activity), the kates://runs/{id}/report.md
// resource of §4.2 and the diagnose_run prompt of §4.3.
//
// Every tool goes through addReadTool and reads only with GET. get_run and
// assess_run read the run with GET /api/tests/{id}, which makes the backend
// poll a run that is still active and save what it finds
// (TestResource.java:458-464, TestOrchestrator.refreshStatus); both
// descriptions say so. Third-party text (task errors, scenario and phase
// names, labels, plan names, audit details, backend messages) is fenced;
// ids, types and statuses are cleaned so an agent can pass them back.
func registerMCPRunTools(s *mcp.Server, deps *mcpDeps) {
	registerMCPListRuns(s, deps)
	registerMCPAssessRun(s, deps)
	registerMCPKatesActivity(s, deps)
	registerMCPRunReport(s, deps)
	registerMCPDiagnoseRun(s, deps)
}

// registerMCPListRuns registers list_runs and get_run.
func registerMCPListRuns(s *mcp.Server, deps *mcpDeps) {
	addReadTool(s, deps, &mcp.Tool{
		Name:        "list_runs",
		Title:       "Test runs",
		InputSchema: mcpListRunsInputSchema(),
		Description: "Lists Kates test runs, newest first, a page at a time, optionally only one test type " +
			"and one status. Each row has the run id, type, status, creation time, backend, scenario name " +
			"(fenced, third-party) and specHash, a digest of the stored effective spec: runs with the same " +
			"specHash ran with the same stored spec. Use get_run for one run's spec and measurements. With both " +
			"type and status, the backend can filter by only one, so this tool reads the newest 1000 runs of " +
			"the type and filters them by status; total is then a lower bound unless totalExact is true. Only reads.",
	}, mcpListRuns, mcpCaveatMergedSpecOnly)

	addReadTool(s, deps, &mcp.Tool{
		Name:  "get_run",
		Title: "Test run",
		Description: "One test run: type, status, the effective spec the backend stored, the request's own " +
			"spec fields, each task's measurements and error, and the report summary. The stored spec is the " +
			"request merged with the test type's defaults, and holds a field no type has a default for only when " +
			"the request set it; requestedSpec holds only what the request set, so the two show which values the " +
			"defaults filled in. A run stored before the backend kept the request has " +
			"no requestedSpec, and notCarried then names the request fields that backend never copied into the " +
			"spec, so what a request said for them cannot be shown. An INTEGRITY run that has ended has an integrity " +
			"result: the verdict, records sent, acknowledged, consumed, lost and duplicated, producer and consumer " +
			"RTO, RPO where it was measured, and the first lost ranges and timeline events. For a run still PENDING, RUNNING or STOPPING, " +
			"reading it makes the backend poll its tasks and save any change of status or results, the same " +
			"update its 5-second reconciler makes; a finished run is not changed. Task ids and errors, scenario " +
			"and phase names, labels, and spec values the backend never validated are third-party text and " +
			"fenced. Otherwise only reads.",
	}, mcpGetRun, mcpCaveatMergedSpecOnly, mcpCaveatSummaryAveragesStartedTasks)
}

// registerMCPRunReport registers the kates://runs/{id}/report.md resource.
func registerMCPRunReport(s *mcp.Server, deps *mcpDeps) {
	addReadResourceTemplate(s, deps, &mcp.ResourceTemplate{
		URITemplate: mcpRunReportURITemplate,
		Name:        "run-report",
		Title:       "Test run report",
		Description: "One test run's full report in Markdown, as the Kates backend renders it: metadata, " +
			"summary, phases and SLA verdict. The body is third-party text inside one untrusted fence.",
		MIMEType: "text/markdown",
	}, map[string]mcpResourceVar{"id": mcpIDVar}, mcpRunReport, mcpCaveatSummaryAveragesStartedTasks)
}

const mcpRunReportURITemplate = "kates://runs/{id}/report.md"

// mcpRunNotCarried are the request fields applyTypeDefaults did not copy into
// the merged spec before the backend kept the request: it copied the other
// fourteen, and a run stored then shows these at their Java defaults whatever
// its request said. A run with a requestedSpec was merged with all of them,
// and its spec shows each only when the request set it. In TestSpec.java's
// order.
var mcpRunNotCarried = []string{
	"consumerGroup", "targetThroughput", "fetchMinBytes", "fetchMaxWaitMs",
	"enableIdempotence", "enableTransactions", "enableCrc",
}

const (
	mcpRunsDefaultPageSize = 10
	mcpRunsMaxPageSize     = 25
	mcpRunsMaxPage         = 10_000
	// With both filters the backend applies only the type (TestResource.java:
	// 249-261), so the status is filtered here over the newest runs of the
	// type, read in the backend's largest pages (size is capped at 200).
	mcpRunsScanPageSize = 200
	mcpRunsMaxScan      = 1000

	mcpRunMaxTasks  = 20
	mcpRunMaxLabels = 10

	mcpRunMaxIntegrity      = 5
	mcpRunMaxLostRanges     = 10
	mcpRunMaxIntegrityEvent = 20
)

// ---- list_runs --------------------------------------------------------------

type mcpListRunsIn struct {
	Type   string `json:"type,omitempty" jsonschema:"only runs of this test type"`
	Status string `json:"status,omitempty" jsonschema:"only runs stored in this status; a cancelled run is stored as FAILED"`
	Page   int    `json:"page,omitempty" jsonschema:"page number, from 0 (default 0)"`
	Size   int    `json:"size,omitempty" jsonschema:"runs per page, 1 to 25 (default 10)"`
}

type mcpListRunsOut struct {
	Filters    mcpListRunsFilters `json:"filters"`
	Page       int                `json:"page"`
	Size       int                `json:"size"`
	Total      int64              `json:"total" jsonschema:"runs matching the filters; a lower bound when totalExact is false"`
	TotalExact bool               `json:"totalExact" jsonschema:"false when both filters were given and the runs read did not cover every run of the type"`
	HasMore    bool               `json:"hasMore" jsonschema:"true when the next page has runs"`
	Scanned    int                `json:"scanned,omitempty" jsonschema:"with both filters: how many runs of the type were read to filter by status"`
	Runs       []mcpRunRow        `json:"runs"`
}

type mcpListRunsFilters struct {
	Type   string `json:"type,omitempty"`
	Status string `json:"status,omitempty"`
}

// mcpRunRow is one run in a list.
type mcpRunRow struct {
	ID        string       `json:"id" jsonschema:"the run id, for get_run, assess_run and kates://runs/{id}/report.md"`
	Type      string       `json:"type"`
	Status    string       `json:"status" jsonschema:"PENDING, RUNNING, STOPPING, DONE or FAILED, as stored"`
	CreatedAt string       `json:"createdAt"`
	Backend   string       `json:"backend,omitempty" jsonschema:"the benchmark backend that ran it"`
	Scenario  mcpUntrusted `json:"scenario,omitempty" jsonschema:"the scenario's name, for a run started from a scenario"`
	SpecHash  string       `json:"specHash,omitempty" jsonschema:"digest of the stored effective spec; equal for runs with the same stored spec"`

	// outlastsReaper is not shown: it marks a row that needs the reaper caveat.
	outlastsReaper bool
}

func mcpListRunsInputSchema() *jsonschema.Schema {
	return mcpInputSchema[mcpListRunsIn]("list_runs", func(s *jsonschema.Schema) {
		s.Properties["type"].Enum = mcpAnys(mcpTestTypes)
		s.Properties["status"].Enum = mcpAnys(mcpRunStatuses)
		mcpBounds(s.Properties["page"], 0, mcpRunsMaxPage)
		mcpBounds(s.Properties["size"], 1, mcpRunsMaxPageSize)
	})
}

func mcpListRuns(ctx context.Context, call *mcpCall, in mcpListRunsIn) (mcpListRunsOut, error) {
	var out mcpListRunsOut
	if in.Type != "" && !slices.Contains(mcpTestTypes, in.Type) {
		return out, mcpInvalidArgument("type must be one of the Kates test types: "+strings.Join(mcpTestTypes, ", ")+".", in.Type)
	}
	if in.Status != "" && !slices.Contains(mcpRunStatuses, in.Status) {
		return out, mcpInvalidArgument("status must be one of "+strings.Join(mcpRunStatuses, ", ")+".", in.Status)
	}
	if in.Page < 0 || in.Page > mcpRunsMaxPage {
		return out, mcpInvalidArgument(fmt.Sprintf("page must be from 0 to %d.", mcpRunsMaxPage), fmt.Sprint(in.Page))
	}
	size := in.Size
	if size == 0 {
		size = mcpRunsDefaultPageSize
	}
	if size < 1 || size > mcpRunsMaxPageSize {
		return out, mcpInvalidArgument(fmt.Sprintf("size must be from 1 to %d.", mcpRunsMaxPageSize), fmt.Sprint(in.Size))
	}
	out = mcpListRunsOut{Filters: mcpListRunsFilters{Type: in.Type, Status: in.Status}, Page: in.Page, Size: size}

	var rows []client.MCPRun
	if in.Type != "" && in.Status != "" {
		scan, err := mcpScanRunsByStatus(ctx, call, in.Type, in.Status, in.Page, size)
		if err != nil {
			return out, err
		}
		rows, out.Total, out.TotalExact, out.HasMore, out.Scanned = scan.rows, scan.total, scan.exact, scan.hasMore, scan.scanned
	} else {
		p, err := call.Client().MCPRunsPage(ctx, in.Type, in.Status, in.Page, size)
		if err != nil {
			return out, err
		}
		if p == nil {
			return out, mcpRunEmptyAnswer()
		}
		rows, out.Total, out.TotalExact = p.Items, p.Total, true
		out.HasMore = int64(in.Page+1)*int64(size) < p.Total
	}
	out.Runs = make([]mcpRunRow, 0, len(rows))
	for _, r := range rows {
		out.Runs = append(out.Runs, mcpRunRowFrom(call, r))
	}
	out.Runs = mcpCap(call, out.Runs, size)
	mcpRunRowsCaveats(call, out.Runs)
	return out, nil
}

type mcpRunScan struct {
	rows    []client.MCPRun
	total   int64
	exact   bool
	hasMore bool
	scanned int
}

// mcpScanRunsByStatus is list_runs with both filters: the newest runs of the
// type, read in large pages, filtered by status here, until the requested
// page and one more match are found, the type runs out, or mcpRunsMaxScan
// runs were read.
func mcpScanRunsByStatus(ctx context.Context, call *mcpCall, typ, status string, page, size int) (mcpRunScan, error) {
	var scan mcpRunScan
	start, end := page*size, (page+1)*size
	var matches []client.MCPRun
	for p := 0; scan.scanned < mcpRunsMaxScan; p++ {
		pg, err := call.Client().MCPRunsPage(ctx, typ, "", p, mcpRunsScanPageSize)
		if err != nil {
			return scan, err
		}
		if pg == nil {
			return scan, mcpRunEmptyAnswer()
		}
		scan.scanned += len(pg.Items)
		for _, r := range pg.Items {
			if r.Status == status {
				matches = append(matches, r)
			}
		}
		if len(pg.Items) < mcpRunsScanPageSize || int64(scan.scanned) >= pg.Total {
			scan.exact = true
			break
		}
		if len(matches) > end {
			break
		}
	}
	if !scan.exact && len(matches) <= end {
		// The scan stopped at its cap: older runs of the type were not read.
		call.MarkTruncated()
	}
	scan.total = int64(len(matches))
	scan.hasMore = len(matches) > end
	if start < len(matches) {
		scan.rows = matches[start:min(end, len(matches))]
	}
	return scan, nil
}

func mcpRunRowFrom(call *mcpCall, r client.MCPRun) mcpRunRow {
	hash, _ := mcpRunSpecKey(r.Spec)
	return mcpRunRow{
		ID:             mcpSanitizeLine(r.ID, 36),
		Type:           mcpSanitizeLine(r.TestType, 32),
		Status:         mcpSanitizeLine(r.Status, 16),
		CreatedAt:      mcpSanitizeLine(r.CreatedAt, 40),
		Backend:        mcpSanitizeLine(r.Backend, 32),
		Scenario:       call.FenceN(r.ScenarioName, 100),
		SpecHash:       mcpRunSpecHash(hash),
		outlastsReaper: mcpRunOutlastsReaper(&r),
	}
}

// mcpRunRowsCaveats adds the caveats a list of runs needs: a FAILED row may
// be a reaped or a cancelled run, and an older Kates API reaps an active row
// set to last 30 minutes or more before it ends.
func mcpRunRowsCaveats(call *mcpCall, rows []mcpRunRow) {
	for _, r := range rows {
		if r.Status == "FAILED" {
			call.Caveat(mcpCaveatReaperDeadline, mcpCaveatCancelStoredAsFailed)
		}
		if r.outlastsReaper {
			call.Caveat(mcpCaveatReaperDeadline)
		}
	}
}

// mcpRunOutlastsReaper reports whether a run that has not finished is set to
// last 30 minutes or more, an INTEGRITY run's read-back included
// (mcpPlannedDurationMs). An older Kates API fails such a run before it ends,
// 30 minutes after its creation, time spent PENDING included
// (V24__run_planned_duration.sql:8-10); a current one gives it a deadline of
// its own, and these tools cannot tell which the API is. A scenario run's
// stored spec is its base spec, which shows none of its phases; they run one
// after another (caveat scenario-phases-in-turn), so it may last their
// durations added up, and counts as set to last that long. A spec without a
// duration is judged by its type: only ENDURANCE defaults to 30 minutes or
// more (application.properties:58-114).
func mcpRunOutlastsReaper(run *client.MCPRun) bool {
	if run.Status != "RUNNING" && run.Status != "PENDING" {
		return false
	}
	if run.ScenarioName != "" {
		return true
	}
	var w struct {
		DurationMs *int64 `json:"durationMs"`
	}
	if len(bytes.TrimSpace(run.Spec)) > 0 && json.Unmarshal(run.Spec, &w) == nil && w.DurationMs != nil {
		planned, ok := mcpPlannedDurationMs(run.TestType, *w.DurationMs)
		return ok && planned >= mcpReaperOlderLimitMs
	}
	return run.TestType == "ENDURANCE"
}

// ---- get_run ----------------------------------------------------------------

type mcpGetRunIn struct {
	RunID mcpID `json:"run_id" jsonschema:"the test run id, from list_runs"`
}

type mcpGetRunOut struct {
	Run           mcpRunHead           `json:"run"`
	Finished      bool                 `json:"finished" jsonschema:"true when the status is DONE or FAILED"`
	Spec          mcpRunSpec           `json:"spec" jsonschema:"the effective spec the backend stored: the request merged with the test type's defaults"`
	SpecHash      string               `json:"specHash" jsonschema:"digest of the stored spec, as list_runs shows it"`
	RequestedSpec *mcpRunRequestedSpec `json:"requestedSpec,omitempty" jsonschema:"the request's own spec fields, as the backend kept them beside the merged spec; absent for a run stored before it kept them"`
	NotCarried    []string             `json:"notCarried,omitempty" jsonschema:"only for a run with no requestedSpec: request fields the backend that stored it never copied into the spec, so the run did not use what a request said for them"`
	TaskCount     int                  `json:"taskCount"`
	Tasks         []mcpRunTaskOut      `json:"tasks" jsonschema:"the run's tasks as stored, in order; at most 20"`
	Summary       *mcpRunSummaryOut    `json:"summary,omitempty" jsonschema:"the report summary; absent when it could not be read"`
	SummaryError  mcpErrorCode         `json:"summaryError,omitempty" jsonschema:"why the summary could not be read"`
	Integrity     []mcpRunIntegrityOut `json:"integrity,omitempty" jsonschema:"the integrity result of each task that carries one, in task order: an INTEGRITY run's, once it has ended; absent while it runs, and for a run stored before the backend kept the result; at most 5"`
}

type mcpRunHead struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Status    string         `json:"status" jsonschema:"PENDING, RUNNING, STOPPING, DONE or FAILED, as stored"`
	CreatedAt string         `json:"createdAt"`
	Backend   string         `json:"backend,omitempty"`
	Scenario  mcpUntrusted   `json:"scenario,omitempty" jsonschema:"the scenario's name, for a run started from a scenario"`
	Labels    []mcpUntrusted `json:"labels,omitempty" jsonschema:"the run's labels as key=value, from its scenario; at most 10"`
}

// mcpRunSpec is the stored spec. Every field the backend's merge sets is a
// pointer, so a spec stored by another backend version that lacks one shows
// it absent rather than as 0.
type mcpRunSpec struct {
	Topic             string  `json:"topic,omitempty" jsonschema:"the topic the run used; absent when the stored name is not a legal Kafka topic name (see invalid)"`
	TopicDefaulted    bool    `json:"topicDefaulted" jsonschema:"true when the spec names no topic and the run used <type>-test, lowercased"`
	NumRecords        *int64  `json:"numRecords,omitempty"`
	RecordSize        *int64  `json:"recordSize,omitempty" jsonschema:"bytes"`
	Throughput        *int64  `json:"throughput,omitempty" jsonschema:"target records per second for each producer task; -1 means no limit. SPIKE and CAPACITY ignore it and run unthrottled, and INTEGRATION_CDC does not read it"`
	Acks              *string `json:"acks,omitempty" jsonschema:"absent when the stored value is not one Kafka accepts (see invalid)"`
	BatchSize         *int64  `json:"batchSize,omitempty" jsonschema:"bytes"`
	LingerMs          *int64  `json:"lingerMs,omitempty"`
	CompressionType   *string `json:"compressionType,omitempty" jsonschema:"absent when the stored value is not one Kafka accepts (see invalid)"`
	NumProducers      *int64  `json:"numProducers,omitempty" jsonschema:"read only by STRESS and CAPACITY, which start this many producer tasks; every other type starts at most one, whatever this says"`
	NumConsumers      *int64  `json:"numConsumers,omitempty" jsonschema:"not read by any run type: LOAD and ENDURANCE start one consumer task and the other types no separate one, whatever this says"`
	DurationMs        *int64  `json:"durationMs,omitempty"`
	ReplicationFactor *int64  `json:"replicationFactor,omitempty"`
	Partitions        *int64  `json:"partitions,omitempty"`
	MinInsyncReplicas *int64  `json:"minInsyncReplicas,omitempty"`
	// The fields below appear only for a run with a requestedSpec, and only
	// when its request set them: the backend has no default for them. A
	// backend that stored no requestedSpec dropped them and stored their Java
	// defaults (mcpRunNotCarried), which would read as values the run used.
	TargetThroughput   *int64         `json:"targetThroughput,omitempty" jsonschema:"the rate as requested under its other name, the one the CLI and scenario files send; throughput is the rate the run used, taken from this when the request set no throughput. Absent when the request did not set it"`
	ConsumerGroup      mcpUntrusted   `json:"consumerGroup,omitempty" jsonschema:"the consumer group the request named; absent when it named none, and then each consumer used a group of its own: the task id with -group appended, and -group-integrity for an INTEGRITY run's (one created before the backend gave each run its own group joined integrity-cg-integrity). An INTEGRITY run's consumer joins a named group with -integrity appended"`
	FetchMinBytes      *int64         `json:"fetchMinBytes,omitempty" jsonschema:"the consumers' fetch.min.bytes; absent when the request set none, and then the Kafka client's default, 1"`
	FetchMaxWaitMs     *int64         `json:"fetchMaxWaitMs,omitempty" jsonschema:"the consumers' fetch.max.wait.ms; absent when the request set none, and then the Kafka client's default, 500"`
	EnableIdempotence  *bool          `json:"enableIdempotence,omitempty" jsonschema:"what the request set: false turned the producers' idempotence off. Absent when it set none, and then the Kafka client decided: it turns idempotence on whenever acks is all"`
	EnableTransactions *bool          `json:"enableTransactions,omitempty" jsonschema:"true when the run's producers were transactional, and then a LOAD or ENDURANCE consumer read with read_committed; absent when the request did not set it"`
	EnableCrc          *bool          `json:"enableCrc,omitempty" jsonschema:"whether an INTEGRITY run checked each record's CRC; absent when the request did not set it, and then an INTEGRITY run did. No other type checks one"`
	Invalid            []mcpUntrusted `json:"invalid,omitempty" jsonschema:"stored topic, acks or compressionType values that are not legal Kafka values, as field=value; the backend does not validate a scenario's base spec, and an older one did not validate compressionType sent over gRPC"`
}

// mcpRunRequestedSpec is the request's own spec fields, as the backend kept
// them beside the merged spec (TestSpec.explicitFields): a field is present
// only when the request set it. Text is checked as in the stored spec.
type mcpRunRequestedSpec struct {
	Topic              string         `json:"topic,omitempty" jsonschema:"absent when the request named none, or named one that is not a legal Kafka topic name (see invalid)"`
	NumRecords         *int64         `json:"numRecords,omitempty"`
	RecordSize         *int64         `json:"recordSize,omitempty"`
	Throughput         *int64         `json:"throughput,omitempty"`
	Acks               *string        `json:"acks,omitempty"`
	BatchSize          *int64         `json:"batchSize,omitempty"`
	LingerMs           *int64         `json:"lingerMs,omitempty"`
	CompressionType    *string        `json:"compressionType,omitempty"`
	NumProducers       *int64         `json:"numProducers,omitempty"`
	NumConsumers       *int64         `json:"numConsumers,omitempty"`
	DurationMs         *int64         `json:"durationMs,omitempty"`
	ReplicationFactor  *int64         `json:"replicationFactor,omitempty"`
	Partitions         *int64         `json:"partitions,omitempty"`
	MinInsyncReplicas  *int64         `json:"minInsyncReplicas,omitempty"`
	ConsumerGroup      mcpUntrusted   `json:"consumerGroup,omitempty"`
	TargetThroughput   *int64         `json:"targetThroughput,omitempty"`
	FetchMinBytes      *int64         `json:"fetchMinBytes,omitempty"`
	FetchMaxWaitMs     *int64         `json:"fetchMaxWaitMs,omitempty"`
	EnableIdempotence  *bool          `json:"enableIdempotence,omitempty"`
	EnableTransactions *bool          `json:"enableTransactions,omitempty"`
	EnableCrc          *bool          `json:"enableCrc,omitempty"`
	Invalid            []mcpUntrusted `json:"invalid,omitempty" jsonschema:"requested topic, acks or compressionType values that are not legal Kafka values, as field=value"`
}

type mcpRunTaskOut struct {
	TaskID              mcpUntrusted `json:"taskId" jsonschema:"the task's id; a scenario run's task ids hold its phase names"`
	Phase               mcpUntrusted `json:"phase,omitempty" jsonschema:"the task's phase; a scenario run takes phase names from its scenario"`
	Status              string       `json:"status"`
	RecordsSent         int64        `json:"recordsSent"`
	ThroughputRecPerSec float64      `json:"throughputRecPerSec"`
	ThroughputMBPerSec  float64      `json:"throughputMBPerSec"`
	AvgLatencyMs        float64      `json:"avgLatencyMs"`
	P50LatencyMs        float64      `json:"p50LatencyMs"`
	P95LatencyMs        float64      `json:"p95LatencyMs"`
	P99LatencyMs        float64      `json:"p99LatencyMs"`
	MaxLatencyMs        float64      `json:"maxLatencyMs"`
	StartTime           string       `json:"startTime,omitempty"`
	EndTime             string       `json:"endTime,omitempty"`
	Error               mcpUntrusted `json:"error,omitempty" jsonschema:"the error the task ended with"`
}

// mcpRunSummaryOut is the report summary without the two fields the backend
// always sends as 0 (p999LatencyMs and durationMs, MetricUtils.java:112,116).
type mcpRunSummaryOut struct {
	TotalRecords            int64   `json:"totalRecords" jsonschema:"records sent, summed over tasks"`
	AvgThroughputRecPerSec  float64 `json:"avgThroughputRecPerSec" jsonschema:"the mean of the rates of the tasks that have started, not their sum"`
	PeakThroughputRecPerSec float64 `json:"peakThroughputRecPerSec" jsonschema:"the fastest task's rate"`
	AvgThroughputMBPerSec   float64 `json:"avgThroughputMBPerSec" jsonschema:"the mean of the rates of the tasks that have started"`
	AvgLatencyMs            float64 `json:"avgLatencyMs" jsonschema:"the mean latency of the tasks that measured latency, weighted by records"`
	P50LatencyMs            float64 `json:"p50LatencyMs" jsonschema:"the p50 of the tasks that measured latency; with several, the highest"`
	P95LatencyMs            float64 `json:"p95LatencyMs" jsonschema:"the p95 of the tasks that measured latency; with several, the highest"`
	P99LatencyMs            float64 `json:"p99LatencyMs" jsonschema:"the p99 of the tasks that measured latency; with several, the highest"`
	MaxLatencyMs            float64 `json:"maxLatencyMs" jsonschema:"the slowest task's maximum"`
	TasksWithErrors         int64   `json:"tasksWithErrors" jsonschema:"tasks that ended with an error (the backend's totalErrors)"`
	ErrorRate               float64 `json:"errorRate" jsonschema:"tasksWithErrors divided by records sent; not a share of failed records"`
}

func mcpGetRun(ctx context.Context, call *mcpCall, in mcpGetRunIn) (mcpGetRunOut, error) {
	var out mcpGetRunOut
	if err := mcpValidateID("run_id", in.RunID); err != nil {
		return out, err
	}
	run, err := mcpReadRun(ctx, call, string(in.RunID))
	if err != nil {
		return out, err
	}
	mcpRunCaveats(call, run)
	key, _ := mcpRunSpecKey(run.Spec)
	spec, err := mcpRunSpecFrom(call, run)
	if err != nil {
		return out, err
	}
	requested, err := mcpRunRequestedFrom(call, run)
	if err != nil {
		return out, err
	}
	out = mcpGetRunOut{
		Run:           mcpRunHeadFrom(call, run),
		Finished:      mcpRunFinished(run.Status),
		Spec:          spec,
		SpecHash:      mcpRunSpecHash(key),
		RequestedSpec: requested,
		TaskCount:     len(run.Results),
	}
	if requested == nil {
		out.NotCarried = append([]string(nil), mcpRunNotCarried...)
	}
	tasks := make([]mcpRunTaskOut, 0, len(run.Results))
	for _, t := range run.Results {
		tasks = append(tasks, mcpRunTaskFrom(call, t))
	}
	out.Tasks = mcpCap(call, tasks, mcpRunMaxTasks)
	var integrity []mcpRunIntegrityOut
	for _, t := range run.Results {
		if t.Integrity != nil {
			integrity = append(integrity, mcpRunIntegrityFrom(call, t.TaskID, *t.Integrity))
		}
	}
	out.Integrity = mcpCap(call, integrity, mcpRunMaxIntegrity)

	summary, err := call.Client().MCPRunSummary(ctx, string(in.RunID))
	switch {
	case err != nil:
		out.SummaryError = call.deps.classify(mcpRunReportErr(err)).Code
		call.deps.logger.Warn("get_run: summary unavailable", "error", err)
	case summary == nil:
		out.SummaryError = mcpErrBackend
	default:
		s := mcpRunSummaryFrom(*summary)
		out.Summary = &s
	}

	// A run with many tasks and long errors can outgrow one result; tasks are
	// dropped from the end (taskCount stays whole), then labels, then the
	// integrity results' timelines and lost ranges (their counts stay whole).
	// The integrity verdicts and counts are never dropped.
	mcpShrink(call, &out,
		func() bool { return mcpHalve(&out.Tasks, 0) },
		func() bool { return mcpHalve(&out.Run.Labels, 0) },
		func() bool { return mcpRunIntegrityShrink(out.Integrity) },
	)
	return out, nil
}

func mcpRunHeadFrom(call *mcpCall, run *client.MCPRun) mcpRunHead {
	h := mcpRunHead{
		ID:        mcpSanitizeLine(run.ID, 36),
		Type:      mcpSanitizeLine(run.TestType, 32),
		Status:    mcpSanitizeLine(run.Status, 16),
		CreatedAt: mcpSanitizeLine(run.CreatedAt, 40),
		Backend:   mcpSanitizeLine(run.Backend, 32),
		Scenario:  call.FenceN(run.ScenarioName, 100),
	}
	keys := make([]string, 0, len(run.Labels))
	for k := range run.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Labels = append(h.Labels, call.FenceN(k+"="+run.Labels[k], 200))
	}
	h.Labels = mcpCap(call, h.Labels, mcpRunMaxLabels)
	if len(h.Labels) == 0 {
		h.Labels = nil
	}
	return h
}

// mcpRunSpecWire is a spec as the backend serialises it: the stored spec
// from domain/TestSpec.java's fields that are set (through its getters, and
// so with every field, for a run stored before the backend kept the
// request), or the requested one through TestSpec.explicitFields, which uses
// the same names.
type mcpRunSpecWire struct {
	Topic              *string `json:"topic"`
	NumRecords         *int64  `json:"numRecords"`
	RecordSize         *int64  `json:"recordSize"`
	Throughput         *int64  `json:"throughput"`
	Acks               *string `json:"acks"`
	BatchSize          *int64  `json:"batchSize"`
	LingerMs           *int64  `json:"lingerMs"`
	CompressionType    *string `json:"compressionType"`
	NumProducers       *int64  `json:"numProducers"`
	NumConsumers       *int64  `json:"numConsumers"`
	DurationMs         *int64  `json:"durationMs"`
	ReplicationFactor  *int64  `json:"replicationFactor"`
	Partitions         *int64  `json:"partitions"`
	MinInsyncReplicas  *int64  `json:"minInsyncReplicas"`
	ConsumerGroup      *string `json:"consumerGroup"`
	TargetThroughput   *int64  `json:"targetThroughput"`
	FetchMinBytes      *int64  `json:"fetchMinBytes"`
	FetchMaxWaitMs     *int64  `json:"fetchMaxWaitMs"`
	EnableIdempotence  *bool   `json:"enableIdempotence"`
	EnableTransactions *bool   `json:"enableTransactions"`
	EnableCrc          *bool   `json:"enableCrc"`
}

// mcpRunDecodeSpec reads raw into w; nothing, or JSON null, leaves w empty and
// reports false.
func mcpRunDecodeSpec(raw json.RawMessage, w *mcpRunSpecWire) (bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false, nil
	}
	return true, json.Unmarshal(trimmed, w)
}

// mcpRunSpecFrom reads the stored spec. A run without a topic used one named
// after its type (TestOrchestrator.java:1614,1941-1942). The topic, acks and
// compressionType are shown as identifiers only when they hold values Kafka
// accepts (mcpKafkaTopicRE, mcpAcksRE, mcpCompressionRE, the values
// TestSpec's bean validation accepts). A stored run can hold others: a
// schedule saved by a PUT, which runs no bean validation
// (ScheduleResource.java:137-138), fires its spec as sent, a Kates API
// without the check of a scenario's specs (TestOrchestrator.java:1126-1130)
// stored a scenario's base spec as sent (TestOrchestrator.java:347-355,
// 373-377), and one without the check of a gRPC request's fields
// (GrpcTestService.java:116-121) stored a compressionType sent over gRPC
// unchecked. So another value is third-party text and goes, fenced, into
// invalid. The seven fields a backend without
// requestedSpec never carried are shown only for a run that has one. call may
// be nil when only the topic's kind is needed.
func mcpRunSpecFrom(call *mcpCall, run *client.MCPRun) (mcpRunSpec, error) {
	var w mcpRunSpecWire
	if _, err := mcpRunDecodeSpec(run.Spec, &w); err != nil {
		return mcpRunSpec{}, err
	}
	carried, err := mcpRunDecodeSpec(run.RequestedSpec, &mcpRunSpecWire{})
	if err != nil {
		return mcpRunSpec{}, err
	}
	spec := mcpRunSpec{
		NumRecords:        w.NumRecords,
		RecordSize:        w.RecordSize,
		Throughput:        w.Throughput,
		BatchSize:         w.BatchSize,
		LingerMs:          w.LingerMs,
		NumProducers:      w.NumProducers,
		NumConsumers:      w.NumConsumers,
		DurationMs:        w.DurationMs,
		ReplicationFactor: w.ReplicationFactor,
		Partitions:        w.Partitions,
		MinInsyncReplicas: w.MinInsyncReplicas,
	}
	invalid := func(field, v string) {
		if call != nil {
			spec.Invalid = append(spec.Invalid, call.FenceN(field+"="+v, 300))
		}
	}
	switch {
	case w.Topic == nil || *w.Topic == "":
		spec.Topic = strings.ToLower(mcpSanitizeLine(run.TestType, 32)) + "-test"
		spec.TopicDefaulted = true
	case mcpKafkaTopicRE.MatchString(*w.Topic):
		spec.Topic = *w.Topic
	default:
		invalid("topic", *w.Topic)
	}
	if w.Acks != nil {
		if mcpAcksRE.MatchString(*w.Acks) {
			spec.Acks = w.Acks
		} else {
			invalid("acks", *w.Acks)
		}
	}
	if w.CompressionType != nil {
		if mcpCompressionRE.MatchString(*w.CompressionType) {
			spec.CompressionType = w.CompressionType
		} else {
			invalid("compressionType", *w.CompressionType)
		}
	}
	if carried {
		spec.TargetThroughput = w.TargetThroughput
		spec.FetchMinBytes = w.FetchMinBytes
		spec.FetchMaxWaitMs = w.FetchMaxWaitMs
		spec.EnableIdempotence = w.EnableIdempotence
		spec.EnableTransactions = w.EnableTransactions
		spec.EnableCrc = w.EnableCrc
		if w.ConsumerGroup != nil && call != nil {
			spec.ConsumerGroup = call.FenceN(*w.ConsumerGroup, 300)
		}
	}
	return spec, nil
}

// mcpRunRequestedFrom reads the request's own spec fields, nil when the run
// has none: a run stored before the backend kept them, or a backend that
// predates them. The request passed the same validation as the stored spec
// (a scenario's base spec, none), so text is checked the same way, and the
// consumer group, which the backend checks only for length, is fenced.
func mcpRunRequestedFrom(call *mcpCall, run *client.MCPRun) (*mcpRunRequestedSpec, error) {
	var w mcpRunSpecWire
	ok, err := mcpRunDecodeSpec(run.RequestedSpec, &w)
	if err != nil || !ok {
		return nil, err
	}
	r := &mcpRunRequestedSpec{
		NumRecords:         w.NumRecords,
		RecordSize:         w.RecordSize,
		Throughput:         w.Throughput,
		BatchSize:          w.BatchSize,
		LingerMs:           w.LingerMs,
		NumProducers:       w.NumProducers,
		NumConsumers:       w.NumConsumers,
		DurationMs:         w.DurationMs,
		ReplicationFactor:  w.ReplicationFactor,
		Partitions:         w.Partitions,
		MinInsyncReplicas:  w.MinInsyncReplicas,
		TargetThroughput:   w.TargetThroughput,
		FetchMinBytes:      w.FetchMinBytes,
		FetchMaxWaitMs:     w.FetchMaxWaitMs,
		EnableIdempotence:  w.EnableIdempotence,
		EnableTransactions: w.EnableTransactions,
		EnableCrc:          w.EnableCrc,
	}
	check := func(field string, v *string, re *regexp.Regexp) *string {
		switch {
		case v == nil:
			return nil
		case re.MatchString(*v):
			return v
		}
		r.Invalid = append(r.Invalid, call.FenceN(field+"="+*v, 300))
		return nil
	}
	if t := check("topic", w.Topic, mcpKafkaTopicRE); t != nil {
		r.Topic = *t
	}
	r.Acks = check("acks", w.Acks, mcpAcksRE)
	r.CompressionType = check("compressionType", w.CompressionType, mcpCompressionRE)
	if w.ConsumerGroup != nil {
		r.ConsumerGroup = call.FenceN(*w.ConsumerGroup, 300)
	}
	return r, nil
}

func mcpRunTaskFrom(call *mcpCall, t client.MCPRunTask) mcpRunTaskOut {
	return mcpRunTaskOut{
		// A scenario run's task ids are the run id and the phase name
		// (TestOrchestrator.java:1758), which nothing validates.
		TaskID:              call.FenceN(t.TaskID, 128),
		Phase:               call.FenceN(t.PhaseName, 64),
		Status:              mcpSanitizeLine(t.Status, 16),
		RecordsSent:         t.RecordsSent,
		ThroughputRecPerSec: mcpRunFinite(t.ThroughputRecordsPerSec),
		ThroughputMBPerSec:  mcpRunFinite(t.ThroughputMBPerSec),
		AvgLatencyMs:        mcpRunFinite(t.AvgLatencyMs),
		P50LatencyMs:        mcpRunFinite(t.P50LatencyMs),
		P95LatencyMs:        mcpRunFinite(t.P95LatencyMs),
		P99LatencyMs:        mcpRunFinite(t.P99LatencyMs),
		MaxLatencyMs:        mcpRunFinite(t.MaxLatencyMs),
		StartTime:           mcpSanitizeLine(t.StartTime, 40),
		EndTime:             mcpSanitizeLine(t.EndTime, 40),
		Error:               call.FenceN(t.Error, 300),
	}
}

// mcpRunIntegrityOut is one task's integrity result
// (domain/IntegrityResult.java, DataIntegrityVerifier.java).
type mcpRunIntegrityOut struct {
	TaskID              mcpUntrusted              `json:"taskId" jsonschema:"the task that carries the result"`
	Verdict             string                    `json:"verdict" jsonschema:"the first that applies of DATA_LOSS (lostRecords above 0), CORRUPTION (crcFailures), ORDERING_VIOLATION (outOfOrderCount), DUPLICATES_DETECTED (duplicateRecords), else PASS"`
	TotalSent           int64                     `json:"totalSent"`
	TotalAcked          int64                     `json:"totalAcked" jsonschema:"records the brokers acknowledged"`
	TotalConsumed       int64                     `json:"totalConsumed" jsonschema:"records read back, duplicates included"`
	LostRecords         int64                     `json:"lostRecords" jsonschema:"acknowledged records that were never read back"`
	DuplicateRecords    int64                     `json:"duplicateRecords" jsonschema:"records read back more than once, counted beyond the first"`
	DataLossPercent     float64                   `json:"dataLossPercent" jsonschema:"lostRecords as a percentage of totalSent"`
	LostRangeCount      int                       `json:"lostRangeCount" jsonschema:"ranges of lost sequence numbers the backend stored; it stores at most the first 1000, while lostRecords counts every lost record"`
	LostRanges          []mcpRunLostRange         `json:"lostRanges,omitempty" jsonschema:"the first lost ranges, at most 10"`
	ProducerRtoMs       float64                   `json:"producerRtoMs" jsonschema:"the longest time the producer's sends kept failing; 0 when none failed"`
	ConsumerRtoMs       float64                   `json:"consumerRtoMs" jsonschema:"from the consumer's first gap in reading to when it read again; 0 when it had none"`
	MaxRtoMs            *float64                  `json:"maxRtoMs,omitempty" jsonschema:"the larger of the two RTOs; absent when the backend sent none"`
	RpoMs               *float64                  `json:"rpoMs,omitempty" jsonschema:"how far before the fault started the oldest lost record was sent; 0 when none sent before it was lost. Absent when not measured: the run did not know when a fault started (a plain INTEGRITY run, or one a separate disruption hit)"`
	OutOfOrderCount     int64                     `json:"outOfOrderCount"`
	CrcFailures         int64                     `json:"crcFailures"`
	OrderingVerified    bool                      `json:"orderingVerified" jsonschema:"whether the run checked ordering; outOfOrderCount means nothing when false"`
	CrcVerified         bool                      `json:"crcVerified" jsonschema:"whether the run checked each record's CRC; crcFailures means nothing when false"`
	IdempotenceEnabled  bool                      `json:"idempotenceEnabled"`
	TransactionsEnabled bool                      `json:"transactionsEnabled"`
	TimelineCount       int                       `json:"timelineCount" jsonschema:"events the backend stored"`
	Timeline            []mcpRunIntegrityEventOut `json:"timeline,omitempty" jsonschema:"the first events: CRC_FAILURE, OUT_OF_ORDER, LOST_RANGE and SUMMARY, at most 20"`
}

type mcpRunLostRange struct {
	FromSeq int64 `json:"fromSeq"`
	ToSeq   int64 `json:"toSeq"`
	Count   int64 `json:"count"`
}

type mcpRunIntegrityEventOut struct {
	TimestampMs int64  `json:"timestampMs" jsonschema:"Unix milliseconds"`
	Type        string `json:"type"`
	Detail      string `json:"detail,omitempty" jsonschema:"the backend's own text, made of the event's numbers"`
}

func mcpRunIntegrityFrom(call *mcpCall, taskID string, ir client.IntegrityResult) mcpRunIntegrityOut {
	out := mcpRunIntegrityOut{
		TaskID:              call.FenceN(taskID, 128),
		Verdict:             mcpSanitizeLine(ir.Verdict, 32),
		TotalSent:           ir.TotalSent,
		TotalAcked:          ir.TotalAcked,
		TotalConsumed:       ir.TotalConsumed,
		LostRecords:         ir.LostRecords,
		DuplicateRecords:    ir.DuplicateRecords,
		DataLossPercent:     mcpRunFinite(ir.DataLossPercent),
		LostRangeCount:      len(ir.LostRanges),
		ProducerRtoMs:       mcpRunFinite(ir.ProducerRtoMs),
		ConsumerRtoMs:       mcpRunFinite(ir.ConsumerRtoMs),
		OutOfOrderCount:     ir.OutOfOrderCount,
		CrcFailures:         ir.CrcFailures,
		OrderingVerified:    ir.OrderingVerified,
		CrcVerified:         ir.CrcVerified,
		IdempotenceEnabled:  ir.IdempotenceEnabled,
		TransactionsEnabled: ir.TransactionsEnabled,
		TimelineCount:       len(ir.Timeline),
	}
	if v, ok := ir.MeasuredMaxRtoMs(); ok {
		v = mcpRunFinite(v)
		out.MaxRtoMs = &v
	}
	if v, ok := ir.MeasuredRpoMs(); ok {
		v = mcpRunFinite(v)
		out.RpoMs = &v
	}
	ranges := make([]mcpRunLostRange, 0, len(ir.LostRanges))
	for _, r := range ir.LostRanges {
		ranges = append(ranges, mcpRunLostRange(r))
	}
	if len(ranges) > 0 {
		out.LostRanges = mcpCap(call, ranges, mcpRunMaxLostRanges)
	}
	events := make([]mcpRunIntegrityEventOut, 0, len(ir.Timeline))
	for _, e := range ir.Timeline {
		events = append(events, mcpRunIntegrityEventOut{
			TimestampMs: e.TimestampMs,
			Type:        mcpSanitizeLine(e.Type, 32),
			Detail:      mcpSanitizeLine(e.Detail, 120),
		})
	}
	if len(events) > 0 {
		out.Timeline = mcpCap(call, events, mcpRunMaxIntegrityEvent)
	}
	return out
}

// mcpRunIntegrityShrink halves the longest timeline, then the longest list
// of lost ranges, and reports whether it dropped anything.
func mcpRunIntegrityShrink(results []mcpRunIntegrityOut) bool {
	for i := range results {
		if mcpHalve(&results[i].Timeline, 0) {
			return true
		}
	}
	for i := range results {
		if mcpHalve(&results[i].LostRanges, 0) {
			return true
		}
	}
	return false
}

func mcpRunSummaryFrom(s client.MCPRunSummary) mcpRunSummaryOut {
	return mcpRunSummaryOut{
		TotalRecords:            s.TotalRecords,
		AvgThroughputRecPerSec:  mcpRunFinite(s.AvgThroughputRecPerSec),
		PeakThroughputRecPerSec: mcpRunFinite(s.PeakThroughputRecPerSec),
		AvgThroughputMBPerSec:   mcpRunFinite(s.AvgThroughputMBPerSec),
		AvgLatencyMs:            mcpRunFinite(s.AvgLatencyMs),
		P50LatencyMs:            mcpRunFinite(s.P50LatencyMs),
		P95LatencyMs:            mcpRunFinite(s.P95LatencyMs),
		P99LatencyMs:            mcpRunFinite(s.P99LatencyMs),
		MaxLatencyMs:            mcpRunFinite(s.MaxLatencyMs),
		TasksWithErrors:         s.TotalErrors,
		ErrorRate:               mcpRunFinite(s.ErrorRate),
	}
}

// mcpReadRun reads one run and holds the backend to the id asked for, so no
// later read of this call follows an id the backend chose.
func mcpReadRun(ctx context.Context, call *mcpCall, id string) (*client.MCPRun, error) {
	run, err := call.Client().MCPRun(ctx, id)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, mcpRunEmptyAnswer()
	}
	if run.ID != id {
		return nil, &mcpToolError{Code: mcpErrBackend, Message: "The Kates API answered with a run other than the one asked for."}
	}
	return run, nil
}

// mcpRunCaveats adds the caveats one run's data carries.
func mcpRunCaveats(call *mcpCall, run *client.MCPRun) {
	switch {
	case run.TestType == "LOAD":
		call.Caveat(mcpCaveatLoadSingleProducer)
	case run.TestType == "INTEGRITY":
		call.Caveat(mcpCaveatIntegrityFromStoredResult)
	case strings.HasPrefix(run.TestType, "TUNE_"):
		call.Caveat(mcpCaveatTuningOneMeasurement)
	}
	if run.Status == "FAILED" {
		call.Caveat(mcpCaveatReaperDeadline, mcpCaveatCancelStoredAsFailed)
	}
	if mcpRunOutlastsReaper(run) {
		call.Caveat(mcpCaveatReaperDeadline)
	}
	if run.ScenarioName != "" {
		call.Caveat(mcpCaveatScenarioPhasesInTurn)
	}
}

func mcpRunFinished(status string) bool { return status == "DONE" || status == "FAILED" }

// ---- kates://runs/{id}/report.md --------------------------------------------

// mcpRunReport reads a run's Markdown report. Its caveats come from the
// report's own metadata table (ReportGenerator.java:206-218,384-391).
func mcpRunReport(ctx context.Context, call *mcpCall, vars map[string]string) (string, error) {
	md, err := call.Client().MCPRunReportMarkdown(ctx, vars["id"])
	if err != nil {
		return "", mcpRunReportErr(err)
	}
	run := mcpRunReportMetadata(md)
	run.ID = vars["id"]
	mcpRunCaveats(call, run)
	return md, nil
}

// mcpRunReportMetadata reads the run's type, status and scenario from the
// metadata table the backend writes first: runId, testType, backend, status,
// then scenarioName for a scenario run, then the labels as "label.<key>" rows
// (ReportGenerator.java:206-218). Only rows of that table and before the
// first label count, so neither a label nor a later section (a phase may be
// named anything) can pose as one of them.
func mcpRunReportMetadata(md string) *client.MCPRun {
	run := &client.MCPRun{}
	value := func(line, key string) (string, bool) {
		v, ok := strings.CutPrefix(line, "| "+key+" | ")
		return strings.TrimSuffix(strings.TrimSpace(v), " |"), ok
	}
	lines := strings.Split(md, "\n")
	start := slices.Index(lines, "## Metadata")
	if start < 0 {
		return run
	}
	inTable := false
	for _, line := range lines[start+1:] {
		switch {
		case !inTable && line == "":
			continue
		case !strings.HasPrefix(line, "|") || strings.HasPrefix(line, "| label."):
			return run
		}
		inTable = true
		if v, ok := value(line, "testType"); ok && run.TestType == "" {
			run.TestType = v
		}
		if v, ok := value(line, "status"); ok && run.Status == "" {
			run.Status = v
		}
		if v, ok := value(line, "scenarioName"); ok && run.ScenarioName == "" {
			run.ScenarioName = v
		}
	}
	return run
}

// ---- helpers ----------------------------------------------------------------

// mcpRunSpecKey is the canonical form of a stored spec: its JSON with keys
// sorted and numbers as sent. Two runs have the same stored spec when their
// keys are equal. false when there is no spec.
func mcpRunSpecKey(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", false
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		return "", false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// mcpRunSpecHash is a short digest of a spec key, for comparing runs by eye.
func mcpRunSpecHash(key string) string {
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:6])
}

// mcpRunReportErr maps the report endpoints' answer for a missing run. They
// throw IllegalArgumentException("Test run not found: …"), which the
// backend's exception mapper turns into a 400 (ReportResource.java:119-120;
// GlobalExceptionMapper.java:31-33), so a missing run would otherwise read
// as a bad argument.
func mcpRunReportErr(err error) error {
	var he *client.HTTPError
	if errors.As(err, &he) && he.StatusCode == http.StatusBadRequest && strings.Contains(he.Error(), "Test run not found") {
		return &mcpToolError{Code: mcpErrNotFound, Message: "Kates has no test run with this id.", Detail: he.Error(), cause: err}
	}
	return err
}

func mcpRunEmptyAnswer() error {
	return &mcpToolError{Code: mcpErrBackend, Message: "The Kates API returned an empty answer.", Retryable: true}
}

// mcpRunFinite keeps a value the backend sent as NaN or infinity (which JSON
// cannot carry) from failing the result.
func mcpRunFinite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// ---- caveats ----------------------------------------------------------------

const (
	mcpCaveatSummaryAveragesStartedTasks mcpCaveatID = "summary-averages-started-tasks"
	mcpCaveatIntegrityFromStoredResult   mcpCaveatID = "integrity-from-stored-result"
	mcpCaveatCancelStoredAsFailed        mcpCaveatID = "cancel-stored-as-failed"
	mcpCaveatScenarioPhasesInTurn        mcpCaveatID = "scenario-phases-in-turn"
)

// mcpCaveatsRunsList holds the caveats only list_runs, get_run and the run report use, part of
// mcpCaveatsRuns.
var mcpCaveatsRunsList = []mcpCaveat{
	{
		ID: mcpCaveatSummaryAveragesStartedTasks,
		Text: "A run's summary is built from its tasks rather than measured over the run as a whole: throughput is " +
			"the mean of the rates of the tasks that have started (a LOAD run's producer and consumer alike; not the " +
			"sum over STRESS producers), and peak is the fastest task. A task that has not started is PENDING with " +
			"no records, as a scenario's later phase is until its turn, so while a scenario runs its throughput is " +
			"that of the phases under way or done, and a phase that has not started reads 0 in the report's phases. " +
			"A task that a cancel or a failure ends before its turn is stored FAILED, and counts as a rate of 0. " +
			"An older Kates API counts a task that has not started as a rate of 0 too. Latency comes only from the " +
			"tasks that measured it, so a LOAD or ENDURANCE run's is its producer's; a consumer records none. Each " +
			"percentile is the highest such task's, exact with one producer and an upper bound with several; the " +
			"average is weighted by records, and maximum latency is the slowest task's. errorRate is the number of " +
			"tasks that ended with an error divided by the records sent, not a share of failed records. The backend " +
			"always sends p99.9 and duration as 0.",
		Refs: []string{
			mcpAnchoredRef(mcpJava+"util/MetricUtils.java:62-179",
				"public static ReportSummary computeSummary(", "filter(r -> !notStarted(r))",
				"result.getStatus() == TestResult.TaskStatus.PENDING", "private static double highest("),
			mcpAnchoredRef(mcpJava+"report/ReportGenerator.java:220-249,283-294",
				"report.setSummary(MetricUtils.computeSummary(results));",
				"summaries.put(phase, MetricUtils.computeSummary(rows))"),
			mcpAnchoredRef(mcpJava+"engine/TestOrchestrator.java:415-424,2089-2105",
				"TestResult.TaskStatus.PENDING : TestResult.TaskStatus.RUNNING",
				"private static TestRun withUnfinishedTasksFailed("),
		},
	},
	{
		ID: mcpCaveatIntegrityFromStoredResult,
		Text: "An INTEGRITY run's integrity result is made when its consumer has read the records back, after " +
			"the run's producing, so a run still under way has none yet. A run stored before the Kates API kept " +
			"that result has none at all: get_run shows no integrity, its report has no Data Integrity section, " +
			"and its SLA verdict treats any data-loss, RTO or RPO limit as met, because a missing value is skipped " +
			"rather than failed. The backend stores at most the first 1,000 ranges of lost records, while " +
			"lostRecords counts every one. RPO is measured only for a run that knew when its fault started, a " +
			"resilience run's; a plain INTEGRITY run, or one a separate disruption hit, has none.",
		Refs: []string{
			mcpAnchoredRef(mcpJava+"engine/NativeKafkaBackend.java:411-431",
				"private void runIntegrity(", "state.integrityResult = state.verifier.verify("),
			mcpAnchoredRef(mcpJava+"engine/DataIntegrityVerifier.java:51,224-227",
				"static final int MAX_LOST_RANGES = 1000;", "if (chaosStartNanos <= 0) {"),
			mcpAnchoredRef(mcpJava+"persistence/EntityMapper.java:34-43,216-223,243",
				"entity.setIntegrityJson(integrityJson);",
				".withIntegrity(fromJson(INTEGRITY_JSON, entity.getIntegrityJson(), IntegrityResult.class));",
				"DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false"),
			"kates/src/main/resources/db/migration/V25__test_result_integrity.sql:1-13",
			mcpJava + "report/ReportGenerator.java:106-132,479-484",
			mcpAnchoredRef(mcpJava+"engine/SlaEvaluator.java:83-104",
				"only an INTEGRITY task measures these", "metrics.rpoMs() >= 0"),
		},
	},
	{
		ID: mcpCaveatCancelStoredAsFailed,
		Text: "A cancelled run is stored as FAILED: there is no CANCELLED status, so cancel saves FAILED, answers " +
			"FAILED with reason cancelled, and gives each task that had not finished the error \"Cancelled by user\". " +
			"A FAILED run may therefore have been cancelled rather than have failed. That task error says which, " +
			"except for a run cancelled before its tasks existed; a cancel through the REST API also leaves a CANCEL " +
			"audit row.",
		Refs: []string{
			mcpAnchoredRef(mcpJava+"engine/TestOrchestrator.java:2040-2118",
				`"Cancelled by user"`, `EventKind.FAILED, "cancelled"`, "return run.withResults(updatedResults);"),
			mcpAnchoredRef(mcpJava+"api/TestResource.java:497-542",
				`@Path("/{id}/cancel")`, `auditService.record("CANCEL"`, `"Test cancelled; it is stored as FAILED"`),
			mcpJava + "domain/TestResult.java:25-31",
		},
	},
	{
		ID: mcpCaveatScenarioPhasesInTurn,
		Text: "A scenario run stores its base spec merged with the type's defaults, and as requestedSpec the base " +
			"spec as the scenario sent it. Each phase resolves its own spec from the base spec as sent and its own " +
			"fields, without the type's defaults, and those specs are not stored: the spec shown is not what every " +
			"phase ran, and a rate it shows may be a type default that no phase used. The phases start only " +
			"producers: the backend refuses a scenario that sets consumerGroup, a fetch setting or enableCrc: true, " +
			"and the producer options reach every phase. A WARMUP, STEADY or COOLDOWN phase runs at its rate " +
			"(throughput, or targetThroughput without it), unthrottled without one, and a SPIKE phase unthrottled " +
			"whatever its rate. A RAMP phase runs rampSteps producers, the k-th at k times its rate divided by " +
			"rampSteps, each for the phase's duration divided by rampSteps. A current Kates API runs the phases one " +
			"after another in the order sent, each once the durations of the phases before it have passed, even " +
			"if they stopped early at their record count, and a RAMP phase's steps in turn within it, so its load " +
			"rises a step at a time. A task whose turn has not come is PENDING, with the startTime it is due to " +
			"start at. That API refuses with 400 a SPIKE phase that sets a rate of its own, and a RAMP phase " +
			"without a rate, with more than 100 steps or with more steps than records a second. An older Kates API " +
			"starts every phase, and every step of a RAMP phase, at once, so they overlap and a RAMP phase's load " +
			"does not rise; some older ones also run the phases a current one refuses, the k-th step of a RAMP " +
			"phase without a rate at k records a second, so that phase sends almost nothing. /api/health does not " +
			"say which the API is, but a scenario run's tasks do: on an older API they all start within moments " +
			"of one another.",
		Refs: []string{
			mcpAnchoredRef(mcpJava+"engine/TestOrchestrator.java:341-343,391-402,404-420",
				"applyTypeDefaults(type, scenario.getBaseSpec())", ".withRequestedSpec(",
				"long phaseStartMs = System.currentTimeMillis();", "scenario.resolveSpecForPhase(phase)",
				"phaseStartMs = saturatedSum(phaseStartMs", "backend.submit(task)", "TestResult.TaskStatus.PENDING"),
			mcpAnchoredRef(mcpJava+"engine/TestOrchestrator.java:1452-1480,1552-1591,1601-1613",
				"MAX_RAMP_STEPS = 100", "scenarioInapplicableFields(TestScenario scenario", "is a SPIKE phase",
				"has none (", "check no record CRCs"),
			mcpAnchoredRef(mcpJava+"engine/TestOrchestrator.java:1738-1807", "List<BenchmarkTask> buildPhaseTask(",
				"int baseTarget = Math.max(1, spec.getThroughput() / steps);", "saturatedSum(startAtMs, s * stepMs)",
				`taskId + "-spike"`),
			mcpAnchoredRef(mcpJava+"domain/TestScenario.java:108-183",
				"TestSpec base = baseSpec != null ? baseSpec : new TestSpec();", "private TestSpec copySpec("),
			mcpAnchoredRef(mcpJava+"domain/ScenarioPhase.java:21-26", "private int rampSteps = 1;"),
			mcpAnchoredRef(mcpJava+"engine/NativeKafkaBackend.java:129-133,192-202,247-257,464-465",
				"Thread.ofVirtual()", "awaitStart(task, state)", "task.getStartAtMs() - System.currentTimeMillis()",
				"1_000_000_000L / task.getTargetMessagesPerSec()"),
			mcpAnchoredRef(mcpJava+"engine/TrogdorBackend.java:110-121,178-179",
				"spec.setStartMs(task.getStartAtMs())", "Integer.MAX_VALUE"),
		},
	},
}

// mcpCaveatsRuns holds the caveats only this group's tools use (see mcpCaveats in
// mcp_caveats.go): those of list_runs, get_run and the run report here, then
// assess_run's and kates_activity's. Add an entry to the list of the file whose
// tool uses it, with its constant there and in mcpCaveatIDsRuns in the group's
// test file.
var mcpCaveatsRuns = mcpJoinCaveats(mcpCaveatsRunsList, mcpCaveatsRunsAssess, mcpCaveatsRunsActivity)
