package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New(srv.URL)
	c.MaxRetries = 1
	return c, srv
}

func jsonHandler(t *testing.T, wantMethod, wantPath string, response interface{}) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != wantMethod {
			t.Errorf("method = %s, want %s", r.Method, wantMethod)
		}
		if !strings.HasPrefix(r.URL.Path, wantPath) {
			t.Errorf("path = %s, want prefix %s", r.URL.Path, wantPath)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

func TestNew(t *testing.T) {
	c := New("http://localhost:8080/")
	if c.BaseURL != "http://localhost:8080" {
		t.Errorf("BaseURL = %q, want trailing slash trimmed", c.BaseURL)
	}
	if c.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", c.MaxRetries)
	}
	if c.HTTPClient == nil {
		t.Error("HTTPClient should not be nil")
	}
}

func TestAPIError_String(t *testing.T) {
	e := &APIError{Status: 404, Error: "Not Found", Message: "resource missing"}
	got := e.String()
	if got != "[404] Not Found: resource missing" {
		t.Errorf("APIError.String() = %q", got)
	}
}

func TestHealth(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/health", HealthResponse{
		Status: "UP",
		Engine: &EngineInfo{ActiveBackend: "trogdor"},
		Kafka:  &KafkaInfo{Status: "UP", BootstrapServers: "broker:9092"},
	}))
	resp, err := c.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != "UP" {
		t.Errorf("Status = %q, want UP", resp.Status)
	}
	if resp.Engine.ActiveBackend != "trogdor" {
		t.Errorf("Backend = %q", resp.Engine.ActiveBackend)
	}
}

func TestClusterInfo(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/cluster/info", ClusterInfo{
		ClusterID:   "test-cluster",
		BrokerCount: 3,
		Brokers:     []BrokerNode{{ID: 0, Host: "broker-0", Port: 9092, Rack: "az-1"}},
	}))
	resp, err := c.ClusterInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.ClusterID != "test-cluster" {
		t.Errorf("ClusterID = %q", resp.ClusterID)
	}
	if len(resp.Brokers) != 1 {
		t.Errorf("Brokers count = %d", len(resp.Brokers))
	}
}

func TestTopics(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/cluster/topics", []string{"topic-1", "topic-2"}))
	topics, err := c.Topics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 2 {
		t.Errorf("Topics count = %d, want 2", len(topics))
	}
	if topics[0] != "topic-1" {
		t.Errorf("topics[0] = %q", topics[0])
	}
}

func TestTopicDetail(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/cluster/topics/", TopicDetail{
		Name: "my-topic", Partitions: 6, ReplicationFactor: 3, Internal: false,
		Configs: map[string]string{"retention.ms": "86400000"},
		PartitionInfo: []PartitionInfo{
			{Partition: 0, Leader: 1, Replicas: []int{1, 2, 3}, ISR: []int{1, 2, 3}},
		},
	}))
	resp, err := c.TopicDetail(context.Background(), "my-topic")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Name != "my-topic" {
		t.Errorf("Name = %q", resp.Name)
	}
	if resp.Partitions != 6 {
		t.Errorf("Partitions = %d", resp.Partitions)
	}
	if len(resp.Configs) != 1 {
		t.Errorf("Configs count = %d", len(resp.Configs))
	}
}

func TestConsumerGroups(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/cluster/groups", []ConsumerGroupSummary{
		{GroupID: "group-1", State: "STABLE", Members: 3},
		{GroupID: "group-2", State: "EMPTY", Members: 0},
	}))
	groups, err := c.ConsumerGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Errorf("Groups count = %d", len(groups))
	}
	if groups[0].GroupID != "group-1" {
		t.Errorf("groups[0].GroupID = %q", groups[0].GroupID)
	}
}

