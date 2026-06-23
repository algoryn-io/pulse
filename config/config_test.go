package config

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	pulse "algoryn.io/pulse"
	"algoryn.io/pulse/transport"
)

type stubHTTPClient struct {
	getURL   string
	postURL  string
	postBody string
	doMethod string
	doURL    string
	doBody   string
}

func (c *stubHTTPClient) Get(_ context.Context, url string) (int, error) {
	c.getURL = url
	return 200, nil
}

func (c *stubHTTPClient) Post(_ context.Context, url string, body io.Reader) (int, error) {
	c.postURL = url
	payload, err := io.ReadAll(body)
	if err != nil {
		return 0, err
	}

	c.postBody = string(payload)
	return 201, nil
}

func (c *stubHTTPClient) Do(_ context.Context, method, url string, body io.Reader) (int, error) {
	c.doMethod = method
	c.doURL = url
	if body != nil {
		payload, err := io.ReadAll(body)
		if err != nil {
			return 0, err
		}
		c.doBody = string(payload)
	}
	return 202, nil
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
	if test.Config.Seed != nil {
		t.Fatalf("expected nil seed, got %v", *test.Config.Seed)
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
		"seed: 42\n" +
		"saturationPolicy: block\n" +
		"reporting:\n" +
		"  interval: 1s\n" +
		"thresholds:\n" +
		"  errorRate: 0.05\n" +
		"  maxMeanLatency: 200ms\n" +
		"  maxP95Latency: 300ms\n" +
		"  maxP99Latency: 500ms\n" +
		"  maxDroppedRate: 0.1\n"

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

	if test.Config.Thresholds.MaxP95Latency != 300*time.Millisecond {
		t.Fatalf("expected p95 latency 300ms, got %v", test.Config.Thresholds.MaxP95Latency)
	}

	if test.Config.Thresholds.MaxP99Latency != 500*time.Millisecond {
		t.Fatalf("expected p99 latency 500ms, got %v", test.Config.Thresholds.MaxP99Latency)
	}

	if test.Config.Thresholds.MaxDroppedRate != 0.1 {
		t.Fatalf("expected max dropped rate 0.1, got %v", test.Config.Thresholds.MaxDroppedRate)
	}

	if test.Config.SaturationPolicy != pulse.SaturationPolicyBlock {
		t.Fatalf("expected block saturation policy, got %q", test.Config.SaturationPolicy)
	}

	if test.Config.Reporting.Interval != time.Second {
		t.Fatalf("expected reporting interval 1s, got %v", test.Config.Reporting.Interval)
	}
	if test.Config.Seed == nil || *test.Config.Seed != 42 {
		t.Fatalf("expected seed 42, got %v", test.Config.Seed)
	}
}

func TestLoadMapsAbort(t *testing.T) {
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
		"reporting:\n" +
		"  interval: 1s\n" +
		"abort:\n" +
		"  maxErrorRate: 0.25\n" +
		"  maxP99: 750ms\n" +
		"  minRequests: 100\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	test, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if test.Config.Abort.MaxErrorRate != 0.25 {
		t.Errorf("abort MaxErrorRate = %v, want 0.25", test.Config.Abort.MaxErrorRate)
	}
	if test.Config.Abort.MaxP99 != 750*time.Millisecond {
		t.Errorf("abort MaxP99 = %v, want 750ms", test.Config.Abort.MaxP99)
	}
	if test.Config.Abort.MinRequests != 100 {
		t.Errorf("abort MinRequests = %v, want 100", test.Config.Abort.MinRequests)
	}
}

