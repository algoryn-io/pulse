package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	if exitCode(err) != 1 {
		t.Fatalf("exitCode(usage) = %d, want 1", exitCode(err))
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
			RPS:      5,
			Latency: pulse.LatencyStats{
				Min:  10 * time.Millisecond,
				Max:  40 * time.Millisecond,
				Mean: 25 * time.Millisecond,
				P50:  20 * time.Millisecond,
				P95:  35 * time.Millisecond,
				P99:  38 * time.Millisecond,
			},
			StatusCounts: map[int]int64{200: 10, 404: 2},
			ErrorCounts:  map[string]int64{"http_status_error": 3, "unknown_error": 1},
			ThresholdOutcomes: []pulse.ThresholdOutcome{
				{Pass: true, Description: "error_rate < 0.05"},
				{Pass: true, Description: "mean_latency < 200ms"},
			},
		}, nil
	}

	var stdout bytes.Buffer
	err := run([]string{"run"}, &stdout)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if exitCode(err) != 0 {
		t.Fatalf("exitCode(success) = %d, want 0", exitCode(err))
	}

	want := "" +
		"Total requests: 15\n" +
		"Failed requests: 2\n" +
		"Duration: 3s\n" +
		"RPS: 5.00\n" +
		"Min latency: 10ms\n" +
		"P50 latency: 20ms\n" +
		"Mean latency: 25ms\n" +
		"P95 latency: 35ms\n" +
		"P99 latency: 38ms\n" +
		"Max latency: 40ms\n" +
		"\n" +
		"Status codes:\n" +
		"  200: 10\n" +
		"  404: 2\n" +
		"\n" +
		"Errors:\n" +
		"  http_status_error: 3\n" +
		"  unknown_error: 1\n" +
		"\n" +
		"Thresholds:\n" +
		"  PASS error_rate < 0.05\n" +
		"  PASS mean_latency < 200ms\n"

	if stdout.String() != want {
		t.Fatalf("expected output %q, got %q", want, stdout.String())
	}
}

func TestRunReturnsThresholdEvaluationError(t *testing.T) {
	previousExecute := execute
	t.Cleanup(func() {
		execute = previousExecute
	})

	threshErr := &pulse.ThresholdViolationError{
		Description: "mean_latency < 200ms",
		Actual:      250 * time.Millisecond,
		Limit:       200 * time.Millisecond,
	}
	execute = func([]string) (pulse.Result, error) {
		return pulse.Result{
			Total: 10,
			ThresholdOutcomes: []pulse.ThresholdOutcome{
				{Pass: false, Description: "mean_latency < 200ms"},
			},
		}, threshErr
	}

	var stdout bytes.Buffer
	err := run([]string{"run"}, &stdout)
	var tv *pulse.ThresholdViolationError
	if !errors.As(err, &tv) {
		t.Fatalf("expected *ThresholdViolationError, got %v", err)
	}
	if tv.Description != threshErr.Description {
		t.Fatalf("description: got %q, want %q", tv.Description, threshErr.Description)
	}
	if exitCode(err) != 2 {
		t.Fatalf("expected exit code 2, got %d", exitCode(err))
	}
	if !strings.Contains(stdout.String(), "FAIL mean_latency") {
		t.Fatalf("expected threshold FAIL in stdout, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Thresholds failed. See results above.\n") {
		t.Fatalf("expected threshold summary in stdout, got %q", stdout.String())
	}
}

func TestExitCode(t *testing.T) {
	if got := exitCode(nil); got != 0 {
		t.Fatalf("exitCode(nil) = %d, want 0", got)
	}
	if got := exitCode(&pulse.ThresholdViolationError{
		Description: "mean_latency < 1ms",
		Actual:      time.Second,
		Limit:       time.Millisecond,
	}); got != 2 {
		t.Fatalf("threshold violation = %d, want 2", got)
	}
	if got := exitCode(errors.New("pulse: threshold error rate must not be negative")); got != 1 {
		t.Fatalf("validation error = %d, want 1", got)
	}
	if got := exitCode(errors.New("config: no such file")); got != 1 {
		t.Fatalf("config error = %d, want 1", got)
	}
	joined := errors.Join(
		&pulse.ThresholdViolationError{Description: "error_rate < 0.1", Actual: 0.5, Limit: 0.1},
		&pulse.ThresholdViolationError{Description: "mean_latency < 1ms", Actual: time.Second, Limit: time.Millisecond},
	)
	if got := exitCode(joined); got != 2 {
		t.Fatalf("joined threshold errors = %d, want 2", got)
	}
	mixed := errors.Join(errors.New("scheduler: failed"), &pulse.ThresholdViolationError{
		Description: "error_rate < 0.1",
		Actual:      0.5,
		Limit:       0.1,
	})
	if got := exitCode(mixed); got != 1 {
		t.Fatalf("mixed errors = %d, want 1", got)
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
			RPS:      4,
			Latency: pulse.LatencyStats{
				Min:  100 * time.Millisecond,
				Max:  300 * time.Millisecond,
				Mean: 200 * time.Millisecond,
				P50:  150 * time.Millisecond,
				P95:  280 * time.Millisecond,
				P99:  300 * time.Millisecond,
			},
			StatusCounts: map[int]int64{500: 1},
			ErrorCounts:  map[string]int64{"context_canceled": 1},
		}, wantErr
	}

	var stdout bytes.Buffer
	err := run([]string{"run"}, &stdout)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
	if exitCode(err) != 1 {
		t.Fatalf("exitCode(execution err) = %d, want 1", exitCode(err))
	}

	want := "" +
		"Total requests: 4\n" +
		"Failed requests: 1\n" +
		"Duration: 1s\n" +
		"RPS: 4.00\n" +
		"Min latency: 100ms\n" +
		"P50 latency: 150ms\n" +
		"Mean latency: 200ms\n" +
		"P95 latency: 280ms\n" +
		"P99 latency: 300ms\n" +
		"Max latency: 300ms\n" +
		"\n" +
		"Status codes:\n" +
		"  500: 1\n" +
		"\n" +
		"Errors:\n" +
		"  context_canceled: 1\n"

	if stdout.String() != want {
		t.Fatalf("expected output %q, got %q", want, stdout.String())
	}
}

