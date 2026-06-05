package pulse

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestMiddlewaresRejectInvalidParameters(t *testing.T) {
	tests := []struct {
		name string
		call func()
	}{
		{name: "negative latency", call: func() { WithLatency(-time.Second, 1) }},
		{name: "rate above one", call: func() { WithErrorRate(1.1) }},
		{name: "inverted jitter", call: func() { WithJitter(time.Second, time.Millisecond, 1) }},
		{name: "non-positive timeout", call: func() { WithTimeout(0) }},
		{name: "negative retries", call: func() { WithRetry(-1, 0) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			tt.call()
		})
	}
}

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

func TestSeedMakesRandomMiddlewaresDeterministic(t *testing.T) {
	buildSequence := func() []bool {
		SetSeed(123)
		scenario := Apply(func(context.Context) (int, error) {
			return http.StatusOK, nil
		}, WithErrorRate(0.5), WithJitter(time.Millisecond, 3*time.Millisecond, 0.5))

		out := make([]bool, 0, 8)
		for range 8 {
			_, err := scenario(context.Background())
			out = append(out, errors.Is(err, ErrInjected))
		}
		return out
	}

	got1 := buildSequence()
	got2 := buildSequence()

	if !reflect.DeepEqual(got1, got2) {
		t.Fatalf("expected deterministic sequence, got %v and %v", got1, got2)
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
	baseScenario := newHealthyHTTPScenario(t)

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
	baseScenario := newHealthyHTTPScenario(t)

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

func TestWithRetryRetriesUntilSuccess(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)

	scenario := Apply(func(context.Context) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls <= 2 {
			return 0, errors.New("temporary failure")
		}
		return http.StatusOK, nil
	}, WithRetry(3, time.Millisecond))

	status, err := scenario(context.Background())

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestWithRetryReturnsLastErrorWhenAllAttemptsFail(t *testing.T) {
	var (
		mu      sync.Mutex
		calls   int
		wantErr = errors.New("always fails")
	)

	scenario := Apply(func(context.Context) (int, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return 0, wantErr
	}, WithRetry(2, time.Millisecond))

	status, err := scenario(context.Background())

	if status != 0 {
		t.Fatalf("expected status 0, got %d", status)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestWithRetryRespectsContextCancellationDuringBackoff(t *testing.T) {
	scenario := Apply(func(context.Context) (int, error) {
		return 0, errors.New("always fails")
	}, WithRetry(5, 500*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)

	startedAt := time.Now()
	status, err := scenario(ctx)
	elapsed := time.Since(startedAt)

	if status != 0 {
		t.Fatalf("expected status 0, got %d", status)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("expected cancellation before full backoff, got %v", elapsed)
	}
}

func TestWithRetryZeroDoesNotRetry(t *testing.T) {
	calls := 0

	scenario := Apply(func(context.Context) (int, error) {
		calls++
		return 0, errors.New("always fails")
	}, WithRetry(0, time.Millisecond))

	_, _ = scenario(context.Background())

	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestWithBulkheadAllowsUpToMaxConcurrent(t *testing.T) {
	entered := make(chan struct{}, 5)
	release := make(chan struct{})

	scenario := Apply(func(context.Context) (int, error) {
		entered <- struct{}{}
		<-release
		return http.StatusOK, nil
	}, WithBulkhead(2))

	type result struct {
		status int
		err    error
	}

	results := make(chan result, 5)

	for range 2 {
		go func() {
			status, err := scenario(context.Background())
			results <- result{status: status, err: err}
		}()
	}

	for range 2 {
		select {
		case <-entered:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("expected first two goroutines to enter immediately")
		}
	}

	waiterCancels := make([]context.CancelFunc, 3)
	for i := range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
		waiterCancels[i] = cancel
		go func(i int) {
			status, err := scenario(ctx)
			results <- result{status: status, err: err}
		}(i)
	}
	defer func() {
		for _, cancel := range waiterCancels {
			if cancel != nil {
				cancel()
			}
		}
	}()

	select {
	case <-entered:
		t.Fatal("expected no third goroutine to enter while bulkhead is full")
	case <-time.After(40 * time.Millisecond):
	}

	close(release)

	var successCount, bulkheadFullCount int
	for range 5 {
		r := <-results
		if errors.Is(r.err, ErrBulkheadFull) {
			bulkheadFullCount++
			continue
		}
		if r.err != nil {
			t.Fatalf("unexpected error: %v", r.err)
		}
		if r.status != http.StatusOK {
			t.Fatalf("expected status 200, got %d", r.status)
		}
		successCount++
	}

	if successCount != 2 {
		t.Fatalf("expected 2 successful executions, got %d", successCount)
	}
	if bulkheadFullCount != 3 {
		t.Fatalf("expected 3 ErrBulkheadFull results, got %d", bulkheadFullCount)
	}
}

func TestWithBulkheadReturnsErrBulkheadFullWhenContextExpires(t *testing.T) {
	started := make(chan struct{}, 1)

	scenario := Apply(func(ctx context.Context) (int, error) {
		started <- struct{}{}
		select {
		case <-time.After(100 * time.Millisecond):
			return http.StatusOK, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}, WithBulkhead(1))

	firstDone := make(chan struct{})
	go func() {
		_, _ = scenario(context.Background())
		close(firstDone)
	}()

	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected first goroutine to acquire bulkhead slot")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	status, err := scenario(ctx)

	if status != 0 {
		t.Fatalf("expected status 0, got %d", status)
	}
	if !errors.Is(err, ErrBulkheadFull) {
		t.Fatalf("expected ErrBulkheadFull, got %v", err)
	}

	<-firstDone
}

func TestWithBulkheadZeroUsesOne(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	scenario := Apply(func(context.Context) (int, error) {
		started <- struct{}{}
		<-release
		return http.StatusOK, nil
	}, WithBulkhead(0))

	firstDone := make(chan struct{})
	go func() {
		_, _ = scenario(context.Background())
		close(firstDone)
	}()

	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected first goroutine to acquire slot")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	status, err := scenario(ctx)
	if status != 0 {
		t.Fatalf("expected status 0, got %d", status)
	}
	if !errors.Is(err, ErrBulkheadFull) {
		t.Fatalf("expected ErrBulkheadFull, got %v", err)
	}

	close(release)
	<-firstDone
}

func TestWithRetryAndWithBulkheadComposeWithRunT(t *testing.T) {
	baseScenario := newHealthyHTTPScenario(t)

	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 4,
		},
		Scenario: Apply(baseScenario, WithBulkhead(2), WithRetry(1, time.Millisecond)),
	}

	result := RunT(t, test)
	if result.Total <= 0 {
		t.Fatalf("expected Total > 0, got %d", result.Total)
	}
}
