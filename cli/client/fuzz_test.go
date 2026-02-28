package client

import (
	"encoding/json"
	"testing"
)

func FuzzTestRunUnmarshal(f *testing.F) {
	f.Add([]byte(`{"id":"abc","status":"RUNNING","results":[]}`))
	f.Add([]byte(`{"id":"","status":"","testType":"LOAD"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`{"results":[{"p99LatencyMs":1.5,"throughputRecordsPerSec":999}]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var run TestRun
		_ = json.Unmarshal(data, &run)
	})
}

func FuzzReportUnmarshal(f *testing.F) {
	f.Add([]byte(`{"summary":{"totalRecords":100}}`))
	f.Add([]byte(`{"overallSlaVerdict":{"passed":true}}`))
	f.Add([]byte(`{"phases":[{"name":"main","status":"DONE"}]}`))
	f.Add([]byte(`{}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var report Report
		_ = json.Unmarshal(data, &report)
	})
}

func FuzzAuditEntryUnmarshal(f *testing.F) {
	f.Add([]byte(`{"id":1,"action":"CREATE","eventType":"test"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`[{"action":"DELETE"}]`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var entry AuditEntry
		_ = json.Unmarshal(data, &entry)

		var entries []AuditEntry
		_ = json.Unmarshal(data, &entries)
	})
}

func FuzzPagedTestsUnmarshal(f *testing.F) {
	f.Add([]byte(`{"content":[{"id":"a","status":"DONE"}],"totalElements":1}`))
	f.Add([]byte(`{"content":[],"totalElements":0}`))
	f.Add([]byte(`{}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var pt PagedTests
		_ = json.Unmarshal(data, &pt)
	})
}

func FuzzReportSummaryUnmarshal(f *testing.F) {
	f.Add([]byte(`{"totalRecords":50000,"avgThroughputRecPerSec":10000,"p99LatencyMs":25.5}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"errorRate":0.999999,"maxLatencyMs":-1}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var rs ReportSummary
		_ = json.Unmarshal(data, &rs)
	})
}
