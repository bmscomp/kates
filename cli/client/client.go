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

func (c *Client) doRequest(ctx context.Context, req *http.Request, retryable bool) ([]byte, error) {
	attempts := 1
	if retryable {
		attempts = c.MaxRetries
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			backoff := time.Duration(math.Pow(2, float64(i-1))) * 500 * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
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
			if strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
				var apiErr APIError
				if json.Unmarshal(body, &apiErr) == nil && apiErr.Message != "" {
					return nil, fmt.Errorf("%s", apiErr.String())
				}
			}
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}

		return body, nil
	}
	return nil, lastErr
}

func (c *Client) getBytes(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.doRequest(ctx, req, true)
}

func get[T any](c *Client, ctx context.Context, path string) (T, error) {
	var result T
	data, err := c.getBytes(ctx, path)
	if err != nil {
		return result, err
	}
	return result, json.Unmarshal(data, &result)
}

func postJSON[T any](c *Client, ctx context.Context, path string, payload interface{}) (T, error) {
	var result T
	data, err := json.Marshal(payload)
	if err != nil {
		return result, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	respData, err := c.doRequest(ctx, req, false)
	if err != nil {
		return result, err
	}
	// For endpoints that return empty responses
	if len(respData) == 0 {
		return result, nil
	}
	return result, json.Unmarshal(respData, &result)
}

func put[T any](c *Client, ctx context.Context, path string, payload interface{}) (T, error) {
	var result T
	data, err := json.Marshal(payload)
	if err != nil {
		return result, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	respData, err := c.doRequest(ctx, req, false)
	if err != nil {
		return result, err
	}
	if len(respData) == 0 {
		return result, nil
	}
	return result, json.Unmarshal(respData, &result)
}

func (c *Client) delete(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	_, err = c.doRequest(ctx, req, false)
	return err
}

func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	return get[*HealthResponse](c, ctx, "/api/health")
}

func (c *Client) ClusterInfo(ctx context.Context) (*ClusterInfo, error) {
	return get[*ClusterInfo](c, ctx, "/api/cluster/info")
}

func (c *Client) ClusterTopology(ctx context.Context) (*ClusterTopology, error) {
	return get[*ClusterTopology](c, ctx, "/api/cluster/topology")
}

func (c *Client) Topics(ctx context.Context) ([]string, error) {
	var paged struct {
		Items []string `json:"items"`
	}
	data, err := c.getBytes(ctx, "/api/cluster/topics")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &paged); err != nil {
		return nil, err
	}
	return paged.Items, nil
}

func (c *Client) TopicDetail(ctx context.Context, name string) (*TopicDetail, error) {
	return get[*TopicDetail](c, ctx, "/api/cluster/topics/"+name)
}

func (c *Client) ConsumerGroups(ctx context.Context) ([]ConsumerGroupSummary, error) {
	var paged struct {
		Items []ConsumerGroupSummary `json:"items"`
	}
	data, err := c.getBytes(ctx, "/api/cluster/groups")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &paged); err != nil {
		return nil, err
	}
	return paged.Items, nil
}

func (c *Client) ConsumerGroupDetail(ctx context.Context, id string) (*ConsumerGroupDetail, error) {
	return get[*ConsumerGroupDetail](c, ctx, "/api/cluster/groups/"+id)
}

func (c *Client) BrokerConfigs(ctx context.Context, id int) ([]BrokerConfig, error) {
	return get[[]BrokerConfig](c, ctx, fmt.Sprintf("/api/cluster/brokers/%d/configs", id))
}

func (c *Client) ClusterCheck(ctx context.Context) (*ClusterHealthReport, error) {
	return get[*ClusterHealthReport](c, ctx, "/api/cluster/check")
}

func (c *Client) ListTests(ctx context.Context, testType, status string, page, size int) (*PagedTests, error) {
	path := fmt.Sprintf("/api/tests?page=%d&size=%d", page, size)
	if testType != "" {
		path += "&type=" + testType
	}
	if status != "" {
		path += "&status=" + status
	}
	return get[*PagedTests](c, ctx, path)
}