func TestConsumerGroupDetail(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/cluster/groups/", ConsumerGroupDetail{
		GroupID: "group-1", State: "STABLE", Members: 3, TotalLag: 150,
		Offsets: []GroupPartitionInfo{
			{Topic: "t1", Partition: 0, CurrentOffset: 100, EndOffset: 150, Lag: 50},
		},
	}))
	resp, err := c.ConsumerGroupDetail(context.Background(), "group-1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalLag != 150 {
		t.Errorf("TotalLag = %d", resp.TotalLag)
	}
	if len(resp.Offsets) != 1 {
		t.Errorf("Offsets count = %d", len(resp.Offsets))
	}
}

func TestBrokerConfigs(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/api/cluster/brokers/0/configs") {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode([]BrokerConfig{
			{Name: "min.insync.replicas", Value: "2", Source: "STATIC_BROKER_CONFIG", ReadOnly: true},
			{Name: "log.dirs", Value: "/var/lib/kafka", Source: "STATIC_BROKER_CONFIG", ReadOnly: true},
		})
	})
	configs, err := c.BrokerConfigs(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 2 {
		t.Errorf("Configs count = %d", len(configs))
	}
	if configs[0].Name != "min.insync.replicas" {
		t.Errorf("configs[0].Name = %q", configs[0].Name)
	}
	if !configs[0].ReadOnly {
		t.Error("configs[0] should be read-only")
	}
}

func TestClusterCheck(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/cluster/check", ClusterHealthReport{
		ClusterID: "test", Brokers: 3, ControllerID: 0, Topics: 10, Partitions: 30,
		ConsumerGroups: 5, Status: "HEALTHY",
		PartitionHealth: PartitionHealthReport{UnderReplicated: 0, Offline: 0},
	}))
	resp, err := c.ClusterCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != "HEALTHY" {
		t.Errorf("Status = %q", resp.Status)
	}
	if resp.Brokers != 3 {
		t.Errorf("Brokers = %d", resp.Brokers)
	}
}

func TestListTests(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Query().Get("page") != "0" {
			t.Errorf("page = %s", r.URL.Query().Get("page"))
		}
		if r.URL.Query().Get("size") != "10" {
			t.Errorf("size = %s", r.URL.Query().Get("size"))
		}
		if r.URL.Query().Get("type") != "LOAD" {
			t.Errorf("type = %s", r.URL.Query().Get("type"))
		}
		json.NewEncoder(w).Encode(PagedTests{
			Content:    []TestRun{{ID: "run-1", TestType: "LOAD", Status: "DONE"}},
			Page:       0,
			Size:       10,
			TotalItems: 1,
			TotalPages: 1,
		})
	})
	resp, err := c.ListTests(context.Background(), "LOAD", "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalItems != 1 {
		t.Errorf("TotalItems = %d", resp.TotalItems)
	}
	if resp.Content[0].ID != "run-1" {
		t.Errorf("Content[0].ID = %q", resp.Content[0].ID)
	}
}

func TestListTests_WithStatusFilter(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("status") != "RUNNING" {
			t.Errorf("status = %s", r.URL.Query().Get("status"))
		}
		json.NewEncoder(w).Encode(PagedTests{})
	})
	_, err := c.ListTests(context.Background(), "", "RUNNING", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
}

func TestGetTest(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/tests/", TestRun{
		ID: "abc-123", TestType: "STRESS", Status: "RUNNING",
		Results: []PhaseResult{{PhaseName: "warmup", Status: "DONE", RecordsSent: 1000}},
	}))
	resp, err := c.GetTest(context.Background(), "abc-123")
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "abc-123" {
		t.Errorf("ID = %q", resp.ID)
	}
	if len(resp.Results) != 1 {
		t.Errorf("Results count = %d", len(resp.Results))
	}
}

