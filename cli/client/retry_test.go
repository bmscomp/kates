package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// doRequestWith is the one retry loop every request goes through. These
// tests drive it directly, so each property holds for whatever method a
// caller builds, not only for the one that happened to be tested.

// request builds a request to srv with method, or fails the test.
func request(t *testing.T, srv *httptest.Server, method, path string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// A 5xx is retried and a later 2xx is the answer; the API key goes out on
// every attempt.
func TestDoRequest_RecoversFrom5xx(t *testing.T) {
	var calls atomic.Int32
	var auths atomic.Int32
	c, srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer k" {
			auths.Add(1)
		}
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream down"))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	c.MaxRetries = 3
	c.APIKey = "k"

	body, err := c.doRequest(context.Background(), request(t, srv, http.MethodGet, "/x"), true)
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("body = %q, err = %v; want the third attempt's answer", body, err)
	}
	if calls.Load() != 3 || auths.Load() != 3 {
		t.Errorf("calls = %d (with key %d), want 3 and 3", calls.Load(), auths.Load())
	}
}

// When every attempt fails the error is the last 5xx, and the attempts are
// exactly MaxRetries.
func TestDoRequest_GivesUpAfterMaxRetries(t *testing.T) {
	var calls atomic.Int32
	c, srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("attempt " + string(rune('0'+calls.Add(1)))))
	})
	c.MaxRetries = 3

	_, err := c.doRequest(context.Background(), request(t, srv, http.MethodGet, "/x"), true)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), "attempt 3") {
		t.Errorf("err = %v, want the last attempt's 503", err)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want MaxRetries", calls.Load())
	}
	// The final 5xx is a typed HTTPError only when it is the last attempt's
	// answer: it is, so poll loops can read the status.
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode != http.StatusServiceUnavailable || !httpErr.Retryable() {
			t.Errorf("HTTPError = %+v, want a retryable 503", httpErr)
		}
	}
}

// MaxRetries is the number of attempts: 1 means the request is sent once and
// a 5xx is returned at once as an HTTPError.
func TestDoRequest_MaxRetriesOneIsOneAttempt(t *testing.T) {
	var calls atomic.Int32
	c, srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	c.MaxRetries = 1

	start := time.Now()
	_, err := c.doRequest(context.Background(), request(t, srv, http.MethodGet, "/x"), true)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 500 {
		t.Errorf("err = %v (%T), want an HTTPError 500", err, err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}
	if time.Since(start) > 400*time.Millisecond {
		t.Errorf("one attempt took %s: a backoff was slept although nothing was retried", time.Since(start))
	}
}

// A 4xx is never retried, retryable or not: repeating a bad request does
// not make it good.
func TestDoRequest_4xxIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	c, srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":404,"error":"Not Found","message":"no such run"}`))
	})
	c.MaxRetries = 3

	_, err := c.doRequest(context.Background(), request(t, srv, http.MethodGet, "/x"), true)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 404 || httpErr.Retryable() || !strings.Contains(err.Error(), "no such run") {
		t.Errorf("err = %v, want a 404 with the backend's message", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}
}

// A connection failure is retried like a 5xx, and the error names it.
func TestDoRequest_RetriesConnectionFailures(t *testing.T) {
	var calls atomic.Int32
	c, srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			// Close the connection without answering.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("recorder cannot hijack")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	c.MaxRetries = 2
	// A fresh connection per attempt: on a reused one net/http retries a
	// GET that was cut off by itself, which would hide the loop's own retry.
	c.HTTPClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	body, err := c.doRequest(context.Background(), request(t, srv, http.MethodGet, "/x"), true)
	if err != nil || string(body) != "ok" {
		t.Errorf("body = %q, err = %v; want the second attempt", body, err)
	}

	calls.Store(0)
	c.MaxRetries = 1
	_, err = c.doRequest(context.Background(), request(t, srv, http.MethodGet, "/x"), true)
	if err == nil || !strings.Contains(err.Error(), "connection failed") {
		t.Errorf("one attempt that was cut off: err = %v, want 'connection failed'", err)
	}
}

// A context cancelled while the loop sleeps between attempts ends the loop
// then, with the context's error, rather than after the backoff.
func TestDoRequest_CancelDuringBackoffReturnsPromptly(t *testing.T) {
	var calls atomic.Int32
	c, srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	c.MaxRetries = 10 // the sleeps would sum to minutes

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = c.doRequest(ctx, req, true)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("returned after %s, want as soon as the context ended", elapsed)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want the one attempt before the backoff", calls.Load())
	}
}

// A request that is not retryable is sent once whatever the answer: that is
// every POST, PUT, PATCH and DELETE the client sends, since repeating one
// repeats its effect.
func TestDoRequest_NonRetryableIsSentOnce(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		var calls atomic.Int32
		c, srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		c.MaxRetries = 3
		_, err := c.doRequest(context.Background(), request(t, srv, method, "/x"), false)
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != 503 {
			t.Errorf("%s: err = %v, want the 503", method, err)
		}
		if calls.Load() != 1 {
			t.Errorf("%s: calls = %d, want 1", method, calls.Load())
		}
	}
}