func TestLoadBuildsGETScenario(t *testing.T) {
	previousNewHTTPClient := newHTTPClient
	client := &stubHTTPClient{}
	newHTTPClient = func(_ fileConfig) httpClient {
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

	if _, err := test.Scenario(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if client.getURL != "https://pulse.test/get" {
		t.Fatalf("expected GET url %q, got %q", "https://pulse.test/get", client.getURL)
	}
}

func TestLoadBuildsPOSTScenario(t *testing.T) {
	previousNewHTTPClient := newHTTPClient
	client := &stubHTTPClient{}
	newHTTPClient = func(_ fileConfig) httpClient {
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

	if _, err := test.Scenario(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if client.postURL != "https://pulse.test/post" {
		t.Fatalf("expected POST url %q, got %q", "https://pulse.test/post", client.postURL)
	}

	if client.postBody != "hello" {
		t.Fatalf("expected POST body %q, got %q", "hello", client.postBody)
	}
}

func TestLoadBuildsPUTScenarioWithDo(t *testing.T) {
	previousNewHTTPClient := newHTTPClient
	client := &stubHTTPClient{}
	newHTTPClient = func(_ fileConfig) httpClient {
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
		"  method: PUT\n" +
		"  url: https://pulse.test/resource/1\n" +
		"  body: updated\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	test, err := Load(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, err := test.Scenario(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if client.doMethod != http.MethodPut {
		t.Fatalf("expected method %q, got %q", http.MethodPut, client.doMethod)
	}
	if client.doURL != "https://pulse.test/resource/1" {
		t.Fatalf("expected PUT url %q, got %q", "https://pulse.test/resource/1", client.doURL)
	}
	if client.doBody != "updated" {
		t.Fatalf("expected PUT body %q, got %q", "updated", client.doBody)
	}
}

func TestLoadMapsSpikePhaseYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "" +
		"phases:\n" +
		"  - type: spike\n" +
		"    duration: 10s\n" +
		"    from: 10\n" +
		"    to: 100\n" +
		"    spikeAt: 2s\n" +
		"    spikeDuration: 3s\n" +
		"target:\n" +
		"  method: GET\n" +
		"  url: https://pulse.test\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	test, err := Load(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	phase := test.Config.Phases[0]
	if phase.Type != pulse.PhaseTypeSpike {
		t.Fatalf("expected spike phase, got %s", phase.Type)
	}
	if phase.SpikeAt != 2*time.Second {
		t.Fatalf("expected spikeAt 2s, got %v", phase.SpikeAt)
	}
	if phase.SpikeDuration != 3*time.Second {
		t.Fatalf("expected spikeDuration 3s, got %v", phase.SpikeDuration)
	}
}

func TestLoadParsesTargetHeadersAndPassesThemToHTTPClient(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	previousNewHTTPClient := newHTTPClient
	newHTTPClient = func(cfg fileConfig) httpClient {
		return transport.NewHTTPClientWith(transport.HTTPClientConfig{
			Timeout: cfg.Target.Timeout.Duration,
			Headers: cfg.Target.Headers,
		})
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
		"  url: " + srv.URL + "\n" +
		"  headers:\n" +
		"    Authorization: Bearer test-token\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	test, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if _, err := test.Scenario(context.Background()); err != nil {
		t.Fatalf("Scenario: %v", err)
	}

	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization header: want %q, got %q", "Bearer test-token", gotAuth)
	}
}

func TestLoadTargetTimeoutAppliesToHTTPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	previousNewHTTPClient := newHTTPClient
	newHTTPClient = func(cfg fileConfig) httpClient {
		return transport.NewHTTPClientWith(transport.HTTPClientConfig{
			Timeout: cfg.Target.Timeout.Duration,
			Headers: cfg.Target.Headers,
		})
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
		"  url: " + srv.URL + "\n" +
		"  timeout: 30ms\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	test, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	_, err = test.Scenario(context.Background())
	if err == nil {
		t.Fatal("expected timeout error from slow server, got nil")
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
	if err == nil {
		t.Fatal("expected error for missing phases, got nil")
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
	if err == nil {
		t.Fatal("expected error for non-positive phase duration, got nil")
	}
}

func TestLoadMapsRampPhaseYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "" +
		"phases:\n" +
		"  - type: ramp\n" +
		"    duration: 10s\n" +
		"    from: 1\n" +
		"    to: 50\n" +
		"target:\n" +
		"  method: GET\n" +
		"  url: https://httpbin.org/get\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	test, err := Load(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	phase := test.Config.Phases[0]
	if phase.Type != pulse.PhaseTypeRamp {
		t.Fatalf("expected ramp phase, got %s", phase.Type)
	}
	if phase.Duration != 10*time.Second {
		t.Fatalf("expected 10s, got %v", phase.Duration)
	}
	if phase.From != 1 || phase.To != 50 {
		t.Fatalf("expected from 1 to 50, got %d %d", phase.From, phase.To)
	}
}

func TestLoadRejectsInvalidRampEndpoints(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "" +
		"phases:\n" +
		"  - type: ramp\n" +
		"    duration: 1s\n" +
		"    from: 0\n" +
		"    to: 10\n" +
		"target:\n" +
		"  method: GET\n" +
		"  url: https://httpbin.org/get\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid ramp endpoints, got nil")
	}
}

func TestLoadRejectsUnsupportedPhaseType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "" +
		"phases:\n" +
		"  - type: mystery\n" +
		"    duration: 1s\n" +
		"    arrivalRate: 1\n" +
		"target:\n" +
		"  method: GET\n" +
		"  url: https://httpbin.org/get\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unsupported phase type, got nil")
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
	if err == nil {
		t.Fatal("expected error for non-positive arrival rate, got nil")
	}
}

