package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"algoryn.io/pulse/distributed"
	"algoryn.io/pulse/transport"
)

func TestBuildHTTPScenarioRunsForwardedChecks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	s := New(nil)

	// Passing checks.
	pass := s.buildHTTPScenario(&distributed.HTTPScenario{
		URL: srv.URL, Method: "GET",
		Checks: &distributed.HTTPChecks{Status: 200, BodyContains: []string{"pong"}},
	})
	if code, err := pass(context.Background()); err != nil || code != 200 {
		t.Fatalf("passing checks: code=%d err=%v", code, err)
	}

	// Failing body check surfaces as a check failure.
	fail := s.buildHTTPScenario(&distributed.HTTPScenario{
		URL: srv.URL, Method: "GET",
		Checks: &distributed.HTTPChecks{BodyContains: []string{"nope"}},
	})
	code, err := fail(context.Background())
	if err == nil || !errors.Is(err, transport.ErrCheckFailed) {
		t.Fatalf("failing check: expected ErrCheckFailed, got code=%d err=%v", code, err)
	}
	if code != 200 {
		t.Fatalf("status should still be reported: got %d", code)
	}
}

// Without an explicit status check, a forwarded 4xx/5xx still fails (default
// semantics preserved on the worker).
func TestBuildHTTPScenarioChecksPreserve4xxDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := New(nil)
	scenario := s.buildHTTPScenario(&distributed.HTTPScenario{
		URL: srv.URL, Method: "GET",
		Checks: &distributed.HTTPChecks{BodyContains: []string{}}, // non-nil, no status
	})
	code, err := scenario(context.Background())
	var statusErr *transport.HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected HTTPStatusError, got %v", err)
	}
	if code != 500 {
		t.Fatalf("status = %d, want 500", code)
	}
}

// End-to-end through handleRun: a forwarded failing check is counted under
// "check_failed" in the returned WorkerResult.
func TestHandleRunForwardedCheckFailedCategory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // 200, but body won't match the check
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	s := New(nil)
	body := marshalRun(t, distributed.RunRequest{
		Phases: []distributed.Phase{{Type: "constant", ArrivalRate: 100, Duration: 50 * time.Millisecond}},
		HTTPScenario: &distributed.HTTPScenario{
			URL: srv.URL, Method: "GET",
			Checks: &distributed.HTTPChecks{Status: 200, BodyContains: []string{"healthy"}},
		},
	})
	w := httptest.NewRecorder()
	s.handleRun(w, httptest.NewRequest(http.MethodPost, "/run", body))
	if w.Code != http.StatusOK {
		t.Fatalf("run: got %d (%s)", w.Code, w.Body.String())
	}
	var res distributed.WorkerResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Failed == 0 || res.ErrorCounts["check_failed"] == 0 {
		t.Fatalf("expected check_failed failures, got failed=%d errors=%#v", res.Failed, res.ErrorCounts)
	}
	// Status code is still recorded as 200.
	if res.StatusCounts["200"] == 0 {
		t.Fatalf("expected 200 status counts, got %#v", res.StatusCounts)
	}
}
