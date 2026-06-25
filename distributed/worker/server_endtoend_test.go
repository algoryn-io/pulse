package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"algoryn.io/pulse/distributed"
)

// freePort binds an ephemeral port, closes it, and returns the address so a
// caller can hand a concrete "host:port" to ListenAndServe. There is a small
// race between close and re-bind, but it is acceptable for a local test.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func marshalRun(t *testing.T, req distributed.RunRequest) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.NewReader(b)
}

func TestHandlePingRejectsWrongMethod(t *testing.T) {
	s := New(nil)
	w := httptest.NewRecorder()
	s.handlePing(w, httptest.NewRequest(http.MethodPost, "/ping", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("ping POST: got %d, want 405", w.Code)
	}
}

func TestHandleRunRejectsWrongMethod(t *testing.T) {
	s := New(nil)
	w := httptest.NewRecorder()
	s.handleRun(w, httptest.NewRequest(http.MethodGet, "/run", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("run GET: got %d, want 405", w.Code)
	}
}

func TestHandleRunRejectsMalformedJSON(t *testing.T) {
	s := New(nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/run", strings.NewReader("{not json"))
	s.handleRun(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON: got %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid request") {
		t.Fatalf("expected an invalid-request message, got %q", w.Body.String())
	}
}

func TestHandleRunRejectsMissingScenario(t *testing.T) {
	// nil pre-registered scenario + nil HTTPScenario => resolveScenario errors.
	s := New(nil)
	body := marshalRun(t, distributed.RunRequest{
		Phases: []distributed.Phase{{Type: "constant", ArrivalRate: 1, Duration: time.Millisecond}},
	})
	w := httptest.NewRecorder()
	s.handleRun(w, httptest.NewRequest(http.MethodPost, "/run", body))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing scenario: got %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "no scenario registered") {
		t.Fatalf("expected a no-scenario message, got %q", w.Body.String())
	}
}

func TestHandleRunRejectsInvalidPhaseType(t *testing.T) {
	s := New(func(context.Context) (int, error) { return 200, nil })
	body := marshalRun(t, distributed.RunRequest{
		Phases: []distributed.Phase{{Type: "  ", ArrivalRate: 1, Duration: time.Millisecond}},
	})
	w := httptest.NewRecorder()
	s.handleRun(w, httptest.NewRequest(http.MethodPost, "/run", body))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty phase type: got %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "type is required") {
		t.Fatalf("expected a phase-type message, got %q", w.Body.String())
	}
}

// TestHandleRunPreRegisteredScenario exercises the full run path: a
// pre-registered scenario is executed by the engine and the result is
// serialized into a WorkerResult.
func TestHandleRunPreRegisteredScenario(t *testing.T) {
	var calls atomic.Int64
	s := New(func(context.Context) (int, error) {
		calls.Add(1)
		return 200, nil
	})
	body := marshalRun(t, distributed.RunRequest{
		Phases:         []distributed.Phase{{Type: "constant", ArrivalRate: 200, Duration: 50 * time.Millisecond}},
		MaxConcurrency: 10,
	})
	w := httptest.NewRecorder()
	s.handleRun(w, httptest.NewRequest(http.MethodPost, "/run", body))

	if w.Code != http.StatusOK {
		t.Fatalf("run: got %d (%s), want 200", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	var res distributed.WorkerResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.Total == 0 {
		t.Fatalf("expected at least one completed request, got total=0")
	}
	if calls.Load() == 0 {
		t.Fatal("scenario was never invoked")
	}
	if res.StatusCounts["200"] == 0 {
		t.Fatalf("expected 200 status counts, got %#v", res.StatusCounts)
	}
	if len(res.Buckets) == 0 {
		t.Fatal("expected histogram buckets to be exported for merge")
	}
}

// TestHandleRunBuiltHTTPScenario exercises the CLI worker mode where the
// scenario is built from RunRequest.HTTPScenario and run against a real target.
func TestHandleRunBuiltHTTPScenario(t *testing.T) {
	var hits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer target.Close()

	s := New(nil) // CLI mode: scenario derived from the request
	body := marshalRun(t, distributed.RunRequest{
		Phases:       []distributed.Phase{{Type: "constant", ArrivalRate: 100, Duration: 50 * time.Millisecond}},
		HTTPScenario: &distributed.HTTPScenario{URL: target.URL, Method: "POST", Body: "ping"},
	})
	w := httptest.NewRecorder()
	s.handleRun(w, httptest.NewRequest(http.MethodPost, "/run", body))

	if w.Code != http.StatusOK {
		t.Fatalf("run: got %d (%s), want 200", w.Code, w.Body.String())
	}
	var res distributed.WorkerResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if hits.Load() == 0 {
		t.Fatal("target server received no requests")
	}
	if res.StatusCounts["201"] == 0 {
		t.Fatalf("expected 201 status counts, got %#v", res.StatusCounts)
	}
}

func TestResolveScenarioPrefersPreRegistered(t *testing.T) {
	sentinel := func(context.Context) (int, error) { return 418, nil }
	s := New(sentinel)
	// Even with an HTTPScenario present, the pre-registered scenario wins.
	got, err := s.resolveScenario(&distributed.HTTPScenario{URL: "http://example", Method: "GET"})
	if err != nil {
		t.Fatalf("resolveScenario: %v", err)
	}
	code, _ := got(context.Background())
	if code != 418 {
		t.Fatalf("expected pre-registered scenario (418), got %d", code)
	}
}

// TestBuildHTTPScenarioDefaultsAndBody verifies that an empty method defaults
// to GET, custom headers are forwarded, and a non-empty body is sent.
func TestBuildHTTPScenarioDefaultsAndBody(t *testing.T) {
	var (
		gotMethod string
		gotHeader string
		gotBody   string
	)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotHeader = r.Header.Get("X-Pulse")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	s := New(nil)
	scenario := s.buildHTTPScenario(&distributed.HTTPScenario{
		URL:     target.URL,
		Method:  "", // should default to GET
		Headers: map[string]string{"X-Pulse": "1"},
		Body:    "hello",
	})
	code, err := scenario(context.Background())
	if err != nil {
		t.Fatalf("scenario: %v", err)
	}
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("method = %q, want GET (default)", gotMethod)
	}
	if gotHeader != "1" {
		t.Fatalf("X-Pulse header = %q, want 1", gotHeader)
	}
	if gotBody != "hello" {
		t.Fatalf("body = %q, want hello", gotBody)
	}
}

func TestToSchedulerPhasesMapsFields(t *testing.T) {
	in := []distributed.Phase{{
		Type:          "spike",
		Duration:      2 * time.Second,
		ArrivalRate:   5,
		From:          1,
		To:            10,
		Steps:         3,
		SpikeAt:       500 * time.Millisecond,
		SpikeDuration: 250 * time.Millisecond,
	}}
	out, err := toSchedulerPhases(in)
	if err != nil {
		t.Fatalf("toSchedulerPhases: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	p := out[0]
	if string(p.Type) != "spike" || p.Duration != 2*time.Second || p.ArrivalRate != 5 ||
		p.From != 1 || p.To != 10 || p.Steps != 3 ||
		p.SpikeAt != 500*time.Millisecond || p.SpikeDuration != 250*time.Millisecond {
		t.Fatalf("phase mapped incorrectly: %#v", p)
	}
}

// TestListenAndServeLifecycle starts a real server, pings it over HTTP, then
// cancels the context and asserts ListenAndServe returns context.Canceled.
func TestListenAndServeLifecycle(t *testing.T) {
	addr := freePort(t)
	s := New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- s.ListenAndServe(ctx, addr) }()

	// Wait for the server to accept connections by polling /ping.
	deadline := time.Now().Add(3 * time.Second)
	var pinged bool
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/ping")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				pinged = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !pinged {
		cancel()
		<-errCh
		t.Fatal("server never became reachable")
	}

	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ListenAndServe returned %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ListenAndServe did not return after context cancellation")
	}
}

func TestListenAndServeListenError(t *testing.T) {
	s := New(nil)
	// An out-of-range port makes net.Listen fail synchronously.
	err := s.ListenAndServe(context.Background(), "127.0.0.1:999999")
	if err == nil {
		t.Fatal("expected a listen error for an invalid port")
	}
	if !strings.Contains(err.Error(), "listen") {
		t.Fatalf("expected a listen error, got %v", err)
	}
}
