package pulse

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestWithLatencyAddsLatencyWhenRateIsOne(t *testing.T) {
	scenario := Apply(func(context.Context) (int, error) {
		return http.StatusOK, nil
	}, WithLatency(20*time.Millisecond, 1.0))

	startedAt := time.Now()
	status, err := scenario(context.Background())
	elapsed := time.Since(startedAt)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}
	if elapsed < 20*time.Millisecond {
		t.Fatalf("expected elapsed >= 20ms, got %v", elapsed)
	}
}

func TestWithLatencyRespectsContextCancellation(t *testing.T) {
	scenario := Apply(func(context.Context) (int, error) {
		t.Fatal("underlying scenario should not be called after cancellation")
		return 0, nil
	}, WithLatency(time.Second, 1.0))

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)

	startedAt := time.Now()
	status, err := scenario(ctx)
	elapsed := time.Since(startedAt)

	if status != 0 {
		t.Fatalf("expected status 0, got %d", status)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if elapsed >= time.Second {
		t.Fatalf("expected cancellation before 1s, got %v", elapsed)
	}
}

func TestWithErrorRateFailsWhenRateIsOne(t *testing.T) {
	called := false

	scenario := Apply(func(context.Context) (int, error) {
		called = true
		return http.StatusOK, nil
	}, WithErrorRate(1.0))

	status, err := scenario(context.Background())

	if status != 0 {
		t.Fatalf("expected status 0, got %d", status)
	}
	if !errors.Is(err, ErrInjected) {
		t.Fatalf("expected ErrInjected, got %v", err)
	}
	if called {
		t.Fatal("expected underlying scenario not to be called")
	}
}

func TestWithErrorRateNeverFailsWhenRateIsZero(t *testing.T) {
	scenario := Apply(func(context.Context) (int, error) {
		return http.StatusOK, nil
	}, WithErrorRate(0.0))

	for range 100 {
		status, err := scenario(context.Background())
		if errors.Is(err, ErrInjected) {
			t.Fatal("expected no injected errors")
		}
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if status != http.StatusOK {
			t.Fatalf("expected status 200, got %d", status)
		}
	}
}

func TestChainAppliesMiddlewaresInOrder(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
	)

	middleware1 := func(next Scenario) Scenario {
		return func(ctx context.Context) (int, error) {
			mu.Lock()
			order = append(order, "middleware1")
			mu.Unlock()
			return next(ctx)
		}
	}

	middleware2 := func(next Scenario) Scenario {
		return func(ctx context.Context) (int, error) {
			mu.Lock()
			order = append(order, "middleware2")
			mu.Unlock()
			return next(ctx)
		}
	}

	scenario := Chain(middleware1, middleware2)(func(context.Context) (int, error) {
		mu.Lock()
		order = append(order, "scenario")
		mu.Unlock()
		return http.StatusOK, nil
	})

	_, err := scenario(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	want := []string{"middleware1", "middleware2", "scenario"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("unexpected order: want %v, got %v", want, order)
	}
}

func TestApplyEquivalentToChain(t *testing.T) {
	base := func(context.Context) (int, error) {
		return http.StatusCreated, nil
	}

	middleware := func(next Scenario) Scenario {
		return func(ctx context.Context) (int, error) {
			status, err := next(ctx)
			if err != nil {
				return 0, err
			}
			return status + 1, nil
		}
	}

	viaApply := Apply(base, middleware)
	viaChain := Chain(middleware)(base)

	gotApplyStatus, gotApplyErr := viaApply(context.Background())
	gotChainStatus, gotChainErr := viaChain(context.Background())

	if gotApplyStatus != gotChainStatus {
		t.Fatalf("expected same status, got apply=%d chain=%d", gotApplyStatus, gotChainStatus)
	}
	if !errors.Is(gotApplyErr, gotChainErr) && gotApplyErr != gotChainErr {
		t.Fatalf("expected same error, got apply=%v chain=%v", gotApplyErr, gotChainErr)
	}
}