func TestCreateTest(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		var req CreateTestRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.TestType != "LOAD" {
			t.Errorf("TestType = %q", req.TestType)
		}
		json.NewEncoder(w).Encode(TestRun{ID: "new-run", TestType: "LOAD", Status: "PENDING"})
	})
	resp, err := c.CreateTest(context.Background(), &CreateTestRequest{
		TestType: "LOAD",
		Spec:     &TestSpec{Records: 50000, ParallelProducers: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "new-run" {
		t.Errorf("ID = %q", resp.ID)
	}
}

func TestDeleteTest(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/api/tests/run-123") {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	err := c.DeleteTest(context.Background(), "run-123")
	if err != nil {
		t.Fatal(err)
	}
}

func TestTestTypes(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/tests/types",
		[]string{"LOAD", "STRESS", "SPIKE", "ENDURANCE", "VOLUME", "CAPACITY", "ROUND_TRIP"}))
	types, err := c.TestTypes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 7 {
		t.Errorf("Types count = %d, want 7", len(types))
	}
}

func TestBackends(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/tests/backends", []string{"trogdor", "internal"}))
	backends, err := c.Backends(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(backends) != 2 {
		t.Errorf("Backends count = %d", len(backends))
	}
}

func TestReport(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/tests/", Report{
		Summary: &ReportSummary{
			TotalRecords: 100000, AvgLatencyMs: 5.2, P99LatencyMs: 12.3,
			AvgThroughputRecPerSec: 50000, ErrorRate: 0.001,
		},
		OverallSlaVerdict: &SlaVerdict{
			Passed: true,
		},
	}))
	resp, err := c.Report(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Summary.TotalRecords != 100000 {
		t.Errorf("TotalRecords = %f", resp.Summary.TotalRecords)
	}
	if !resp.OverallSlaVerdict.Passed {
		t.Error("SLA should have passed")
	}
}

func TestReportSummary(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/tests/", ReportSummary{
		TotalRecords: 50000, AvgLatencyMs: 3.1, P50LatencyMs: 2.5,
		P95LatencyMs: 8.0, P99LatencyMs: 15.0, MaxLatencyMs: 42.0,
	}))
	resp, err := c.ReportSummary(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.P99LatencyMs != 15.0 {
		t.Errorf("P99 = %f", resp.P99LatencyMs)
	}
}

func TestCompare(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "ids=a,b") {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Write([]byte(`{"comparison": "data"}`))
	})
	raw, err := c.Compare(context.Background(), "a,b")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Error("Compare should return non-empty data")
	}
}

func TestExportCSV(t *testing.T) {
	csvData := "metric,value\nlatency,5.2\n"
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/report/csv") {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(csvData))
	})
	resp, err := c.ExportCSV(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if resp != csvData {
		t.Errorf("CSV = %q", resp)
	}
}

func TestExportJUnit(t *testing.T) {
	junitXML := `<?xml version="1.0"?><testsuite tests="1"></testsuite>`
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/report/junit") {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(junitXML))
	})
	resp, err := c.ExportJUnit(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp, "testsuite") {
		t.Errorf("JUnit = %q", resp)
	}
}

func TestListSchedules(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/schedules", []Schedule{
		{ID: "s1", Name: "nightly-load", CronExpression: "0 0 * * *", Enabled: true},
	}))
	schedules, err := c.ListSchedules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 1 {
		t.Errorf("Schedules count = %d", len(schedules))
	}
	if schedules[0].Name != "nightly-load" {
		t.Errorf("Name = %q", schedules[0].Name)
	}
}

func TestGetSchedule(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/schedules/", Schedule{
		ID: "s1", Name: "nightly", CronExpression: "0 2 * * *", Enabled: true,
	}))
	resp, err := c.GetSchedule(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.CronExpression != "0 2 * * *" {
		t.Errorf("Cron = %q", resp.CronExpression)
	}
}

