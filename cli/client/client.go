package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
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

func (c *Client) get(path string) ([]byte, error) {
	resp, err := c.HTTPClient.Get(c.BaseURL + path)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
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

func (c *Client) postJSON(path string, payload interface{}) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	resp, err := c.HTTPClient.Post(c.BaseURL+path, "application/json", strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
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

func (c *Client) put(path string, payload interface{}) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPut, c.BaseURL+path, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (c *Client) delete(path string) error {
	req, err := http.NewRequest(http.MethodDelete, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Health returns the health check response.
func (c *Client) Health() (map[string]interface{}, error) {
	data, err := c.get("/api/health")
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// ClusterInfo returns Kafka cluster metadata.
func (c *Client) ClusterInfo() (map[string]interface{}, error) {
	data, err := c.get("/api/cluster/info")
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// Topics returns the list of Kafka topics.
func (c *Client) Topics() ([]string, error) {
	data, err := c.get("/api/cluster/topics")
	if err != nil {
		return nil, err
	}
	var result []string
	return result, json.Unmarshal(data, &result)
}

// ListTests returns paginated test runs.
func (c *Client) ListTests(testType, status string, page, size int) (json.RawMessage, error) {
	path := fmt.Sprintf("/api/tests?page=%d&size=%d", page, size)
	if testType != "" {
		path += "&type=" + testType
	}
	if status != "" {
		path += "&status=" + status
	}
	data, err := c.get(path)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

// GetTest returns a specific test run.
func (c *Client) GetTest(id string) (map[string]interface{}, error) {
	data, err := c.get("/api/tests/" + id)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// CreateTest starts a new test.
func (c *Client) CreateTest(request map[string]interface{}) (map[string]interface{}, error) {
	data, err := c.postJSON("/api/tests", request)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// DeleteTest removes a test run.
func (c *Client) DeleteTest(id string) error {
	return c.delete("/api/tests/" + id)
}

// TestTypes returns available test types.
func (c *Client) TestTypes() ([]string, error) {
	data, err := c.get("/api/tests/types")
	if err != nil {
		return nil, err
	}
	var result []string
	return result, json.Unmarshal(data, &result)
}

// Backends returns available benchmark backends.
func (c *Client) Backends() ([]string, error) {
	data, err := c.get("/api/tests/backends")
	if err != nil {
		return nil, err
	}
	var result []string
	return result, json.Unmarshal(data, &result)
}

// Report returns the full report for a test run.
func (c *Client) Report(id string) (map[string]interface{}, error) {
	data, err := c.get("/api/tests/" + id + "/report")
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// ReportSummary returns the summary for a test run.
func (c *Client) ReportSummary(id string) (map[string]interface{}, error) {
	data, err := c.get("/api/tests/" + id + "/report/summary")
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// Compare returns comparison of multiple test runs.
func (c *Client) Compare(ids string) (json.RawMessage, error) {
	data, err := c.get("/api/reports/compare?ids=" + ids)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

// ExportCSV returns CSV report for download.
func (c *Client) ExportCSV(id string) (string, error) {
	data, err := c.get("/api/tests/" + id + "/report/csv")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ExportJUnit returns JUnit XML report.
func (c *Client) ExportJUnit(id string) (string, error) {
	data, err := c.get("/api/tests/" + id + "/report/junit")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ListSchedules returns all scheduled tests.
func (c *Client) ListSchedules() ([]map[string]interface{}, error) {
	data, err := c.get("/api/schedules")
	if err != nil {
		return nil, err
	}
	var result []map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// GetSchedule returns a specific schedule.
func (c *Client) GetSchedule(id string) (map[string]interface{}, error) {
	data, err := c.get("/api/schedules/" + id)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// CreateSchedule creates a new scheduled test.
func (c *Client) CreateSchedule(request map[string]interface{}) (map[string]interface{}, error) {
	data, err := c.postJSON("/api/schedules", request)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// UpdateSchedule updates an existing schedule.
func (c *Client) UpdateSchedule(id string, request map[string]interface{}) (map[string]interface{}, error) {
	data, err := c.put("/api/schedules/"+id, request)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// DeleteSchedule removes a schedule.
func (c *Client) DeleteSchedule(id string) error {
	return c.delete("/api/schedules/" + id)
}

// Trends returns historical trend data.
func (c *Client) Trends(testType, metric string, days, baselineWindow int) (map[string]interface{}, error) {
	path := fmt.Sprintf("/api/trends?type=%s&metric=%s&days=%d&baselineWindow=%d",
		testType, metric, days, baselineWindow)
	data, err := c.get(path)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// Resilience executes a combined resilience test.
func (c *Client) Resilience(request map[string]interface{}) (map[string]interface{}, error) {
	data, err := c.postJSON("/api/resilience", request)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}
