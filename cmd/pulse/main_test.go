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
			Latency: pulse.LatencyStats{
				Min:  5 * time.Millisecond,
				Max:  25 * time.Millisecond,
				Mean: 15 * time.Millisecond,
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
		"Min latency: 5ms\n" +
		"Max latency: 25ms\n" +
		"Mean latency: 15ms\n"

	if stdout.String() != wantStdout {
		t.Fatalf("expected output %q, got %q", wantStdout, stdout.String())
	}
}
