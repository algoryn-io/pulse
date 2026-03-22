package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmgo38/Pulse/model"
	"github.com/jmgo38/Pulse/scheduler"
)

func TestEngineRunExecutesScenarioAcrossPhases(t *testing.T) {
	calls := 0
	engine := New([]scheduler.Phase{
		{Type: model.PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 50},
		{Type: model.PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 50},
	}, func(context.Context) (int, error) {
		calls++
		return 0, nil
	}, 4)

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if calls < 2 {
		t.Fatalf("expected scenario to run multiple times, got %d", calls)
	}

	if result.Total != int64(calls) {
		t.Fatalf("expected total %d, got %d", calls, result.Total)
	}

	if result.Failed != 0 {
		t.Fatalf("expected 0 failures, got %d", result.Failed)
	}

	if result.Duration <= 0 {
		t.Fatalf("expected positive duration, got %v", result.Duration)
	}
}

func TestEngineRunRecordsScenarioErrorsWithoutAborting(t *testing.T) {
	wantErr := errors.New("scenario failed")
	engine := New([]scheduler.Phase{
		{Type: model.PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 50},
	}, func(context.Context) (int, error) {
		return 0, wantErr
	}, 4)

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("expected nil engine error, got %v", err)
	}

	if result.Total < 2 {
		t.Fatalf("expected run to continue after scenario errors, total %d", result.Total)
	}

	if result.Failed != result.Total {
		t.Fatalf("expected all executions failed, total %d failed %d", result.Total, result.Failed)
	}

	if result.ErrorCounts["unknown_error"] != result.Total {
		t.Fatalf("expected unknown_error counts to match failures, got %+v total %d", result.ErrorCounts, result.Total)
	}
}

func TestEngineRunRunsAllPhasesWhenScenarioAlwaysFails(t *testing.T) {
	var calls atomic.Int32
	wantErr := errors.New("fail")
	engine := New([]scheduler.Phase{
		{Type: model.PhaseTypeConstant, Duration: 35 * time.Millisecond, ArrivalRate: 40},
		{Type: model.PhaseTypeConstant, Duration: 35 * time.Millisecond, ArrivalRate: 40},
	}, func(context.Context) (int, error) {
		calls.Add(1)
		return 0, wantErr
	}, 4)

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if calls.Load() < 2 {
		t.Fatalf("expected multiple invocations across phases, got %d", calls.Load())
	}

	if int64(calls.Load()) != result.Total {
		t.Fatalf("calls %d vs total %d", calls.Load(), result.Total)
	}

	if result.Failed != result.Total {
		t.Fatalf("expected all failed, total %d failed %d", result.Total, result.Failed)
	}
}

func TestEngineRunPropagatesUnsupportedPhaseType(t *testing.T) {
	engine := New([]scheduler.Phase{
		{Type: model.PhaseType("unsupported"), Duration: time.Second, ArrivalRate: 1},
	}, func(context.Context) (int, error) {
		return 0, nil
	}, 1)

	_, err := engine.Run(context.Background())
	if !errors.Is(err, scheduler.ErrUnsupportedPhaseType) {
		t.Fatalf("expected %v, got %v", scheduler.ErrUnsupportedPhaseType, err)
	}
}

func TestEngineRunLimitsConcurrency(t *testing.T) {
	var running int32
	var maxRunning int32

	engine := New([]scheduler.Phase{
		{Type: model.PhaseTypeConstant, Duration: 60 * time.Millisecond, ArrivalRate: 100},
	}, func(context.Context) (int, error) {
		current := atomic.AddInt32(&running, 1)
		defer atomic.AddInt32(&running, -1)

		for {
			recorded := atomic.LoadInt32(&maxRunning)
			if current <= recorded || atomic.CompareAndSwapInt32(&maxRunning, recorded, current) {
				break
			}
		}

		time.Sleep(20 * time.Millisecond)
		return 0, nil
	}, 2)

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result.Total == 0 {
		t.Fatal("expected executions to run")
	}

	if got := atomic.LoadInt32(&maxRunning); got > 2 {
		t.Fatalf("expected max concurrency 2, got %d", got)
	}
}

func TestEngineRunWaitsForRunningExecutions(t *testing.T) {
	var completed atomic.Int32
	started := make(chan struct{}, 2)
	release := make(chan struct{})

	engine := New([]scheduler.Phase{
		{Type: model.PhaseTypeConstant, Duration: 25 * time.Millisecond, ArrivalRate: 100},
	}, func(context.Context) (int, error) {
		started <- struct{}{}
		<-release
		completed.Add(1)
		return 0, nil
	}, 2)

	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		_, runErr = engine.Run(context.Background())
	}()

	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for executions to start")
		}
	}

	select {
	case <-done:
		t.Fatal("expected engine to wait for running executions")
	case <-time.After(40 * time.Millisecond):
	}

	close(release)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for engine to finish")
	}

	if runErr != nil {
		t.Fatalf("expected no error, got %v", runErr)
	}

	if got := completed.Load(); got != 2 {
		t.Fatalf("expected 2 completed executions, got %d", got)
	}
}

func TestEngineRunRespectsContextCancellationWhileAcquiring(t *testing.T) {
	blocked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	engine := New([]scheduler.Phase{
		{Type: model.PhaseTypeConstant, Duration: 50 * time.Millisecond, ArrivalRate: 100},
	}, func(ctx context.Context) (int, error) {
		once.Do(func() {
			close(blocked)
		})

		select {
		case <-release:
			return 0, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		_, runErr = engine.Run(ctx)
	}()

	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first execution to block")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for engine cancellation")
	}

	close(release)

	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("expected %v, got %v", context.Canceled, runErr)
	}
}
