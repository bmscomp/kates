package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	MaxRetries int
	APIKey     string
}

type ClientOptions struct {
	BaseURL  string
	APIKey   string
	ProxyURL string
	Insecure bool
}

func NewWithOptions(opts ClientOptions) *Client {
	proxyFn := http.ProxyFromEnvironment
	if shouldBypassProxy(opts.BaseURL) {
		// Local endpoints should bypass env proxies by default to avoid
		// accidental routing through corporate/local proxy daemons.
		proxyFn = nil
	}

	transport := &http.Transport{
		Proxy: proxyFn,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 15 * time.Second,
		}).DialContext,
		DisableKeepAlives: false,
	}

	if opts.ProxyURL != "" {
		if proxyURL, err := url.Parse(opts.ProxyURL); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	if opts.Insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	return &Client{
		BaseURL: strings.TrimRight(opts.BaseURL, "/"),
		APIKey:  opts.APIKey,
		HTTPClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: transport,
		},
		MaxRetries: 3,
	}
}

func shouldBypassProxy(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}

	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func New(baseURL string) *Client {
	return NewWithOptions(ClientOptions{BaseURL: baseURL})
}

func NewWithAPIKey(baseURL, apiKey string) *Client {
	return NewWithOptions(ClientOptions{BaseURL: baseURL, APIKey: apiKey})
}