func TestCreateSchedule(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s", r.Method)
		}
		var req CreateScheduleRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Name != "weekly" {
			t.Errorf("Name = %q", req.Name)
		}
		json.NewEncoder(w).Encode(Schedule{ID: "s2", Name: "weekly", Enabled: true})
	})
	resp, err := c.CreateSchedule(context.Background(), &CreateScheduleRequest{
		Name: "weekly", CronExpression: "0 0 * * 0", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "s2" {
		t.Errorf("ID = %q", resp.ID)
	}
}

func TestUpdateSchedule(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		json.NewEncoder(w).Encode(Schedule{ID: "s1", Name: "updated", Enabled: false})
	})
	resp, err := c.UpdateSchedule(context.Background(), "s1", &CreateScheduleRequest{
		Name: "updated", CronExpression: "0 3 * * *", Enabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Name != "updated" {
		t.Errorf("Name = %q", resp.Name)
	}
	if resp.Enabled {
		t.Error("should be disabled")
	}
}

func TestDeleteSchedule(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	err := c.DeleteSchedule(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestTrends(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("type") != "LOAD" {
			t.Errorf("type = %s", q.Get("type"))
		}
		if q.Get("metric") != "p99LatencyMs" {
			t.Errorf("metric = %s", q.Get("metric"))
		}
		if q.Get("days") != "30" {
			t.Errorf("days = %s", q.Get("days"))
		}
		if q.Get("baselineWindow") != "5" {
			t.Errorf("baselineWindow = %s", q.Get("baselineWindow"))
		}
		if q.Get("phase") != "" {
			t.Errorf("phase should be empty, got %s", q.Get("phase"))
		}
		json.NewEncoder(w).Encode(TrendResponse{
			Baseline:    10.5,
			DataPoints:  []DataPoint{{RunID: "r1", Value: 11.0}},
			Regressions: []Regression{{RunID: "r2", Value: 25.0, Baseline: 10.5, DeviationPercent: 138}},
		})
	})
	resp, err := c.Trends(context.Background(), "LOAD", "p99LatencyMs", 30, 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Baseline != 10.5 {
		t.Errorf("Baseline = %f", resp.Baseline)
	}
	if len(resp.DataPoints) != 1 {
		t.Errorf("DataPoints = %d", len(resp.DataPoints))
	}
	if len(resp.Regressions) != 1 {
		t.Errorf("Regressions = %d", len(resp.Regressions))
	}
}

func TestTrends_WithPhase(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("phase") != "spike" {
			t.Errorf("phase = %q, want spike", q.Get("phase"))
		}
		json.NewEncoder(w).Encode(TrendResponse{
			Phase:      "spike",
			Baseline:   8.0,
			DataPoints: []DataPoint{{RunID: "r1", Value: 9.5}},
		})
	})
	resp, err := c.Trends(context.Background(), "SPIKE", "p99LatencyMs", 14, 3, "spike")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Phase != "spike" {
		t.Errorf("Phase = %q, want spike", resp.Phase)
	}
	if resp.Baseline != 8.0 {
		t.Errorf("Baseline = %f", resp.Baseline)
	}
}

func TestTrendPhases(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/trends/phases") {
			t.Errorf("path = %s, want /api/trends/phases", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("type") != "ENDURANCE" {
			t.Errorf("type = %s", q.Get("type"))
		}
		json.NewEncoder(w).Encode([]string{"warmup", "sustained", "cooldown"})
	})
	phases, err := c.TrendPhases(context.Background(), "ENDURANCE", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(phases) != 3 {
		t.Errorf("Phases count = %d, want 3", len(phases))
	}
	if phases[0] != "warmup" {
		t.Errorf("phases[0] = %q", phases[0])
	}
}

