package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
			RPS:      5,
			Latency: pulse.LatencyStats{
				Min:  10 * time.Millisecond,
				Max:  40 * time.Millisecond,
				Mean: 25 * time.Millisecond,
				P50:  20 * time.Millisecond,
				P95:  35 * time.Millisecond,
				P99:  38 * time.Millisecond,
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
		"RPS: 5.00\n" +
		"Min latency: 10ms\n" +
		"P50 latency: 20ms\n" +
		"Mean latency: 25ms\n" +
		"P95 latency: 35ms\n" +
		"P99 latency: 38ms\n" +
		"Max latency: 40ms\n"

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
			RPS:      4,
			Latency: pulse.LatencyStats{
				Min:  100 * time.Millisecond,
				Max:  300 * time.Millisecond,
				Mean: 200 * time.Millisecond,
				P50:  150 * time.Millisecond,
				P95:  280 * time.Millisecond,
				P99:  300 * time.Millisecond,
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
		"RPS: 4.00\n" +
		"Min latency: 100ms\n" +
		"P50 latency: 150ms\n" +
		"Mean latency: 200ms\n" +
		"P95 latency: 280ms\n" +
		"P99 latency: 300ms\n" +
		"Max latency: 300ms\n"

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
		"Max latency: 25ms\n"

	if stdout.String() != wantStdout {
		t.Fatalf("expected output %q, got %q", wantStdout, stdout.String())
	}
}