type APIError struct {
	Status  int    `json:"status"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

func (e *APIError) String() string {
	return fmt.Sprintf("[%d] %s: %s", e.Status, e.Error, e.Message)
}

// HTTPError is a non-2xx response, carrying the status code so callers can tell
// a permanent failure from one worth retrying. Poll loops previously saw only an
// opaque error string and could not distinguish "the server is briefly
// unreachable" from "your key is wrong", so they retried both for their whole
// budget and then reported a misleading timeout.
type HTTPError struct {
	StatusCode int
	message    string
}

func (e *HTTPError) Error() string {
	return e.message
}

// Retryable reports whether repeating the request could plausibly succeed.
func (e *HTTPError) Retryable() bool {
	return e.StatusCode >= 500 ||
		e.StatusCode == http.StatusRequestTimeout ||
		e.StatusCode == http.StatusTooManyRequests
}

// ErrInvalidPathSegment is wrapped by the error a method returns, before it
// sends anything, when a name or id it would put in the request path is one
// that escaping cannot make safe (see pathf). Callers tell it apart from a
// transport or HTTP failure with errors.Is.
var ErrInvalidPathSegment = errors.New("not a valid name or id")

// pathf builds a request path from a format whose %s verbs each stand for one
// path segment taken from caller input: a run id, a topic, a group, a type.
//
// Segments used to be concatenated as given, so the input could choose the
// endpoint: the id "../security/pentest" went out as
// /api/tests/../security/pentest, which a server that resolves dot segments
// routes to /api/security/pentest, and a group id containing "/" or "?" split
// into more segments or a query string. Each segment is now percent-escaped,
// which keeps it one segment. Escaping cannot neutralise three values, so they
// are refused with ErrInvalidPathSegment: "" drops the segment and addresses
// the collection instead of a member, and "." and ".." are dot segments, which
// RFC 3986 normalisation resolves even when percent-encoded.
func pathf(format string, segments ...string) (string, error) {
	args := make([]any, len(segments))
	for i, s := range segments {
		switch s {
		case "":
			return "", fmt.Errorf("an empty value is %w", ErrInvalidPathSegment)
		case ".", "..":
			return "", fmt.Errorf("%q is %w", s, ErrInvalidPathSegment)
		}
		args[i] = url.PathEscape(s)
	}
	return fmt.Sprintf(format, args...), nil
}

// withQuery appends a query string built by url.Values, which escapes every
// value: a filter such as "LOAD&status=DONE" stays one value instead of adding
// a parameter, and the "+" in a timestamp offset is not read back as a space.
func withQuery(path string, query url.Values) string {
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
}

func (c *Client) doRequest(ctx context.Context, req *http.Request, retryable bool) ([]byte, error) {
	return c.doRequestWith(ctx, c.HTTPClient, req, retryable)
}

// doRequestWith sends req through hc, which is c.HTTPClient except for calls
// that carry their own deadline (postJSONWithTimeout).
func (c *Client) doRequestWith(ctx context.Context, hc *http.Client, req *http.Request, retryable bool) ([]byte, error) {
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

		if c.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}
		resp, err := hc.Do(req)
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
					return nil, &HTTPError{StatusCode: resp.StatusCode, message: apiErr.String()}
				}
			}
			return nil, &HTTPError{
				StatusCode: resp.StatusCode,
				message:    fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
			}
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

func (c *Client) ClusterAlerts(ctx context.Context) (*ClusterAlertsResponse, error) {
	return get[*ClusterAlertsResponse](c, ctx, "/api/cluster/alerts")
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
	path, err := pathf("/api/cluster/topics/%s", name)
	if err != nil {
		return nil, err
	}
	return get[*TopicDetail](c, ctx, path)
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
	path, err := pathf("/api/cluster/groups/%s", id)
	if err != nil {
		return nil, err
	}
	return get[*ConsumerGroupDetail](c, ctx, path)
}

func (c *Client) BrokerConfigs(ctx context.Context, id int) ([]BrokerConfig, error) {
	return get[[]BrokerConfig](c, ctx, fmt.Sprintf("/api/cluster/brokers/%d/configs", id))
}

func (c *Client) ClusterCheck(ctx context.Context) (*ClusterHealthReport, error) {
	return get[*ClusterHealthReport](c, ctx, "/api/cluster/check")
}

func (c *Client) ListTests(ctx context.Context, testType, status string, page, size int) (*PagedTests, error) {
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("size", strconv.Itoa(size))
	if testType != "" {
		query.Set("type", testType)
	}
	if status != "" {
		query.Set("status", status)
	}
	return get[*PagedTests](c, ctx, withQuery("/api/tests", query))
}

func (c *Client) GetTest(ctx context.Context, id string) (*TestRun, error) {
	path, err := pathf("/api/tests/%s", id)
	if err != nil {
		return nil, err
	}
	return get[*TestRun](c, ctx, path)
}

func (c *Client) CreateTest(ctx context.Context, request *CreateTestRequest) (*TestRun, error) {
	return postJSON[*TestRun](c, ctx, "/api/tests", request)
}

// RerunTest is CreateTest for a spec the backend served, sent back as JSON.
func (c *Client) RerunTest(ctx context.Context, request *RerunTestRequest) (*TestRun, error) {
	return postJSON[*TestRun](c, ctx, "/api/tests", request)
}

func (c *Client) CreateCompareRebalanceTest(ctx context.Context, request *CreateTestRequest) (*TestRun, error) {
	return postJSON[*TestRun](c, ctx, "/api/tests/compare-rebalance", request)
}

func (c *Client) DeleteTest(ctx context.Context, id string) error {
	path, err := pathf("/api/tests/%s", id)
	if err != nil {
		return err
	}
	return c.delete(ctx, path)
}

func (c *Client) CancelTest(ctx context.Context, id string) error {
	path, err := pathf("/api/tests/%s/cancel", id)
	if err != nil {
		return err
	}
	_, err = postJSON[json.RawMessage](c, ctx, path, nil)
	return err
}

func (c *Client) TestTypes(ctx context.Context) ([]string, error) {
	return get[[]string](c, ctx, "/api/tests/types")
}

func (c *Client) Backends(ctx context.Context) ([]string, error) {
	return get[[]string](c, ctx, "/api/tests/backends")
}

func (c *Client) Report(ctx context.Context, id string) (*Report, error) {
	path, err := pathf("/api/tests/%s/report", id)
	if err != nil {
		return nil, err
	}
	return get[*Report](c, ctx, path)
}

func (c *Client) ReportSummary(ctx context.Context, id string) (*ReportSummary, error) {
	path, err := pathf("/api/tests/%s/report/summary", id)
	if err != nil {
		return nil, err
	}
	return get[*ReportSummary](c, ctx, path)
}

// Compare takes the run ids already joined with commas, as the backend's ids
// parameter expects; the joined string is sent as one escaped value.
func (c *Client) Compare(ctx context.Context, ids string) (json.RawMessage, error) {
	data, err := c.getBytes(ctx, withQuery("/api/tests/reports/compare", url.Values{"ids": {ids}}))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func (c *Client) ExportCSV(ctx context.Context, id string) (string, error) {
	path, err := pathf("/api/tests/%s/report/csv", id)
	if err != nil {
		return "", err
	}
	data, err := c.getBytes(ctx, path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *Client) ExportHeatmap(ctx context.Context, id string, format string) (string, error) {
	path, err := pathf("/api/tests/%s/report/heatmap", id)
	if err != nil {
		return "", err
	}
	if format != "" {
		path = withQuery(path, url.Values{"format": {format}})
	}
	data, err := c.getBytes(ctx, path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *Client) ExportJUnit(ctx context.Context, id string) (string, error) {
	path, err := pathf("/api/tests/%s/report/junit", id)
	if err != nil {
		return "", err
	}
	data, err := c.getBytes(ctx, path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *Client) ListSchedules(ctx context.Context) ([]Schedule, error) {
	return get[[]Schedule](c, ctx, "/api/schedules")
}

func (c *Client) GetSchedule(ctx context.Context, id string) (*Schedule, error) {
	path, err := pathf("/api/schedules/%s", id)
	if err != nil {
		return nil, err
	}
	return get[*Schedule](c, ctx, path)
}

func (c *Client) CreateSchedule(ctx context.Context, request *CreateScheduleRequest) (*Schedule, error) {
	return postJSON[*Schedule](c, ctx, "/api/schedules", request)
}

func (c *Client) UpdateSchedule(ctx context.Context, id string, request *CreateScheduleRequest) (*Schedule, error) {
	path, err := pathf("/api/schedules/%s", id)
	if err != nil {
		return nil, err
	}
	return put[*Schedule](c, ctx, path, request)
}

func (c *Client) DeleteSchedule(ctx context.Context, id string) error {
	path, err := pathf("/api/schedules/%s", id)
	if err != nil {
		return err
	}
	return c.delete(ctx, path)
}

// trendQuery holds the parameters every /api/trends endpoint takes. type and
// metric are always sent, even when empty, as they were before the query was
// built with url.Values, so the backend sees the same parameters it always has.
func trendQuery(testType, metric string, days, baselineWindow int) url.Values {
	query := url.Values{}
	query.Set("type", testType)
	query.Set("metric", metric)
	query.Set("days", strconv.Itoa(days))
	query.Set("baselineWindow", strconv.Itoa(baselineWindow))
	return query
}

func (c *Client) Trends(ctx context.Context, testType, metric string, days, baselineWindow int, phase string) (*TrendResponse, error) {
	query := trendQuery(testType, metric, days, baselineWindow)
	if phase != "" {
		query.Set("phase", phase)
	}
	return get[*TrendResponse](c, ctx, withQuery("/api/trends", query))
}

func (c *Client) TrendPhases(ctx context.Context, testType string, days int) ([]string, error) {
	query := url.Values{}
	query.Set("type", testType)
	query.Set("days", strconv.Itoa(days))
	return get[[]string](c, ctx, withQuery("/api/trends/phases", query))
}

func (c *Client) TrendBreakdown(ctx context.Context, testType, metric string, days, baselineWindow int) (*PhaseTrendResponse, error) {
	query := trendQuery(testType, metric, days, baselineWindow)
	return get[*PhaseTrendResponse](c, ctx, withQuery("/api/trends/breakdown", query))
}

func (c *Client) ReportBrokers(ctx context.Context, runID string) ([]BrokerMetricsResponse, error) {
	path, err := pathf("/api/tests/%s/report/brokers", runID)
	if err != nil {
		return nil, err
	}
	return get[[]BrokerMetricsResponse](c, ctx, path)
}

func (c *Client) ReportSnapshot(ctx context.Context, runID string) (*ClusterSnapshotResponse, error) {
	path, err := pathf("/api/tests/%s/report/snapshot", runID)
	if err != nil {
		return nil, err
	}
	return get[*ClusterSnapshotResponse](c, ctx, path)
}

func (c *Client) BrokerTrend(ctx context.Context, testType, metric string, brokerId, days, baselineWindow int) (*BrokerTrendResponse, error) {
	query := trendQuery(testType, metric, days, baselineWindow)
	query.Set("brokerId", strconv.Itoa(brokerId))
	return get[*BrokerTrendResponse](c, ctx, withQuery("/api/trends/broker", query))
}

func (c *Client) Resilience(ctx context.Context, request interface{}) (*ResilienceResult, error) {
	// Resilience tests are long-running: steady-state + chaos + recovery can
	// easily exceed the default 60s client timeout. Use the same extended
	// timeout as disruption tests.
	return postJSONWithTimeout[*ResilienceResult](c, ctx, "/api/resilience", request, 20*time.Minute)
}

func postJSONWithTimeout[T any](c *Client, ctx context.Context, path string, payload interface{}, timeout time.Duration) (T, error) {
	var result T
	data, err := json.Marshal(payload)
	if err != nil {
		return result, fmt.Errorf("marshal request: %w", err)
	}

	// The deadline for this call lives on its context. The client's own
	// Timeout (60s) would still cut the call short, so it goes through a copy
	// of the client without one; the copy shares the Transport, and with it the
	// connection pool, proxy and TLS settings. This used to widen
	// c.HTTPClient.Timeout for the length of the call and restore it after,
	// which raced with every concurrent request on the same client and gave
	// them the 20-minute timeout too.
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	longClient := *c.HTTPClient
	longClient.Timeout = 0

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")

	respData, err := c.doRequestWith(timeoutCtx, &longClient, req, false)
	if err != nil {
		return result, err
	}

	if len(respData) == 0 {
		return result, nil
	}
	return result, json.Unmarshal(respData, &result)
}

// Terminal states a disruption report can settle into.
//
// REJECTED belongs here even though a plan rejected at submission never reaches
// a poll (that is a 422): a plan can also be rejected asynchronously, when it
// loses the race for the cluster's concurrency lease after being accepted.
func isTerminalDisruptionStatus(status string) bool {
	switch status {
	case "COMPLETED", "PARTIAL", "FAILED", "REJECTED", "INTERRUPTED":
		return true
	default:
		return false
	}
}

// RunDisruption submits a plan and waits for its report.
//
// The backend accepts the plan and executes it asynchronously (202 + id), so
// this polls GET /api/disruptions/{id} rather than holding a single request
// open for the life of the plan. A plan runs for minutes — the old inline call
// outlived proxy and load-balancer read timeouts, so callers saw a gateway
// error while the chaos kept running. The blocking behaviour callers expect is
// preserved here; only the transport changed.
func (c *Client) RunDisruption(ctx context.Context, plan interface{}) (*DisruptionRunResponse, error) {
	accepted, err := postJSON[*DisruptionAccepted](c, ctx, "/api/disruptions", plan)
	if err != nil {
		return nil, err
	}
	if accepted == nil || accepted.ID == "" {
		return nil, fmt.Errorf("backend did not return a disruption id")
	}

	return c.awaitDisruptionFrom(ctx, accepted.ID, accepted.Status)
}

const (
	// Same overall budget the synchronous call used.
	disruptionPollBudget = 20 * time.Minute
	disruptionPollEvery  = 5 * time.Second
	// Transient failures tolerated back-to-back before giving up (~25s).
	maxConsecutivePollFailures = 5
)

// awaitDisruption polls a report until it reaches a terminal status.
//
// Poll errors are not all equal, and treating them as equal is how this used to
// hide real failures: a 401, a 403 or a permanent 404 would be swallowed on
// every attempt for the full 20 minutes and then surface as "timed out … it may
// still be running", which is both wrong and unactionable. Permanent errors now
// fail immediately; transient ones are tolerated in a bounded run.
func (c *Client) awaitDisruption(ctx context.Context, id string) (*DisruptionRunResponse, error) {
	return c.awaitDisruptionFrom(ctx, id, "")
}

// awaitDisruptionFrom skips the first poll when the accept response already
// carried a terminal status — there is nothing to wait for, and a five-second
// sleep before reporting it is just latency.
func (c *Client) awaitDisruptionFrom(ctx context.Context, id, acceptedStatus string) (*DisruptionRunResponse, error) {
	if isTerminalDisruptionStatus(acceptedStatus) {
		report, err := c.DisruptionStatus(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("fetching disruption %s: %w", id, err)
		}
		if report != nil {
			return &DisruptionRunResponse{ID: id, Report: *report}, nil
		}
	}

	deadline := time.Now().Add(disruptionPollBudget)
	consecutiveFailures := 0

	for {
		report, err := c.DisruptionStatus(ctx, id)
		switch {
		case err == nil:
			consecutiveFailures = 0
			if report != nil && isTerminalDisruptionStatus(report.Status) {
				return &DisruptionRunResponse{ID: id, Report: *report}, nil
			}
		default:
			var httpErr *HTTPError
			if errors.As(err, &httpErr) && !httpErr.Retryable() {
				return nil, fmt.Errorf("polling disruption %s: %w", id, err)
			}
			consecutiveFailures++
			if consecutiveFailures >= maxConsecutivePollFailures {
				return nil, fmt.Errorf(
					"polling disruption %s failed %d times in a row: %w", id, consecutiveFailures, err)
			}
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf(
				"timed out waiting for disruption %s; it may still be running — check 'kates disruption status %s'",
				id, id)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(disruptionPollEvery):
		}
	}
}

func (c *Client) RunDryRun(ctx context.Context, plan interface{}) (*DryRunResult, error) {
	return postJSON[*DryRunResult](c, ctx, withQuery("/api/disruptions", url.Values{"dryRun": {"true"}}), plan)
}

func (c *Client) DisruptionStatus(ctx context.Context, id string) (*DisruptionReport, error) {
	path, err := pathf("/api/disruptions/%s", id)
	if err != nil {
		return nil, err
	}
	return get[*DisruptionReport](c, ctx, path)
}

// DisruptionStreamURL is the address of a disruption's server-sent event
// stream. `kates disruption watch` reads the stream itself rather than through
// a Client method, so it takes the escaped URL from here.
func (c *Client) DisruptionStreamURL(id string) (string, error) {
	path, err := pathf("/api/disruptions/%s/stream", id)
	if err != nil {
		return "", err
	}
	return c.BaseURL + path, nil
}

func (c *Client) DisruptionTimelineData(ctx context.Context, id string) ([]DisruptionTimeline, error) {
	path, err := pathf("/api/disruptions/%s/timeline", id)
	if err != nil {
		return nil, err
	}
	return get[[]DisruptionTimeline](c, ctx, path)
}

func (c *Client) DisruptionTypes(ctx context.Context) ([]DisruptionTypeInfo, error) {
	return get[[]DisruptionTypeInfo](c, ctx, "/api/disruptions/types")
}

func (c *Client) DisruptionList(ctx context.Context, limit int) ([]DisruptionListEntry, error) {
	path := withQuery("/api/disruptions", url.Values{"limit": {strconv.Itoa(limit)}})
	var paged struct {
		Items []DisruptionListEntry `json:"items"`
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

func (c *Client) DisruptionKafkaMetrics(ctx context.Context, id string) ([]KafkaMetricsEntry, error) {
	path, err := pathf("/api/disruptions/%s/kafka-metrics", id)
	if err != nil {
		return nil, err
	}
	return get[[]KafkaMetricsEntry](c, ctx, path)
}

func (c *Client) PlaybookList(ctx context.Context) ([]PlaybookEntry, error) {
	return get[[]PlaybookEntry](c, ctx, "/api/disruptions/playbooks")
}

// PlaybookRun starts a named playbook and waits for its report.
//
// Like RunDisruption, the backend now accepts the playbook (202 + id) and runs
// it in the background, so this polls instead of holding one request open for
// the life of the plan — which outlived proxy read timeouts and left the caller
// with a gateway error while the chaos continued.
func (c *Client) PlaybookRun(ctx context.Context, name string) (*DisruptionRunResponse, error) {
	path, err := pathf("/api/disruptions/playbooks/%s", name)
	if err != nil {
		return nil, err
	}
	accepted, err := postJSON[*DisruptionAccepted](c, ctx, path, nil)
	if err != nil {
		return nil, err
	}
	if accepted == nil || accepted.ID == "" {
		return nil, fmt.Errorf("backend did not return a disruption id for playbook %q", name)
	}
	return c.awaitDisruptionFrom(ctx, accepted.ID, accepted.Status)
}

func (c *Client) DisruptionScheduleList(ctx context.Context) ([]DisruptionScheduleEntry, error) {
	return get[[]DisruptionScheduleEntry](c, ctx, "/api/disruptions/schedules")
}

func (c *Client) DisruptionScheduleCreate(ctx context.Context, body map[string]interface{}) (json.RawMessage, error) {
	return postJSON[json.RawMessage](c, ctx, "/api/disruptions/schedules", body)
}

func (c *Client) DisruptionScheduleDelete(ctx context.Context, id string) error {
	path, err := pathf("/api/disruptions/schedules/%s", id)
	if err != nil {
		return err
	}
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
	path, err := pathf("/api/webhooks/%s", name)
	if err != nil {
		return err
	}
	return c.delete(ctx, path)
}

func (c *Client) KafkaBrokers(ctx context.Context) (*ClusterInfo, error) {
	return get[*ClusterInfo](c, ctx, "/api/kafka/brokers")
}

func (c *Client) KafkaTopics(ctx context.Context) ([]KafkaTopic, error) {
	return get[[]KafkaTopic](c, ctx, "/api/kafka/topics")
}

func (c *Client) KafkaTopicDetail(ctx context.Context, name string) (map[string]interface{}, error) {
	path, err := pathf("/api/kafka/topics/%s", name)
	if err != nil {
		return nil, err
	}
	return get[map[string]interface{}](c, ctx, path)
}

func (c *Client) KafkaGroups(ctx context.Context) ([]map[string]interface{}, error) {
	return get[[]map[string]interface{}](c, ctx, "/api/kafka/groups")
}

func (c *Client) KafkaGroupDetail(ctx context.Context, id string) (map[string]interface{}, error) {
	path, err := pathf("/api/kafka/groups/%s", id)
	if err != nil {
		return nil, err
	}
	return get[map[string]interface{}](c, ctx, path)
}

func (c *Client) KafkaConsume(ctx context.Context, topic string, offset string, limit int) ([]KafkaRecord, error) {
	path, err := pathf("/api/kafka/consume/%s", topic)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("offset", offset)
	query.Set("limit", strconv.Itoa(limit))
	return get[[]KafkaRecord](c, ctx, withQuery(path, query))
}

func (c *Client) KafkaProduce(ctx context.Context, topic, key, value string) (*ProduceMeta, error) {
	path, err := pathf("/api/kafka/produce/%s", topic)
	if err != nil {
		return nil, err
	}
	payload := map[string]string{"key": key, "value": value}
	return postJSON[*ProduceMeta](c, ctx, path, payload)
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
	path, err := pathf("/api/kafka/topics/%s", name)
	if err != nil {
		return nil, err
	}
	return patch[map[string]interface{}](c, ctx, path, request)
}

func (c *Client) KafkaDeleteTopic(ctx context.Context, name string) error {
	path, err := pathf("/api/kafka/topics/%s", name)
	if err != nil {
		return err
	}
	return c.delete(ctx, path)
}

func (c *Client) BaselineSet(ctx context.Context, testType, runID string) (*BaselineEntry, error) {
	path, err := pathf("/api/tests/baselines/%s", testType)
	if err != nil {
		return nil, err
	}
	req := SetBaselineRequest{RunID: runID}
	return put[*BaselineEntry](c, ctx, path, req)
}

func (c *Client) BaselineUnset(ctx context.Context, testType string) error {
	path, err := pathf("/api/tests/baselines/%s", testType)
	if err != nil {
		return err
	}
	return c.delete(ctx, path)
}

func (c *Client) BaselineGet(ctx context.Context, testType string) (*BaselineEntry, error) {
	path, err := pathf("/api/tests/baselines/%s", testType)
	if err != nil {
		return nil, err
	}
	return get[*BaselineEntry](c, ctx, path)
}

func (c *Client) BaselineList(ctx context.Context) ([]BaselineEntry, error) {
	return get[[]BaselineEntry](c, ctx, "/api/tests/baselines")
}

func (c *Client) ReportRegression(ctx context.Context, runID string) (*RegressionReport, error) {
	path, err := pathf("/api/tests/%s/report/regression", runID)
	if err != nil {
		return nil, err
	}
	return get[*RegressionReport](c, ctx, path)
}

func (c *Client) ReportTuning(ctx context.Context, runID string) (*TuningReport, error) {
	path, err := pathf("/api/tests/%s/report/tuning", runID)
	if err != nil {
		return nil, err
	}
	return get[*TuningReport](c, ctx, path)
}

func (c *Client) TuningTypes(ctx context.Context) ([]TuningTypeInfo, error) {
	return get[[]TuningTypeInfo](c, ctx, "/api/tests/tuning/types")
}

func (c *Client) Audit(ctx context.Context, limit int, eventType, since string) ([]AuditEntry, error) {
	size := limit
	if size <= 0 {
		size = 50
	}
	query := url.Values{}
	query.Set("page", "0")
	query.Set("size", strconv.Itoa(size))
	if eventType != "" {
		query.Set("type", eventType)
	}
	if since != "" {
		query.Set("since", since)
	}
	var paged struct {
		Items []AuditEntry `json:"items"`
	}
	data, err := c.getBytes(ctx, withQuery("/api/audit", query))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &paged); err != nil {
		return nil, err
	}
	return paged.Items, nil
}

func (c *Client) SecurityAudit(ctx context.Context) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, "/api/security/audit")
}

func (c *Client) SecurityTLS(ctx context.Context) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, "/api/security/tls")
}

func (c *Client) SecurityAuthTest(ctx context.Context, username string) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, withQuery("/api/security/auth-test", url.Values{"user": {username}}))
}

func (c *Client) SecurityPentest(ctx context.Context, testName string) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, withQuery("/api/security/pentest", url.Values{"test": {testName}}))
}

func (c *Client) SecurityCompliance(ctx context.Context) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, "/api/security/compliance")
}

func (c *Client) SecurityBaselineSave(ctx context.Context) (map[string]interface{}, error) {
	return postJSON[map[string]interface{}](c, ctx, "/api/security/baseline", nil)
}

func (c *Client) SecurityDrift(ctx context.Context) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, "/api/security/drift")
}

func (c *Client) SecurityGate(ctx context.Context, minGrade string) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, withQuery("/api/security/gate", url.Values{"min-grade": {minGrade}}))
}

func (c *Client) SecurityCerts(ctx context.Context) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, "/api/security/certs")
}

func (c *Client) SecurityCVE(ctx context.Context) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, "/api/security/cve")
}

func (c *Client) SecurityConfigDiff(ctx context.Context) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, "/api/security/config-diff")
}

func (c *Client) SecurityACLMap(ctx context.Context) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, "/api/security/acl-map")
}

func (c *Client) SecurityTrend(ctx context.Context) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, "/api/security/trend")
}

func (c *Client) SecuritySecrets(ctx context.Context) (map[string]interface{}, error) {
	return get[map[string]interface{}](c, ctx, "/api/security/secrets")
}