func TestTrendBreakdown(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/trends/breakdown") {
			t.Errorf("path = %s, want /api/trends/breakdown", r.URL.Path)
		}
		json.NewEncoder(w).Encode(PhaseTrendResponse{
			TestType: "SPIKE",
			Metric:   "p99LatencyMs",
			Phases: []PhaseTrend{
				{Phase: "ramp", Baseline: 5.0, DataPoints: []DataPoint{{RunID: "r1", Value: 4.8}}},
				{Phase: "spike", Baseline: 15.0, DataPoints: []DataPoint{{RunID: "r1", Value: 18.2}}},
			},
		})
	})
	resp, err := c.TrendBreakdown(context.Background(), "SPIKE", "p99LatencyMs", 30, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Phases) != 2 {
		t.Fatalf("Phases count = %d, want 2", len(resp.Phases))
	}
	if resp.Phases[0].Phase != "ramp" {
		t.Errorf("phases[0].Phase = %q", resp.Phases[0].Phase)
	}
	if resp.Phases[1].Baseline != 15.0 {
		t.Errorf("phases[1].Baseline = %f", resp.Phases[1].Baseline)
	}
}

func TestReportBrokers(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tests/abc123/report/brokers" {
			t.Errorf("path = %s, want /api/tests/abc123/report/brokers", r.URL.Path)
		}
		json.NewEncoder(w).Encode([]BrokerMetricsResponse{
			{
				BrokerID:         0,
				Host:             "broker-0",
				Rack:             "zone-a",
				LeaderPartitions: 4,
				TotalPartitions:  12,
				LeaderSharePct:   33.33,
				Metrics:          BrokerMetricsSummary{AvgThroughputRecPerSec: 12000, P99LatencyMs: 3.2},
				Skewed:           false,
			},
			{
				BrokerID:         1,
				Host:             "broker-1",
				Rack:             "zone-b",
				LeaderPartitions: 4,
				TotalPartitions:  12,
				LeaderSharePct:   33.33,
				Metrics:          BrokerMetricsSummary{AvgThroughputRecPerSec: 11800, P99LatencyMs: 3.8},
				Skewed:           false,
			},
			{
				BrokerID:         2,
				Host:             "broker-2",
				Rack:             "zone-c",
				LeaderPartitions: 4,
				TotalPartitions:  12,
				LeaderSharePct:   33.34,
				Metrics:          BrokerMetricsSummary{AvgThroughputRecPerSec: 7200, P99LatencyMs: 7.1},
				Skewed:           true,
			},
		})
	})
	brokers, err := c.ReportBrokers(context.Background(), "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(brokers) != 3 {
		t.Fatalf("Brokers count = %d, want 3", len(brokers))
	}
	if brokers[0].Host != "broker-0" {
		t.Errorf("brokers[0].Host = %q", brokers[0].Host)
	}
	if brokers[2].Skewed != true {
		t.Errorf("brokers[2].Skewed = %v, want true", brokers[2].Skewed)
	}
	if brokers[0].Metrics.AvgThroughputRecPerSec != 12000 {
		t.Errorf("brokers[0].Metrics.AvgThroughputRecPerSec = %f", brokers[0].Metrics.AvgThroughputRecPerSec)
	}
}

func TestReportSnapshot(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tests/abc123/report/snapshot" {
			t.Errorf("path = %s, want /api/tests/abc123/report/snapshot", r.URL.Path)
		}
		json.NewEncoder(w).Encode(ClusterSnapshotResponse{
			ClusterID:    "test-cluster-123",
			BrokerCount:  3,
			ControllerID: 0,
			Brokers: []SnapshotBrokerInfo{
				{ID: 0, Host: "broker-0", Port: 9092, Rack: "zone-a"},
				{ID: 1, Host: "broker-1", Port: 9092, Rack: "zone-b"},
				{ID: 2, Host: "broker-2", Port: 9092, Rack: "zone-c"},
			},
			Leaders: []PartitionAssignment{
				{Topic: "test-topic", Partition: 0, LeaderID: 0, Replicas: []int{0, 1, 2}, ISR: []int{0, 1, 2}},
				{Topic: "test-topic", Partition: 1, LeaderID: 1, Replicas: []int{1, 2, 0}, ISR: []int{1, 2, 0}},
			},
		})
	})
	snap, err := c.ReportSnapshot(context.Background(), "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if snap.ClusterID != "test-cluster-123" {
		t.Errorf("ClusterID = %q", snap.ClusterID)
	}
	if snap.BrokerCount != 3 {
		t.Errorf("BrokerCount = %d", snap.BrokerCount)
	}
	if len(snap.Leaders) != 2 {
		t.Errorf("Leaders count = %d, want 2", len(snap.Leaders))
	}
	if snap.Leaders[0].LeaderID != 0 {
		t.Errorf("Leaders[0].LeaderID = %d", snap.Leaders[0].LeaderID)
	}
}

