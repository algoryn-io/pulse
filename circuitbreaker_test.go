package pulse

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWithCircuitBreakerStartsClosed(t *testing.T) {
	scenario := Apply(func(context.Context) (int, error) {
		return http.StatusOK, nil
	}, WithCircuitBreaker(0.5, 10*time.Second, time.Second))

	for range 3 {
		status, err := scenario(context.Background())
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if status != http.StatusOK {
			t.Fatalf("expected status 200, got %d", status)
		}
	}
}

func TestWithCircuitBreakerOpensWhenErrorRateExceedsThreshold(t *testing.T) {
	scenario := Apply(func(context.Context) (int, error) {
		return 0, errors.New("upstream failed")
	}, WithCircuitBreaker(0.3, 10*time.Second, time.Second))

	var sawOpen bool
	for range 10 {
		_, err := scenario(context.Background())
		if errors.Is(err, ErrCircuitOpen) {
			sawOpen = true
			break
		}
	}

	if !sawOpen {
		t.Fatal("expected circuit to open")
	}
}

func TestCircuitBreakerAllowRejectsWhenOpen(t *testing.T) {
	cb := newCircuitBreaker(0.5, 10*time.Second, time.Second)
	now := time.Now()

	cb.mu.Lock()
	cb.state = cbOpen
	cb.openedAt = now
	allowed := cb.allow(now)
	cb.mu.Unlock()

	if allowed {
		t.Fatal("expected allow to reject while circuit is open")
	}
	if cb.total != 0 {
		t.Fatalf("expected total to remain 0, got %d", cb.total)
	}
}

func TestCircuitBreakerTransitionsToHalfOpenAfterTimeout(t *testing.T) {
	cb := newCircuitBreaker(0.5, 10*time.Second, 100*time.Millisecond)
	now := time.Now()

	cb.mu.Lock()
	cb.state = cbOpen
	cb.openedAt = now.Add(-200 * time.Millisecond)
	allowed := cb.allow(now)
	state := cb.state
	cb.mu.Unlock()

	if !allowed {
		t.Fatal("expected allow to permit probe after timeout")
	}
	if state != cbHalfOpen {
		t.Fatalf("expected half-open state, got %v", state)
	}
}

func TestCircuitBreakerHalfOpenSuccessClosesCircuit(t *testing.T) {
	cb := newCircuitBreaker(0.5, 10*time.Second, time.Second)
	now := time.Now()

	cb.mu.Lock()
	cb.state = cbHalfOpen
	cb.record(true, now)
	state := cb.state
	total := cb.total
	cb.mu.Unlock()

	if state != cbClosed {
		t.Fatalf("expected closed state, got %v", state)
	}
	if total != 0 {
		t.Fatalf("expected total reset to 0, got %d", total)
	}
}

func TestCircuitBreakerHalfOpenFailureReopensCircuit(t *testing.T) {
	cb := newCircuitBreaker(0.5, 10*time.Second, time.Second)
	now := time.Now()

	cb.mu.Lock()
	cb.state = cbHalfOpen
	cb.record(false, now)
	state := cb.state
	openedAt := cb.openedAt
	cb.mu.Unlock()

	if state != cbOpen {
		t.Fatalf("expected open state, got %v", state)
	}
	if openedAt != now {
		t.Fatalf("expected openedAt %v, got %v", now, openedAt)
	}
}

func TestCircuitBreakerWindowResetsAfterWindowDuration(t *testing.T) {
	cb := newCircuitBreaker(0.8, 50*time.Millisecond, time.Second)
	start := time.Now()

	cb.mu.Lock()
	cb.record(false, start)
	cb.record(false, start.Add(10*time.Millisecond))
	cb.record(true, start.Add(20*time.Millisecond))
	cb.record(true, start.Add(30*time.Millisecond))
	cb.record(true, start.Add(40*time.Millisecond))
	cb.record(false, start.Add(100*time.Millisecond))
	failures := cb.failures
	total := cb.total
	windowStart := cb.windowStart
	cb.mu.Unlock()

	if failures != 1 {
		t.Fatalf("expected failures reset to 1, got %d", failures)
	}
	if total != 1 {
		t.Fatalf("expected total reset to 1, got %d", total)
	}
	if !windowStart.Equal(start.Add(100 * time.Millisecond)) {
		t.Fatalf("expected windowStart to be reset, got %v", windowStart)
	}
}

func TestWithCircuitBreakerIntegratesWithRunT(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: time.Second}

	baseScenario := func(ctx context.Context) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		if err != nil {
			return 0, err
		}

		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()

		return resp.StatusCode, nil
	}

	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 120 * time.Millisecond, ArrivalRate: 30},
			},
			MaxConcurrency: 4,
		},
		Scenario: Apply(baseScenario,
			WithCircuitBreaker(0.5, 5*time.Second, 100*time.Millisecond),
			WithErrorRate(0.8),
		),
	}

	result := RunT(t, test)
	if result.Total <= 0 {
		t.Fatalf("expected Total > 0, got %d", result.Total)
	}
	if len(result.ErrorCounts) == 0 {
		t.Fatalf("expected ErrorCounts to have entries, got %+v", result.ErrorCounts)
	}
}
