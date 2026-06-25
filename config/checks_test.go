package config

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"algoryn.io/pulse/transport"
)

// loadScenario writes a YAML config to a temp file and returns the built
// scenario. It uses the real HTTP client (newHTTPClient) so checks run against
// the provided server.
func loadScenario(t *testing.T, yaml string) func(context.Context) (int, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	test, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return test.Scenario
}

func checksYAML(url, checks string) string {
	return fmt.Sprintf(""+
		"phases:\n"+
		"  - type: constant\n"+
		"    duration: 1s\n"+
		"    arrivalRate: 1\n"+
		"target:\n"+
		"  method: GET\n"+
		"  url: %s\n"+
		"  checks:\n%s", url, checks)
}

func TestChecksPassMarksSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	}))
	defer srv.Close()

	scenario := loadScenario(t, checksYAML(srv.URL, ""+
		"    status: 200\n"+
		"    headerEquals:\n"+
		"      Content-Type: application/json\n"+
		"    bodyContains:\n"+
		"      - healthy\n"+
		"    jsonEquals:\n"+
		"      status: healthy\n"))

	code, err := scenario(context.Background())
	if err != nil {
		t.Fatalf("expected checks to pass, got %v", err)
	}
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
}

func TestChecksStatusMismatchIsCheckFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	scenario := loadScenario(t, checksYAML(srv.URL, "    status: 201\n"))
	code, err := scenario(context.Background())
	if err == nil {
		t.Fatal("expected a check failure")
	}
	if !errors.Is(err, transport.ErrCheckFailed) {
		t.Fatalf("expected ErrCheckFailed, got %v", err)
	}
	// Status code is still reported so status-count metrics stay accurate.
	if code != 200 {
		t.Fatalf("status = %d, want the real 200", code)
	}
}

// With an explicit status check, a 4xx that matches the check is a success:
// the user has taken full control of status evaluation.
func TestChecksExplicitStatusOverrides4xxDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	scenario := loadScenario(t, checksYAML(srv.URL, "    status: 404\n"))
	code, err := scenario(context.Background())
	if err != nil {
		t.Fatalf("expected an expected-404 to pass, got %v", err)
	}
	if code != 404 {
		t.Fatalf("status = %d, want 404", code)
	}
}

// Without a status check, the default "4xx/5xx fails" semantics are preserved
// even when other checks pass.
func TestChecksWithoutStatusPreserves4xxDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	scenario := loadScenario(t, checksYAML(srv.URL, ""+
		"    bodyContains:\n"+
		"      - boom\n"))
	code, err := scenario(context.Background())
	if err == nil {
		t.Fatal("expected a 500 without a status check to still fail")
	}
	var statusErr *transport.HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected HTTPStatusError, got %v", err)
	}
	if code != 500 {
		t.Fatalf("status = %d, want 500", code)
	}
}

func TestChecksInvalidStatusRejectedAtLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	yaml := checksYAML("http://example.com/", "    status: 99\n")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Load(path)
	if !errors.Is(err, errInvalidCheckStatus) {
		t.Fatalf("expected errInvalidCheckStatus, got %v", err)
	}
}
