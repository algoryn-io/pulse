package config

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	pulse "github.com/jmgo38/Pulse"
)

type stubHTTPClient struct {
	getURL   string
	postURL  string
	postBody string
}

func (c *stubHTTPClient) Get(_ context.Context, url string) error {
	c.getURL = url
	return nil
}

func (c *stubHTTPClient) Post(_ context.Context, url string, body io.Reader) error {
	c.postURL = url
	payload, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	c.postBody = string(payload)
	return nil
}

func TestLoadMapsYAMLToPulseTest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "" +
		"phases:\n" +
		"  - type:  CoNsTaNt  \n" +
		"    duration: 3s\n" +
		"    arrivalRate: 5\n" +
		"target:\n" +
		"  method: GET\n" +
		"  url: https://httpbin.org/get\n" +
		"maxConcurrency: 5\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	test, err := Load(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(test.Config.Phases) != 1 {
		t.Fatalf("expected 1 phase, got %d", len(test.Config.Phases))
	}

	if test.Config.MaxConcurrency != 5 {
		t.Fatalf("expected max concurrency 5, got %d", test.Config.MaxConcurrency)
	}

	if test.Config.Thresholds.ErrorRate != 0 {
		t.Fatalf("expected zero error rate threshold, got %v", test.Config.Thresholds.ErrorRate)
	}

	phase := test.Config.Phases[0]
	if phase.Type != pulse.PhaseTypeConstant {
		t.Fatalf("expected constant phase, got %s", phase.Type)
	}

	if phase.Duration != 3*time.Second {
		t.Fatalf("expected 3s, got %v", phase.Duration)
	}

	if phase.ArrivalRate != 5 {
		t.Fatalf("expected arrival rate 5, got %d", phase.ArrivalRate)
	}

	if test.Scenario == nil {
		t.Fatal("expected scenario to be configured")
	}
}

func TestLoadMapsThresholds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "" +
		"phases:\n" +
		"  - type: constant\n" +
		"    duration: 3s\n" +
		"    arrivalRate: 5\n" +
		"target:\n" +
		"  method: GET\n" +
		"  url: https://httpbin.org/get\n" +
		"thresholds:\n" +
		"  errorRate: 0.05\n" +
		"  maxMeanLatency: 200ms\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	test, err := Load(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if test.Config.Thresholds.ErrorRate != 0.05 {
		t.Fatalf("expected error rate 0.05, got %v", test.Config.Thresholds.ErrorRate)
	}

	if test.Config.Thresholds.MaxMeanLatency != 200*time.Millisecond {
		t.Fatalf("expected mean latency 200ms, got %v", test.Config.Thresholds.MaxMeanLatency)
	}
}

func TestLoadBuildsGETScenario(t *testing.T) {
	previousNewHTTPClient := newHTTPClient
	client := &stubHTTPClient{}
	newHTTPClient = func() httpClient {
		return client
	}
	t.Cleanup(func() {
		newHTTPClient = previousNewHTTPClient
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "" +
		"phases:\n" +
		"  - type: constant\n" +
		"    duration: 1s\n" +
		"    arrivalRate: 1\n" +
		"target:\n" +
		"  method: GET\n" +
		"  url: https://pulse.test/get\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	test, err := Load(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := test.Scenario(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if client.getURL != "https://pulse.test/get" {
		t.Fatalf("expected GET url %q, got %q", "https://pulse.test/get", client.getURL)
	}
}

func TestLoadBuildsPOSTScenario(t *testing.T) {
	previousNewHTTPClient := newHTTPClient
	client := &stubHTTPClient{}
	newHTTPClient = func() httpClient {
		return client
	}
	t.Cleanup(func() {
		newHTTPClient = previousNewHTTPClient
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "" +
		"phases:\n" +
		"  - type: constant\n" +
		"    duration: 1s\n" +
		"    arrivalRate: 1\n" +
		"target:\n" +
		"  method: POST\n" +
		"  url: https://pulse.test/post\n" +
		"  body: hello\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	test, err := Load(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := test.Scenario(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if client.postURL != "https://pulse.test/post" {
		t.Fatalf("expected POST url %q, got %q", "https://pulse.test/post", client.postURL)
	}

	if client.postBody != "hello" {
		t.Fatalf("expected POST body %q, got %q", "hello", client.postBody)
	}
}

func TestLoadValidatesRequiredFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "target:\n  method: GET\n  url: https://httpbin.org/get\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, err := Load(path)
	if err != errNoPhases {
		t.Fatalf("expected %v, got %v", errNoPhases, err)
	}
}

func TestLoadRejectsNonPositivePhaseDuration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "" +
		"phases:\n" +
		"  - type: constant\n" +
		"    duration: 0s\n" +
		"    arrivalRate: 1\n" +
		"target:\n" +
		"  method: GET\n" +
		"  url: https://httpbin.org/get\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, err := Load(path)
	if err != errNonPositivePhase {
		t.Fatalf("expected %v, got %v", errNonPositivePhase, err)
	}
}

func TestLoadRejectsNonPositiveArrivalRate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "" +
		"phases:\n" +
		"  - type: constant\n" +
		"    duration: 1s\n" +
		"    arrivalRate: 0\n" +
		"target:\n" +
		"  method: GET\n" +
		"  url: https://httpbin.org/get\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, err := Load(path)
	if err != errNonPositiveRate {
		t.Fatalf("expected %v, got %v", errNonPositiveRate, err)
	}
}
