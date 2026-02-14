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

func (c *Client) Trends(ctx context.Context, testType, metric string, days, baselineWindow int) (*TrendResponse, error) {
	path := fmt.Sprintf("/api/trends?type=%s&metric=%s&days=%d&baselineWindow=%d",
		testType, metric, days, baselineWindow)
	data, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result TrendResponse
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
