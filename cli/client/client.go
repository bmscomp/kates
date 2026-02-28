package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	MaxRetries int
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		MaxRetries: 3,
	}
}

type APIError struct {
	Status  int    `json:"status"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

func (e *APIError) String() string {
	return fmt.Sprintf("[%d] %s: %s", e.Status, e.Error, e.Message)
}

func (c *Client) doRequest(req *http.Request, retryable bool) ([]byte, error) {
	attempts := 1
	if retryable {
		attempts = c.MaxRetries
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			backoff := time.Duration(math.Pow(2, float64(i-1))) * 500 * time.Millisecond
			time.Sleep(backoff)
		}

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("connection failed: %w", err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			continue
		}

		if resp.StatusCode >= 500 && retryable && i < attempts-1 {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
			continue
		}

		if resp.StatusCode >= 400 {
			var apiErr APIError
			if json.Unmarshal(body, &apiErr) == nil && apiErr.Message != "" {
				return nil, fmt.Errorf("%s", apiErr.String())
			}
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}

		return body, nil
	}
	return nil, lastErr
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.doRequest(req, true)
}

func (c *Client) postJSON(ctx context.Context, path string, payload interface{}) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doRequest(req, false)
}

func (c *Client) put(ctx context.Context, path string, payload interface{}) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doRequest(req, false)
}

func (c *Client) delete(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	_, err = c.doRequest(req, false)
	return err
}

func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	data, err := c.get(ctx, "/api/health")
	if err != nil {
		return nil, err
	}
	var result HealthResponse
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) ClusterInfo(ctx context.Context) (*ClusterInfo, error) {
	data, err := c.get(ctx, "/api/cluster/info")
	if err != nil {
		return nil, err
	}
	var result ClusterInfo
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) Topics(ctx context.Context) ([]string, error) {
	data, err := c.get(ctx, "/api/cluster/topics")
	if err != nil {
		return nil, err
	}
	var result []string
	return result, json.Unmarshal(data, &result)
}

func (c *Client) TopicDetail(ctx context.Context, name string) (*TopicDetail, error) {
	data, err := c.get(ctx, "/api/cluster/topics/"+name)
	if err != nil {
		return nil, err
	}
	var result TopicDetail
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) ConsumerGroups(ctx context.Context) ([]ConsumerGroupSummary, error) {
	data, err := c.get(ctx, "/api/cluster/groups")
	if err != nil {
		return nil, err
	}
	var result []ConsumerGroupSummary
	return result, json.Unmarshal(data, &result)
}

func (c *Client) ConsumerGroupDetail(ctx context.Context, id string) (*ConsumerGroupDetail, error) {
	data, err := c.get(ctx, "/api/cluster/groups/"+id)
	if err != nil {
		return nil, err
	}
	var result ConsumerGroupDetail
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) BrokerConfigs(ctx context.Context, id int) ([]BrokerConfig, error) {
	data, err := c.get(ctx, fmt.Sprintf("/api/cluster/brokers/%d/configs", id))
	if err != nil {
		return nil, err
	}
	var result []BrokerConfig
	return result, json.Unmarshal(data, &result)
}

func (c *Client) ClusterCheck(ctx context.Context) (*ClusterHealthReport, error) {
	data, err := c.get(ctx, "/api/cluster/check")
	if err != nil {
		return nil, err
	}
	var result ClusterHealthReport
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) ListTests(ctx context.Context, testType, status string, page, size int) (*PagedTests, error) {
	path := fmt.Sprintf("/api/tests?page=%d&size=%d", page, size)
	if testType != "" {
		path += "&type=" + testType
	}
	if status != "" {
		path += "&status=" + status
	}
	data, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result PagedTests
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) GetTest(ctx context.Context, id string) (*TestRun, error) {
	data, err := c.get(ctx, "/api/tests/"+id)
	if err != nil {
		return nil, err
	}
	var result TestRun
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) CreateTest(ctx context.Context, request *CreateTestRequest) (*TestRun, error) {
	data, err := c.postJSON(ctx, "/api/tests", request)
	if err != nil {
		return nil, err
	}
	var result TestRun
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) DeleteTest(ctx context.Context, id string) error {
	return c.delete(ctx, "/api/tests/"+id)
}

func (c *Client) TestTypes(ctx context.Context) ([]string, error) {
	data, err := c.get(ctx, "/api/tests/types")
	if err != nil {
		return nil, err
	}
	var result []string
	return result, json.Unmarshal(data, &result)
}

func (c *Client) Backends(ctx context.Context) ([]string, error) {
	data, err := c.get(ctx, "/api/tests/backends")
	if err != nil {
		return nil, err
	}
	var result []string
	return result, json.Unmarshal(data, &result)
}

func (c *Client) Report(ctx context.Context, id string) (*Report, error) {
	data, err := c.get(ctx, "/api/tests/"+id+"/report")
	if err != nil {
		return nil, err
	}
	var result Report
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) ReportSummary(ctx context.Context, id string) (*ReportSummary, error) {
	data, err := c.get(ctx, "/api/tests/"+id+"/report/summary")
	if err != nil {
		return nil, err
	}
	var result ReportSummary
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) Compare(ctx context.Context, ids string) (json.RawMessage, error) {
	data, err := c.get(ctx, "/api/reports/compare?ids="+ids)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func (c *Client) ExportCSV(ctx context.Context, id string) (string, error) {
	data, err := c.get(ctx, "/api/tests/"+id+"/report/csv")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *Client) ExportHeatmap(ctx context.Context, id string, format string) (string, error) {
	path := "/api/tests/" + id + "/report/heatmap"
	if format != "" {
		path += "?format=" + format
	}
	data, err := c.get(ctx, path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *Client) ExportJUnit(ctx context.Context, id string) (string, error) {
	data, err := c.get(ctx, "/api/tests/"+id+"/report/junit")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *Client) ListSchedules(ctx context.Context) ([]Schedule, error) {
	data, err := c.get(ctx, "/api/schedules")
	if err != nil {
		return nil, err
	}
	var result []Schedule
	return result, json.Unmarshal(data, &result)
}

func (c *Client) GetSchedule(ctx context.Context, id string) (*Schedule, error) {
	data, err := c.get(ctx, "/api/schedules/"+id)
	if err != nil {
		return nil, err
	}
	var result Schedule
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) CreateSchedule(ctx context.Context, request *CreateScheduleRequest) (*Schedule, error) {
	data, err := c.postJSON(ctx, "/api/schedules", request)
	if err != nil {
		return nil, err
	}
	var result Schedule
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) UpdateSchedule(ctx context.Context, id string, request *CreateScheduleRequest) (*Schedule, error) {
	data, err := c.put(ctx, "/api/schedules/"+id, request)
	if err != nil {
		return nil, err
	}
	var result Schedule
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) DeleteSchedule(ctx context.Context, id string) error {
	return c.delete(ctx, "/api/schedules/"+id)
}

func (c *Client) Trends(ctx context.Context, testType, metric string, days, baselineWindow int, phase string) (*TrendResponse, error) {
	path := fmt.Sprintf("/api/trends?type=%s&metric=%s&days=%d&baselineWindow=%d",
		testType, metric, days, baselineWindow)
	if phase != "" {
		path += "&phase=" + phase
	}
	data, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result TrendResponse
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) TrendPhases(ctx context.Context, testType string, days int) ([]string, error) {
	path := fmt.Sprintf("/api/trends/phases?type=%s&days=%d", testType, days)
	data, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result []string
	return result, json.Unmarshal(data, &result)
}

func (c *Client) TrendBreakdown(ctx context.Context, testType, metric string, days, baselineWindow int) (*PhaseTrendResponse, error) {
	path := fmt.Sprintf("/api/trends/breakdown?type=%s&metric=%s&days=%d&baselineWindow=%d",
		testType, metric, days, baselineWindow)
	data, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result PhaseTrendResponse
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) ReportBrokers(ctx context.Context, runID string) ([]BrokerMetricsResponse, error) {
	path := fmt.Sprintf("/api/tests/%s/report/brokers", runID)
	data, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result []BrokerMetricsResponse
	return result, json.Unmarshal(data, &result)
}

func (c *Client) ReportSnapshot(ctx context.Context, runID string) (*ClusterSnapshotResponse, error) {
	path := fmt.Sprintf("/api/tests/%s/report/snapshot", runID)
	data, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result ClusterSnapshotResponse
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) BrokerTrend(ctx context.Context, testType, metric string, brokerId, days, baselineWindow int) (*BrokerTrendResponse, error) {
	path := fmt.Sprintf("/api/trends/broker?type=%s&metric=%s&brokerId=%d&days=%d&baselineWindow=%d",
		testType, metric, brokerId, days, baselineWindow)
	data, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result BrokerTrendResponse
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) Resilience(ctx context.Context, request interface{}) (*ResilienceResult, error) {
	data, err := c.postJSON(ctx, "/api/resilience", request)
	if err != nil {
		return nil, err
	}
	var result ResilienceResult
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) RunDisruption(ctx context.Context, plan interface{}) (*DisruptionRunResponse, error) {
	data, err := c.postJSON(ctx, "/api/disruptions", plan)
	if err != nil {
		return nil, err
	}
	var result DisruptionRunResponse
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) RunDryRun(ctx context.Context, plan interface{}) (*DryRunResult, error) {
	data, err := c.postJSON(ctx, "/api/disruptions?dryRun=true", plan)
	if err != nil {
		return nil, err
	}
	var result DryRunResult
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) DisruptionStatus(ctx context.Context, id string) (*DisruptionReport, error) {
	path := fmt.Sprintf("/api/disruptions/%s", id)
	data, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result DisruptionReport
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) DisruptionTimelineData(ctx context.Context, id string) ([]DisruptionTimeline, error) {
	path := fmt.Sprintf("/api/disruptions/%s/timeline", id)
	data, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result []DisruptionTimeline
	return result, json.Unmarshal(data, &result)
}

func (c *Client) DisruptionTypes(ctx context.Context) ([]DisruptionTypeInfo, error) {
	data, err := c.get(ctx, "/api/disruptions/types")
	if err != nil {
		return nil, err
	}
	var result []DisruptionTypeInfo
	return result, json.Unmarshal(data, &result)
}

func (c *Client) DisruptionList(ctx context.Context, limit int) ([]DisruptionListEntry, error) {
	path := fmt.Sprintf("/api/disruptions?limit=%d", limit)
	data, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result []DisruptionListEntry
	return result, json.Unmarshal(data, &result)
}

func (c *Client) DisruptionKafkaMetrics(ctx context.Context, id string) ([]KafkaMetricsEntry, error) {
	path := fmt.Sprintf("/api/disruptions/%s/kafka-metrics", id)
	data, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result []KafkaMetricsEntry
	return result, json.Unmarshal(data, &result)
}

func (c *Client) PlaybookList(ctx context.Context) ([]PlaybookEntry, error) {
	data, err := c.get(ctx, "/api/disruptions/playbooks")
	if err != nil {
		return nil, err
	}
	var result []PlaybookEntry
	return result, json.Unmarshal(data, &result)
}

func (c *Client) PlaybookRun(ctx context.Context, name string) (*DisruptionRunResponse, error) {
	path := fmt.Sprintf("/api/disruptions/playbooks/%s", name)
	data, err := c.postJSON(ctx, path, nil)
	if err != nil {
		return nil, err
	}
	var result DisruptionRunResponse
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) DisruptionScheduleList(ctx context.Context) ([]DisruptionScheduleEntry, error) {
	data, err := c.get(ctx, "/api/disruptions/schedules")
	if err != nil {
		return nil, err
	}
	var result []DisruptionScheduleEntry
	return result, json.Unmarshal(data, &result)
}

func (c *Client) DisruptionScheduleCreate(ctx context.Context, body map[string]interface{}) (json.RawMessage, error) {
	return c.postJSON(ctx, "/api/disruptions/schedules", body)
}

func (c *Client) DisruptionScheduleDelete(ctx context.Context, id string) error {
	path := fmt.Sprintf("/api/disruptions/schedules/%s", id)
	return c.delete(ctx, path)
}

func (c *Client) ListWebhooks(ctx context.Context) ([]WebhookRegistration, error) {
	data, err := c.get(ctx, "/api/webhooks")
	if err != nil {
		return nil, err
	}
	var result []WebhookRegistration
	return result, json.Unmarshal(data, &result)
}

func (c *Client) RegisterWebhook(ctx context.Context, name, url string) error {
	payload := map[string]string{"name": name, "url": url, "events": "test.completed"}
	_, err := c.postJSON(ctx, "/api/webhooks", payload)
	return err
}

func (c *Client) DeleteWebhook(ctx context.Context, name string) error {
	return c.delete(ctx, "/api/webhooks/"+name)
}

func (c *Client) KafkaBrokers(ctx context.Context) (*ClusterInfo, error) {
	data, err := c.get(ctx, "/api/kafka/brokers")
	if err != nil {
		return nil, err
	}
	var result ClusterInfo
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) KafkaTopics(ctx context.Context) ([]KafkaTopic, error) {
	data, err := c.get(ctx, "/api/kafka/topics")
	if err != nil {
		return nil, err
	}
	var result []KafkaTopic
	return result, json.Unmarshal(data, &result)
}

func (c *Client) KafkaTopicDetail(ctx context.Context, name string) (map[string]interface{}, error) {
	data, err := c.get(ctx, "/api/kafka/topics/"+name)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

func (c *Client) KafkaGroups(ctx context.Context) ([]map[string]interface{}, error) {
	data, err := c.get(ctx, "/api/kafka/groups")
	if err != nil {
		return nil, err
	}
	var result []map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

func (c *Client) KafkaGroupDetail(ctx context.Context, id string) (map[string]interface{}, error) {
	data, err := c.get(ctx, "/api/kafka/groups/"+id)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

func (c *Client) KafkaConsume(ctx context.Context, topic string, offset string, limit int) ([]KafkaRecord, error) {
	path := fmt.Sprintf("/api/kafka/consume/%s?offset=%s&limit=%d", topic, offset, limit)
	data, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result []KafkaRecord
	return result, json.Unmarshal(data, &result)
}

func (c *Client) KafkaProduce(ctx context.Context, topic, key, value string) (*ProduceMeta, error) {
	payload := map[string]string{"key": key, "value": value}
	data, err := c.postJSON(ctx, "/api/kafka/produce/"+topic, payload)
	if err != nil {
		return nil, err
	}
	var result ProduceMeta
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) patch(ctx context.Context, path string, payload interface{}) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doRequest(req, false)
}

func (c *Client) KafkaCreateTopic(ctx context.Context, request *CreateTopicRequest) (map[string]interface{}, error) {
	data, err := c.postJSON(ctx, "/api/kafka/topics", request)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

func (c *Client) KafkaAlterTopic(ctx context.Context, name string, request *AlterTopicRequest) (map[string]interface{}, error) {
	data, err := c.patch(ctx, "/api/kafka/topics/"+name, request)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

func (c *Client) KafkaDeleteTopic(ctx context.Context, name string) error {
	return c.delete(ctx, "/api/kafka/topics/"+name)
}

func (c *Client) BaselineSet(ctx context.Context, testType, runID string) (*BaselineEntry, error) {
	req := SetBaselineRequest{RunID: runID}
	data, err := c.put(ctx, "/api/tests/baselines/"+testType, req)
	if err != nil {
		return nil, err
	}
	var result BaselineEntry
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) BaselineUnset(ctx context.Context, testType string) error {
	return c.delete(ctx, "/api/tests/baselines/"+testType)
}

func (c *Client) BaselineGet(ctx context.Context, testType string) (*BaselineEntry, error) {
	data, err := c.get(ctx, "/api/tests/baselines/"+testType)
	if err != nil {
		return nil, err
	}
	var result BaselineEntry
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) BaselineList(ctx context.Context) ([]BaselineEntry, error) {
	data, err := c.get(ctx, "/api/tests/baselines")
	if err != nil {
		return nil, err
	}
	var result []BaselineEntry
	return result, json.Unmarshal(data, &result)
}

func (c *Client) ReportRegression(ctx context.Context, runID string) (*RegressionReport, error) {
	data, err := c.get(ctx, "/api/tests/"+runID+"/report/regression")
	if err != nil {
		return nil, err
	}
	var result RegressionReport
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) ReportTuning(ctx context.Context, runID string) (*TuningReport, error) {
	data, err := c.get(ctx, "/api/tests/"+runID+"/report/tuning")
	if err != nil {
		return nil, err
	}
	var result TuningReport
	return &result, json.Unmarshal(data, &result)
}

func (c *Client) TuningTypes(ctx context.Context) ([]TuningTypeInfo, error) {
	data, err := c.get(ctx, "/api/tuning/types")
	if err != nil {
		return nil, err
	}
	var result []TuningTypeInfo
	return result, json.Unmarshal(data, &result)
}

func (c *Client) Audit(ctx context.Context, limit int, eventType, since string) ([]AuditEntry, error) {
	path := fmt.Sprintf("/api/audit?limit=%d", limit)
	if eventType != "" {
		path += "&type=" + eventType
	}
	if since != "" {
		path += "&since=" + since
	}
	data, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result []AuditEntry
	return result, json.Unmarshal(data, &result)
}
