package pulse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmgo38/Pulse/transport"
)

func TestIntegrationRunEndToEndWithHTTPThresholds(t *testing.T) {
	const handlerLatency = 8 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(handlerLatency)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := transport.NewHTTPClient()

	test := Test{
		Config: Config{
			Phases: []Phase{
				{
					Type:        PhaseTypeConstant,
					Duration:    200 * time.Millisecond,
					ArrivalRate: 20,
				},
			},
			MaxConcurrency: 4,
			Thresholds: Thresholds{
				MaxMeanLatency: 50 * time.Millisecond,
				MaxP95Latency:  50 * time.Millisecond,
				MaxP99Latency:  50 * time.Millisecond,
			},
		},
		Scenario: func(ctx context.Context) (int, error) {
			return client.Get(ctx, srv.URL)
		},
	}

	result, err := Run(test)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Total <= 0 {
		t.Fatalf("expected Total > 0, got %d", result.Total)
	}
	if result.Failed != 0 {
		t.Fatalf("expected Failed == 0, got %d", result.Failed)
	}
	if n := result.StatusCounts[200]; n <= 0 {
		t.Fatalf("expected StatusCounts[200] > 0, got %d", n)
	}
	if result.RPS <= 0 {
		t.Fatalf("expected RPS > 0, got %f", result.RPS)
	}

	wantOutcomes := 3
	if len(result.ThresholdOutcomes) != wantOutcomes {
		t.Fatalf("expected %d threshold outcomes, got %+v", wantOutcomes, result.ThresholdOutcomes)
	}
	for i, o := range result.ThresholdOutcomes {
		if !o.Pass {
			t.Fatalf("outcome %d: want Pass true, got %+v", i, o)
		}
	}
}

func TestIntegration_Run_WithMixedStatusCodes(t *testing.T) {
	var reqNum atomic.Uint64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := reqNum.Add(1)
		if n%2 == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := transport.NewHTTPClient()

	test := Test{
		Config: Config{
			Phases: []Phase{
				{
					Type:        PhaseTypeConstant,
					Duration:    300 * time.Millisecond,
					ArrivalRate: 20,
				},
			},
			MaxConcurrency: 4,
			Thresholds: Thresholds{
				ErrorRate: 0.2,
			},
		},
		Scenario: func(ctx context.Context) (int, error) {
			return client.Get(ctx, srv.URL)
		},
	}

	result, err := Run(test)
	if err == nil {
		t.Fatal("expected threshold error, got nil")
	}

	if result.Total <= 0 {
		t.Fatalf("expected Total > 0, got %d", result.Total)
	}
	if result.Failed <= 0 {
		t.Fatalf("expected Failed > 0, got %d", result.Failed)
	}
	if result.StatusCounts[200] <= 0 {
		t.Fatalf("expected StatusCounts[200] > 0, got %d", result.StatusCounts[200])
	}
	if result.StatusCounts[500] <= 0 {
		t.Fatalf("expected StatusCounts[500] > 0, got %d", result.StatusCounts[500])
	}
	if result.ErrorCounts["http_status_error"] <= 0 {
		t.Fatalf("expected ErrorCounts[http_status_error] > 0, got %+v", result.ErrorCounts)
	}

	if len(result.ThresholdOutcomes) != 1 {
		t.Fatalf("expected 1 threshold outcome, got %+v", result.ThresholdOutcomes)
	}
	o := result.ThresholdOutcomes[0]
	if o.Pass {
		t.Fatalf("expected FAIL threshold outcome, got %+v", o)
	}
	if o.Description != "error_rate < 0.2" {
		t.Fatalf("unexpected description %q", o.Description)
	}
}

func TestIntegration_Run_WithTimeouts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := transport.NewHTTPClientWith(transport.HTTPClientConfig{
		Timeout: 40 * time.Millisecond,
	})

	test := Test{
		Config: Config{
			Phases: []Phase{
				{
					Type:        PhaseTypeConstant,
					Duration:    250 * time.Millisecond,
					ArrivalRate: 10,
				},
			},
			MaxConcurrency: 4,
			Thresholds: Thresholds{
				ErrorRate: 0.1,
			},
		},
		Scenario: func(ctx context.Context) (int, error) {
			return client.Get(ctx, srv.URL)
		},
	}

	result, err := Run(test)
	if err == nil {
		t.Fatal("expected threshold error, got nil")
	}

	if result.Total <= 0 {
		t.Fatalf("expected Total > 0, got %d", result.Total)
	}
	if result.Failed <= 0 {
		t.Fatalf("expected Failed > 0, got %d", result.Failed)
	}
	if result.ErrorCounts["deadline_exceeded"] <= 0 {
		t.Fatalf("expected ErrorCounts[deadline_exceeded] > 0, got %+v", result.ErrorCounts)
	}
	if result.StatusCounts[200] != 0 {
		t.Fatalf("expected no successful 200 responses before timeout, StatusCounts[200]=%d", result.StatusCounts[200])
	}

	if len(result.ThresholdOutcomes) != 1 {
		t.Fatalf("expected 1 threshold outcome, got %+v", result.ThresholdOutcomes)
	}
	o := result.ThresholdOutcomes[0]
	if o.Pass {
		t.Fatalf("expected FAIL threshold outcome, got %+v", o)
	}
	if o.Description != "error_rate < 0.1" {
		t.Fatalf("unexpected description %q", o.Description)
	}
}