func TestValidateConfigAcceptsPUTDeleteAndPATCH(t *testing.T) {
	cfg := fileConfig{
		Phases: []phaseConfig{
			{Type: "constant", Duration: duration{Duration: time.Second}, ArrivalRate: 1},
		},
		Target: targetConfig{URL: "https://pulse.test"},
	}

	methods := []string{http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		if err := validateConfig(cfg, method); err != nil {
			t.Fatalf("validateConfig(%q): expected no error, got %v", method, err)
		}
	}
}

func TestValidateConfigRejectsCONNECT(t *testing.T) {
	cfg := fileConfig{
		Phases: []phaseConfig{
			{Type: "constant", Duration: duration{Duration: time.Second}, ArrivalRate: 1},
		},
		Target: targetConfig{URL: "https://pulse.test"},
	}

	err := validateConfig(cfg, http.MethodConnect)
	if !errors.Is(err, errUnsupportedMethod) {
		t.Fatalf("expected %v, got %v", errUnsupportedMethod, err)
	}
}

func TestLoadRejectsUnsupportedSaturationPolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "" +
		"phases:\n" +
		"  - type: constant\n" +
		"    duration: 1s\n" +
		"    arrivalRate: 1\n" +
		"target:\n" +
		"  method: GET\n" +
		"  url: https://pulse.test\n" +
		"saturationPolicy: queue\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unsupported saturation policy, got nil")
	}
}

func TestLoadRejectsInvalidSpike(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "" +
		"phases:\n" +
		"  - type: spike\n" +
		"    duration: 1s\n" +
		"    from: 10\n" +
		"    to: 20\n" +
		"    spikeDuration: 0s\n" +
		"target:\n" +
		"  method: GET\n" +
		"  url: https://pulse.test\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected error for invalid spike config, got nil")
	}
}

func TestLoadRejectsSpikeOutsidePhase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "" +
		"phases:\n" +
		"  - type: spike\n" +
		"    duration: 1s\n" +
		"    from: 10\n" +
		"    to: 20\n" +
		"    spikeAt: 800ms\n" +
		"    spikeDuration: 300ms\n" +
		"target:\n" +
		"  method: GET\n" +
		"  url: https://pulse.test\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected error for spike that extends past phase duration, got nil")
	}
}

func TestValidateConfigRejectsInvalidTargetFields(t *testing.T) {
	// validateConfig is responsible only for target-specific fields (method, URL,
	// timeout). Phase, threshold, concurrency, and reporting checks are delegated
	// to pulse.ValidateConfig and are covered by api_test.go.
	valid := fileConfig{
		Phases: []phaseConfig{
			{Type: "constant", Duration: duration{Duration: time.Second}, ArrivalRate: 1},
		},
		Target: targetConfig{URL: "https://pulse.test"},
	}

	tests := []struct {
		name    string
		mutate  func(*fileConfig)
		wantErr error
	}{
		{
			name:    "negative timeout",
			mutate:  func(cfg *fileConfig) { cfg.Target.Timeout.Duration = -time.Second },
			wantErr: errNegativeTargetTimeout,
		},
		{
			name:    "relative target URL",
			mutate:  func(cfg *fileConfig) { cfg.Target.URL = "/api" },
			wantErr: errInvalidTargetURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			err := validateConfig(cfg, http.MethodGet)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestLoadRejectsUnknownYAMLFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := "" +
		"phases:\n" +
		"  - type: constant\n" +
		"    duration: 1s\n" +
		"    arrivalRate: 1\n" +
		"target:\n" +
		"  method: GET\n" +
		"  url: https://pulse.test\n" +
		"maxConcurreny: 5\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown YAML field error, got nil")
	}
}

func TestLoadRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.yaml")
	if err := os.WriteFile(path, make([]byte, maxConfigBytes+1), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected oversized YAML error")
	}
}

func TestLoadAcceptsRepositoryExamples(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "examples", "*.yaml"))
	if err != nil {
		t.Fatalf("glob examples: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("expected repository examples")
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if _, err := Load(path); err != nil {
				t.Fatalf("Load(%q): %v", path, err)
			}
		})
	}
}