func TestRunTWithLatencyMiddleware(t *testing.T) {
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
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
		},
		Scenario: Apply(baseScenario, WithLatency(5*time.Millisecond, 1.0)),
	}

	result := RunT(t, test)
	if result.Total <= 0 {
		t.Fatalf("expected Total > 0, got %d", result.Total)
	}
	if result.Latency.P50 < 5*time.Millisecond {
		t.Fatalf("expected P50 >= 5ms, got %v", result.Latency.P50)
	}
}

func TestWithJitterAddsLatencyWhenRateIsOne(t *testing.T) {
	scenario := Apply(func(context.Context) (int, error) {
		return http.StatusOK, nil
	}, WithJitter(10*time.Millisecond, 50*time.Millisecond, 1.0))

	startedAt := time.Now()
	status, err := scenario(context.Background())
	elapsed := time.Since(startedAt)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}
	if elapsed < 10*time.Millisecond {
		t.Fatalf("expected elapsed >= 10ms, got %v", elapsed)
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("expected elapsed < 200ms, got %v", elapsed)
	}
}

func TestWithJitterRespectsContextCancellation(t *testing.T) {
	scenario := Apply(func(context.Context) (int, error) {
		t.Fatal("underlying scenario should not be called after cancellation")
		return 0, nil
	}, WithJitter(time.Second, 2*time.Second, 1.0))

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)

	status, err := scenario(ctx)

	if status != 0 {
		t.Fatalf("expected status 0, got %d", status)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestWithJitterUsesMinWhenBoundsAreEqual(t *testing.T) {
	scenario := Apply(func(context.Context) (int, error) {
		return http.StatusOK, nil
	}, WithJitter(20*time.Millisecond, 20*time.Millisecond, 1.0))

	startedAt := time.Now()
	status, err := scenario(context.Background())
	elapsed := time.Since(startedAt)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}
	if elapsed < 20*time.Millisecond {
		t.Fatalf("expected elapsed >= 20ms, got %v", elapsed)
	}
}

func TestWithTimeoutCancelsSlowScenario(t *testing.T) {
	scenario := Apply(func(ctx context.Context) (int, error) {
		select {
		case <-time.After(500 * time.Millisecond):
			return http.StatusOK, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}, WithTimeout(50*time.Millisecond))

	startedAt := time.Now()
	status, err := scenario(context.Background())
	elapsed := time.Since(startedAt)

	if status != 0 {
		t.Fatalf("expected status 0, got %d", status)
	}
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected deadline-related error, got %v", err)
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("expected elapsed < 200ms, got %v", elapsed)
	}
}

func TestWithTimeoutDoesNotAffectFastScenario(t *testing.T) {
	scenario := Apply(func(context.Context) (int, error) {
		return http.StatusOK, nil
	}, WithTimeout(500*time.Millisecond))

	status, err := scenario(context.Background())

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}
}

func TestWithStatusCodeReturnsInjectedCodeWhenRateIsOne(t *testing.T) {
	called := false

	scenario := Apply(func(context.Context) (int, error) {
		called = true
		return http.StatusOK, nil
	}, WithStatusCode(http.StatusServiceUnavailable, 1.0))

	status, err := scenario(context.Background())

	if status != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", status)
	}
	if !errors.Is(err, ErrInjected) {
		t.Fatalf("expected ErrInjected, got %v", err)
	}
	if called {
		t.Fatal("expected underlying scenario not to be called")
	}
}

func TestWithStatusCodeNeverActsWhenRateIsZero(t *testing.T) {
	scenario := Apply(func(context.Context) (int, error) {
		return http.StatusOK, nil
	}, WithStatusCode(http.StatusServiceUnavailable, 0.0))

	for range 50 {
		status, err := scenario(context.Background())
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if status == http.StatusServiceUnavailable {
			t.Fatal("expected injected status code never to be returned")
		}
	}
}

func TestWithStatusCodeAndWithJitterComposeWithRunT(t *testing.T) {
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
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
		},
		Scenario: Chain(
			WithStatusCode(http.StatusTooManyRequests, 0.5),
			WithJitter(5*time.Millisecond, 10*time.Millisecond, 1.0),
		)(baseScenario),
	}

	result := RunT(t, test)
	if result.Total <= 0 {
		t.Fatalf("expected Total > 0, got %d", result.Total)
	}
}