func TestResilience(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s", r.Method)
		}
		json.NewEncoder(w).Encode(ResilienceResult{
			Status: "COMPLETED",
			ChaosOutcome: &ChaosOutcome{
				ExperimentName: "pod-kill", Verdict: "Pass", ChaosDuration: "60s",
			},
			ImpactDeltas: map[string]float64{"p99LatencyMs": 3.5},
		})
	})
	resp, err := c.Resilience(context.Background(), map[string]string{"experiment": "pod-kill"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != "COMPLETED" {
		t.Errorf("Status = %q", resp.Status)
	}
	if resp.ChaosOutcome.Verdict != "Pass" {
		t.Errorf("Verdict = %q", resp.ChaosOutcome.Verdict)
	}
}

func TestHTTPError_4xx(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(APIError{Status: 404, Error: "Not Found", Message: "test not found"})
	})
	_, err := c.Health(context.Background())
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("error = %q, should contain 'Not Found'", err.Error())
	}
}

func TestHTTPError_4xx_PlainText(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request body"))
	})
	_, err := c.Health(context.Background())
	if err == nil {
		t.Fatal("expected error for 400")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %q, should contain status code", err.Error())
	}
}

func TestHTTPError_5xx_NoRetry(t *testing.T) {
	calls := 0
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	})
	c.MaxRetries = 1
	_, err := c.Health(context.Background())
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry with MaxRetries=1)", calls)
	}
}

func TestHTTPError_ConnectionRefused(t *testing.T) {
	c := New("http://127.0.0.1:1")
	c.MaxRetries = 1
	c.HTTPClient.Timeout = 100 * 1000000 // 100ms
	_, err := c.Health(context.Background())
	if err == nil {
		t.Fatal("expected connection error")
	}
	if !strings.Contains(err.Error(), "connection") {
		t.Errorf("error = %q, should mention connection", err.Error())
	}
}

func TestTopics_Empty(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/cluster/topics", []string{}))
	topics, err := c.Topics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 0 {
		t.Errorf("Topics should be empty, got %d", len(topics))
	}
}

func TestConsumerGroups_Empty(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/cluster/groups", []ConsumerGroupSummary{}))
	groups, err := c.ConsumerGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Errorf("Groups should be empty, got %d", len(groups))
	}
}

func TestListSchedules_Empty(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/schedules", []Schedule{}))
	schedules, err := c.ListSchedules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 0 {
		t.Errorf("Schedules should be empty, got %d", len(schedules))
	}
}

func TestReport_WithViolations(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, "GET", "/api/tests/", Report{
		Summary: &ReportSummary{P99LatencyMs: 100},
		OverallSlaVerdict: &SlaVerdict{
			Passed: false,
			Violations: []SlaViolation{
				{Metric: "p99LatencyMs", Threshold: 50, Actual: 100},
			},
		},
	}))
	resp, err := c.Report(context.Background(), "run-fail")
	if err != nil {
		t.Fatal(err)
	}
	if resp.OverallSlaVerdict.Passed {
		t.Error("SLA should have failed")
	}
	if len(resp.OverallSlaVerdict.Violations) != 1 {
		t.Errorf("Violations = %d", len(resp.OverallSlaVerdict.Violations))
	}
}