func (c *Client) GetTest(ctx context.Context, id string) (*TestRun, error) {
	return get[*TestRun](c, ctx, "/api/tests/"+id)
}

func (c *Client) CreateTest(ctx context.Context, request *CreateTestRequest) (*TestRun, error) {
	return postJSON[*TestRun](c, ctx, "/api/tests", request)
}

func (c *Client) DeleteTest(ctx context.Context, id string) error {
	return c.delete(ctx, "/api/tests/"+id)
}

func (c *Client) TestTypes(ctx context.Context) ([]string, error) {
	return get[[]string](c, ctx, "/api/tests/types")
}

func (c *Client) Backends(ctx context.Context) ([]string, error) {
	return get[[]string](c, ctx, "/api/tests/backends")
}

func (c *Client) Report(ctx context.Context, id string) (*Report, error) {
	return get[*Report](c, ctx, "/api/tests/"+id+"/report")
}

func (c *Client) ReportSummary(ctx context.Context, id string) (*ReportSummary, error) {
	return get[*ReportSummary](c, ctx, "/api/tests/"+id+"/report/summary")
}

func (c *Client) Compare(ctx context.Context, ids string) (json.RawMessage, error) {
	data, err := c.getBytes(ctx, "/api/tests/reports/compare?ids="+ids)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func (c *Client) ExportCSV(ctx context.Context, id string) (string, error) {
	data, err := c.getBytes(ctx, "/api/tests/"+id+"/report/csv")
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
	data, err := c.getBytes(ctx, path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *Client) ExportJUnit(ctx context.Context, id string) (string, error) {
	data, err := c.getBytes(ctx, "/api/tests/"+id+"/report/junit")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *Client) ListSchedules(ctx context.Context) ([]Schedule, error) {
	return get[[]Schedule](c, ctx, "/api/schedules")
}

func (c *Client) GetSchedule(ctx context.Context, id string) (*Schedule, error) {
	return get[*Schedule](c, ctx, "/api/schedules/"+id)
}

func (c *Client) CreateSchedule(ctx context.Context, request *CreateScheduleRequest) (*Schedule, error) {
	return postJSON[*Schedule](c, ctx, "/api/schedules", request)
}

func (c *Client) UpdateSchedule(ctx context.Context, id string, request *CreateScheduleRequest) (*Schedule, error) {
	return put[*Schedule](c, ctx, "/api/schedules/"+id, request)
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
	return get[*TrendResponse](c, ctx, path)
}

func (c *Client) TrendPhases(ctx context.Context, testType string, days int) ([]string, error) {
	path := fmt.Sprintf("/api/trends/phases?type=%s&days=%d", testType, days)
	return get[[]string](c, ctx, path)
}

func (c *Client) TrendBreakdown(ctx context.Context, testType, metric string, days, baselineWindow int) (*PhaseTrendResponse, error) {
	path := fmt.Sprintf("/api/trends/breakdown?type=%s&metric=%s&days=%d&baselineWindow=%d",
		testType, metric, days, baselineWindow)
	return get[*PhaseTrendResponse](c, ctx, path)
}

func (c *Client) ReportBrokers(ctx context.Context, runID string) ([]BrokerMetricsResponse, error) {
	path := fmt.Sprintf("/api/tests/%s/report/brokers", runID)
	return get[[]BrokerMetricsResponse](c, ctx, path)
}

func (c *Client) ReportSnapshot(ctx context.Context, runID string) (*ClusterSnapshotResponse, error) {
	path := fmt.Sprintf("/api/tests/%s/report/snapshot", runID)
	return get[*ClusterSnapshotResponse](c, ctx, path)
}

func (c *Client) BrokerTrend(ctx context.Context, testType, metric string, brokerId, days, baselineWindow int) (*BrokerTrendResponse, error) {
	path := fmt.Sprintf("/api/trends/broker?type=%s&metric=%s&brokerId=%d&days=%d&baselineWindow=%d",
		testType, metric, brokerId, days, baselineWindow)
	return get[*BrokerTrendResponse](c, ctx, path)
}

func (c *Client) Resilience(ctx context.Context, request interface{}) (*ResilienceResult, error) {
	return postJSON[*ResilienceResult](c, ctx, "/api/resilience", request)
}

func (c *Client) RunDisruption(ctx context.Context, plan interface{}) (*DisruptionRunResponse, error) {
	return postJSON[*DisruptionRunResponse](c, ctx, "/api/disruptions", plan)
}

func (c *Client) RunDryRun(ctx context.Context, plan interface{}) (*DryRunResult, error) {
	return postJSON[*DryRunResult](c, ctx, "/api/disruptions?dryRun=true", plan)
}

func (c *Client) DisruptionStatus(ctx context.Context, id string) (*DisruptionReport, error) {
	path := fmt.Sprintf("/api/disruptions/%s", id)
	return get[*DisruptionReport](c, ctx, path)
}

func (c *Client) DisruptionTimelineData(ctx context.Context, id string) ([]DisruptionTimeline, error) {
	path := fmt.Sprintf("/api/disruptions/%s/timeline", id)
	return get[[]DisruptionTimeline](c, ctx, path)
}

func (c *Client) DisruptionTypes(ctx context.Context) ([]DisruptionTypeInfo, error) {
	return get[[]DisruptionTypeInfo](c, ctx, "/api/disruptions/types")
}

func (c *Client) DisruptionList(ctx context.Context, limit int) ([]DisruptionListEntry, error) {
	path := fmt.Sprintf("/api/disruptions?limit=%d", limit)
	return get[[]DisruptionListEntry](c, ctx, path)
}

func (c *Client) DisruptionKafkaMetrics(ctx context.Context, id string) ([]KafkaMetricsEntry, error) {
	path := fmt.Sprintf("/api/disruptions/%s/kafka-metrics", id)
	return get[[]KafkaMetricsEntry](c, ctx, path)
}

func (c *Client) PlaybookList(ctx context.Context) ([]PlaybookEntry, error) {
	return get[[]PlaybookEntry](c, ctx, "/api/disruptions/playbooks")
}

func (c *Client) PlaybookRun(ctx context.Context, name string) (*DisruptionRunResponse, error) {
	path := fmt.Sprintf("/api/disruptions/playbooks/%s", name)
	return postJSON[*DisruptionRunResponse](c, ctx, path, nil)
}

func (c *Client) DisruptionScheduleList(ctx context.Context) ([]DisruptionScheduleEntry, error) {
	return get[[]DisruptionScheduleEntry](c, ctx, "/api/disruptions/schedules")
}

func (c *Client) DisruptionScheduleCreate(ctx context.Context, body map[string]interface{}) (json.RawMessage, error) {
	return postJSON[json.RawMessage](c, ctx, "/api/disruptions/schedules", body)
}

func (c *Client) DisruptionScheduleDelete(ctx context.Context, id string) error {
	path := fmt.Sprintf("/api/disruptions/schedules/%s", id)
	return c.delete(ctx, path)
}

func (c *Client) ListWebhooks(ctx context.Context) ([]WebhookRegistration, error) {
	return get[[]WebhookRegistration](c, ctx, "/api/webhooks")
}

func (c *Client) RegisterWebhook(ctx context.Context, name, url string) error {
	payload := map[string]string{"name": name, "url": url, "events": "test.completed"}
	_, err := postJSON[json.RawMessage](c, ctx, "/api/webhooks", payload)
	return err
}

func (c *Client) DeleteWebhook(ctx context.Context, name string) error {
	return c.delete(ctx, "/api/webhooks/"+name)
}

func (c *Client) KafkaBrokers(ctx context.Context) (*ClusterInfo, error) {
	return get[*ClusterInfo](c, ctx, "/api/kafka/brokers")
}

func (c *Client) KafkaTopics(ctx context.Context) ([]KafkaTopic, error) {
	return get[[]KafkaTopic](c, ctx, "/api/kafka/topics")
}

func (c *Client) KafkaTopicDetail(ctx context.Context, name string) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, "/api/kafka/topics/"+name)
}

