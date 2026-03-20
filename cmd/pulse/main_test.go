package main

import (
	"bytes"
	"errors"
	"testing"
	"time"

	pulse "github.com/jmgo38/Pulse"
)

func TestRunReturnsUsageForInvalidArgs(t *testing.T) {
	var stdout bytes.Buffer

	err := run(nil, &stdout)
	if err != errUsage {
		t.Fatalf("expected %v, got %v", errUsage, err)
	}
}

func TestRunPrintsResults(t *testing.T) {
	previousExecute := execute
	t.Cleanup(func() {
		execute = previousExecute
	})

	execute = func([]string) (pulse.Result, error) {
		return pulse.Result{
			Total:    15,
			Failed:   2,
			Duration: 3 * time.Second,
			Latency: pulse.LatencyStats{
				Min:  10 * time.Millisecond,
				Max:  40 * time.Millisecond,
				Mean: 25 * time.Millisecond,
			},
		}, nil
	}

	var stdout bytes.Buffer
	if err := run([]string{"run"}, &stdout); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	want := "" +
		"Total requests: 15\n" +
		"Failed requests: 2\n" +
		"Duration: 3s\n" +
		"Min latency: 10ms\n" +
		"Max latency: 40ms\n" +
		"Mean latency: 25ms\n"

	if stdout.String() != want {
		t.Fatalf("expected output %q, got %q", want, stdout.String())
	}
}

func TestRunPrintsResultsWhenExecutionFails(t *testing.T) {
	previousExecute := execute
	t.Cleanup(func() {
		execute = previousExecute
	})

	wantErr := errors.New("request failed")
	execute = func([]string) (pulse.Result, error) {
		return pulse.Result{
			Total:    4,
			Failed:   1,
			Duration: time.Second,
			Latency: pulse.LatencyStats{
				Min:  100 * time.Millisecond,
				Max:  300 * time.Millisecond,
				Mean: 200 * time.Millisecond,
			},
		}, wantErr
	}

	var stdout bytes.Buffer
	err := run([]string{"run"}, &stdout)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}

	want := "" +
		"Total requests: 4\n" +
		"Failed requests: 1\n" +
		"Duration: 1s\n" +
		"Min latency: 100ms\n" +
		"Max latency: 300ms\n" +
		"Mean latency: 200ms\n"

	if stdout.String() != want {
		t.Fatalf("expected output %q, got %q", want, stdout.String())
	}
}