// The public methods choose retryable correctly: a GET retries, a POST, PUT,
// PATCH and DELETE do not. (PruneTests' dry run, the one DELETE that retries,
// is covered by TestPruneTests_RetriesOnlyADryRun.)
func TestPublicMethods_RetryOnlyReads(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name string
		call func(*Client) error
		want int32
	}{
		{"GET Health", func(c *Client) error { _, err := c.Health(ctx); return err }, 2},
		{"POST CancelTest", func(c *Client) error { return c.CancelTest(ctx, "r1") }, 1},
		{"PATCH KafkaAlterTopic", func(c *Client) error { _, err := c.KafkaAlterTopic(ctx, "t", &AlterTopicRequest{}); return err }, 1},
		{"DELETE DeleteTest", func(c *Client) error { return c.DeleteTest(ctx, "r1") }, 1},
	} {
		var calls atomic.Int32
		c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		})
		c.MaxRetries = 2
		if err := tt.call(c); err == nil {
			t.Errorf("%s: no error from a server that always fails", tt.name)
		}
		if calls.Load() != tt.want {
			t.Errorf("%s: calls = %d, want %d", tt.name, calls.Load(), tt.want)
		}
	}
}

// A PUT through the generic helper is sent once too.
func TestPut_IsSentOnce(t *testing.T) {
	var calls atomic.Int32
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		w.WriteHeader(http.StatusInternalServerError)
	})
	c.MaxRetries = 3
	if _, err := put[map[string]any](c, context.Background(), "/x", map[string]string{"a": "b"}); err == nil {
		t.Error("no error from a server that always fails")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}
}

// A Failing client sends nothing and returns its error from every method,
// the stream included.
func TestFailing(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	t.Cleanup(srv.Close)

	setup := errors.New("context \"prod\" names no server")
	c := Failing(setup)
	c.BaseURL = srv.URL // even pointed at a live server
	ctx := context.Background()

	if _, err := c.Health(ctx); !errors.Is(err, setup) {
		t.Errorf("Health: err = %v, want the setup error", err)
	}
	if err := c.CancelTest(ctx, "r1"); !errors.Is(err, setup) {
		t.Errorf("CancelTest: err = %v, want the setup error", err)
	}
	if err := c.DeleteTest(ctx, "r1"); !errors.Is(err, setup) {
		t.Errorf("DeleteTest: err = %v, want the setup error", err)
	}
	if _, err := c.DisruptionStream(ctx, "d1"); !errors.Is(err, setup) {
		t.Errorf("DisruptionStream: err = %v, want the setup error", err)
	}
	if _, err := c.doRequestWith(ctx, &http.Client{}, request(t, srv, http.MethodGet, "/x"), true); !errors.Is(err, setup) {
		t.Errorf("doRequestWith: err = %v, want the setup error", err)
	}
	if calls.Load() != 0 {
		t.Errorf("a Failing client sent %d requests", calls.Load())
	}
	if c := New("http://x"); c.setupErr != nil {
		t.Errorf("an ordinary client carries a setup error: %v", c.setupErr)
	}
}

// The stream carries the API key and asks for events; its body is returned
// open, unbounded by the client's timeout.
func TestDisruptionStream(t *testing.T) {
	var gotAuth, gotAccept string
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		if r.URL.EscapedPath() != "/api/disruptions/d%201/stream" {
			t.Errorf("path = %q, want the id escaped", r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: step\ndata: {}\n\n"))
	})
	c.APIKey = "secret"
	c.HTTPClient.Timeout = time.Millisecond // must not bound the stream

	body, err := c.DisruptionStream(context.Background(), "d 1")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil || string(data) != "event: step\ndata: {}\n\n" {
		t.Errorf("stream = %q, %v", data, err)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q, want the API key", gotAuth)
	}
	if gotAccept != "text/event-stream" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if _, err := c.DisruptionStream(context.Background(), ".."); !errors.Is(err, ErrInvalidPathSegment) {
		t.Errorf("an id that is a dot segment: err = %v", err)
	}
}

// A refused stream is an HTTPError with the status, and its body goes into
// the error only up to the cap, so a server that answers a refusal with an
// endless stream cannot hang the caller.
func TestDisruptionStream_RefusalBodyIsCapped(t *testing.T) {
	var sent atomic.Int64
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		chunk := []byte(strings.Repeat("x", 1024))
		for i := 0; i < 64; i++ { // 64 KiB, well past the cap
			if _, err := w.Write(chunk); err != nil {
				return
			}
			sent.Add(int64(len(chunk)))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.DisruptionStream(ctx, "d1")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("err = %v (%T), want an HTTPError 401", err, err)
	}
	if n := len(err.Error()); n > maxStreamErrorBody+64 {
		t.Errorf("error message is %d bytes, want at most the cap plus the prefix", n)
	}
	if !strings.HasPrefix(err.Error(), "HTTP 401: xxxx") {
		t.Errorf("error = %.40q, want the status and the start of the body", err.Error())
	}
}

// A stream to a server that is not there is a connection failure, not a
// panic over a nil transport.
func TestDisruptionStream_ConnectionFailure(t *testing.T) {
	c := New("http://127.0.0.1:1")
	c.HTTPClient = nil
	_, err := c.DisruptionStream(context.Background(), "d1")
	if err == nil || !strings.Contains(err.Error(), "connection failed") {
		t.Errorf("err = %v, want 'connection failed'", err)
	}
}