func (c *Client) KafkaGroups(ctx context.Context) ([]map[string]interface{}, error) {
	return get[[]map[string]interface{}](c, ctx, "/api/kafka/groups")
}

func (c *Client) KafkaGroupDetail(ctx context.Context, id string) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, "/api/kafka/groups/"+id)
}

func (c *Client) KafkaConsume(ctx context.Context, topic string, offset string, limit int) ([]KafkaRecord, error) {
	path := fmt.Sprintf("/api/kafka/consume/%s?offset=%s&limit=%d", topic, offset, limit)
	return get[[]KafkaRecord](c, ctx, path)
}

func (c *Client) KafkaProduce(ctx context.Context, topic, key, value string) (*ProduceMeta, error) {
	payload := map[string]string{"key": key, "value": value}
	return postJSON[*ProduceMeta](c, ctx, "/api/kafka/produce/"+topic, payload)
}

func patch[T any](c *Client, ctx context.Context, path string, payload interface{}) (T, error) {
	var result T
	data, err := json.Marshal(payload)
	if err != nil {
		return result, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	respData, err := c.doRequest(ctx, req, false)
	if err != nil {
		return result, err
	}
	if len(respData) == 0 {
		return result, nil
	}
	return result, json.Unmarshal(respData, &result)
}

func (c *Client) KafkaCreateTopic(ctx context.Context, request *CreateTopicRequest) (map[string]interface{}, error) {
	return postJSON[map[string]interface{}](c, ctx, "/api/kafka/topics", request)
}

func (c *Client) KafkaAlterTopic(ctx context.Context, name string, request *AlterTopicRequest) (map[string]interface{}, error) {
	return patch[map[string]interface{}](c, ctx, "/api/kafka/topics/"+name, request)
}

func (c *Client) KafkaDeleteTopic(ctx context.Context, name string) error {
	return c.delete(ctx, "/api/kafka/topics/"+name)
}

func (c *Client) BaselineSet(ctx context.Context, testType, runID string) (*BaselineEntry, error) {
	req := SetBaselineRequest{RunID: runID}
	return put[*BaselineEntry](c, ctx, "/api/tests/baselines/"+testType, req)
}

func (c *Client) BaselineUnset(ctx context.Context, testType string) error {
	return c.delete(ctx, "/api/tests/baselines/"+testType)
}

func (c *Client) BaselineGet(ctx context.Context, testType string) (*BaselineEntry, error) {
	return get[*BaselineEntry](c, ctx, "/api/tests/baselines/"+testType)
}

func (c *Client) BaselineList(ctx context.Context) ([]BaselineEntry, error) {
	return get[[]BaselineEntry](c, ctx, "/api/tests/baselines")
}

func (c *Client) ReportRegression(ctx context.Context, runID string) (*RegressionReport, error) {
	return get[*RegressionReport](c, ctx, "/api/tests/"+runID+"/report/regression")
}

func (c *Client) ReportTuning(ctx context.Context, runID string) (*TuningReport, error) {
	return get[*TuningReport](c, ctx, "/api/tests/"+runID+"/report/tuning")
}

func (c *Client) TuningTypes(ctx context.Context) ([]TuningTypeInfo, error) {
	return get[[]TuningTypeInfo](c, ctx, "/api/tests/tuning/types")
}

func (c *Client) Audit(ctx context.Context, limit int, eventType, since string) ([]AuditEntry, error) {
	size := limit
	if size <= 0 {
		size = 50
	}
	path := fmt.Sprintf("/api/audit?page=0&size=%d", size)
	if eventType != "" {
		path += "&type=" + eventType
	}
	if since != "" {
		path += "&since=" + since
	}
	var paged struct {
		Items []AuditEntry `json:"items"`
	}
	data, err := c.getBytes(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &paged); err != nil {
		return nil, err
	}
	return paged.Items, nil
}