func TestRunPrintsJSON(t *testing.T) {
	previousExecute := execute
	t.Cleanup(func() {
		execute = previousExecute
	})

	execute = func([]string) (pulse.Result, error) {
		return pulse.Result{
			Total:    3,
			Failed:   1,
			Duration: 2 * time.Second,
			Latency: pulse.LatencyStats{
				Min:  10 * time.Millisecond,
				Max:  30 * time.Millisecond,
				Mean: 20 * time.Millisecond,
				P50:  18 * time.Millisecond,
				P95:  28 * time.Millisecond,
				P99:  30 * time.Millisecond,
			},
		}, nil
	}

	var stdout bytes.Buffer
	if err := run([]string{"run", "--json"}, &stdout); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var got pulse.Result
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("expected valid json, got %v", err)
	}

	if got.Total != 3 || got.Failed != 1 {
		t.Fatalf("expected result totals to match, got %+v", got)
	}
	if got.Latency.P50 != 18*time.Millisecond || got.Latency.P95 != 28*time.Millisecond || got.Latency.P99 != 30*time.Millisecond {
		t.Fatalf("expected percentiles in JSON, got %+v", got.Latency)
	}
}

func TestRunWritesJSONToFile(t *testing.T) {
	previousExecute := execute
	t.Cleanup(func() {
		execute = previousExecute
	})

	execute = func([]string) (pulse.Result, error) {
		return pulse.Result{
			Total:    8,
			Failed:   2,
			Duration: time.Second,
			RPS:      8,
			Latency: pulse.LatencyStats{
				Min:  5 * time.Millisecond,
				Max:  25 * time.Millisecond,
				Mean: 15 * time.Millisecond,
				P50:  14 * time.Millisecond,
				P95:  24 * time.Millisecond,
				P99:  25 * time.Millisecond,
			},
			StatusCounts: map[int]int64{201: 8},
			ErrorCounts:  map[string]int64{"deadline_exceeded": 2},
		}, nil
	}

	dir := t.TempDir()
	outputPath := filepath.Join(dir, "result.json")

	var stdout bytes.Buffer
	if err := run([]string{"run", "--out", outputPath}, &stdout); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("expected no error reading output file, got %v", err)
	}

	var got pulse.Result
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("expected valid json file, got %v", err)
	}

	if got.Total != 8 || got.Failed != 2 {
		t.Fatalf("expected result totals to match, got %+v", got)
	}

	wantStdout := "" +
		"Total requests: 8\n" +
		"Failed requests: 2\n" +
		"Duration: 1s\n" +
		"RPS: 8.00\n" +
		"Min latency: 5ms\n" +
		"P50 latency: 14ms\n" +
		"Mean latency: 15ms\n" +
		"P95 latency: 24ms\n" +
		"P99 latency: 25ms\n" +
		"Max latency: 25ms\n" +
		"\n" +
		"Status codes:\n" +
		"  201: 8\n" +
		"\n" +
		"Errors:\n" +
		"  deadline_exceeded: 2\n"

	if stdout.String() != wantStdout {
		t.Fatalf("expected output %q, got %q", wantStdout, stdout.String())
	}
}

func TestRunCLISuppressesThresholdOnlyErrorOnStderr(t *testing.T) {
	previousExecute := execute
	t.Cleanup(func() {
		execute = previousExecute
	})

	execute = func([]string) (pulse.Result, error) {
		return pulse.Result{
			Total: 1,
			Failed: 1,
			ThresholdOutcomes: []pulse.ThresholdOutcome{
				{Pass: false, Description: "error_rate < 0.1"},
			},
		}, &pulse.ThresholdViolationError{
			Description: "error_rate < 0.1",
			Actual:      1.0,
			Limit:       0.1,
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runCLI([]string{"run"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("runCLI() code = %d, want 2", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Thresholds failed. See results above.") {
		t.Fatalf("expected threshold summary in stdout, got %q", stdout.String())
	}
}

func TestRunCLIPrintsRealErrorsToStderr(t *testing.T) {
	previousExecute := execute
	t.Cleanup(func() {
		execute = previousExecute
	})

	wantErr := errors.New("scheduler: failed")
	execute = func([]string) (pulse.Result, error) {
		return pulse.Result{}, wantErr
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runCLI([]string{"run"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runCLI() code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "Total requests: 0\n") {
		t.Fatalf("expected result summary in stdout, got %q", stdout.String())
	}
	if stderr.String() != "scheduler: failed\n" {
		t.Fatalf("stderr = %q, want %q", stderr.String(), "scheduler: failed\n")
	}
}
