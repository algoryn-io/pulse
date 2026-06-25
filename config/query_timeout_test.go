package config

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestAppendQuery(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		params map[string]string
		want   string
	}{
		{"no params", "http://x/y", nil, "http://x/y"},
		{"adds query", "http://x/y", map[string]string{"a": "1", "b": "2"}, "http://x/y?a=1&b=2"},
		{"sorted deterministic", "http://x/y", map[string]string{"z": "1", "a": "2"}, "http://x/y?a=2&z=1"},
		{"merges with existing query", "http://x/y?q=1", map[string]string{"a": "2"}, "http://x/y?q=1&a=2"},
		{"encodes value and key", "http://x/y", map[string]string{"q": "a b&c"}, "http://x/y?q=a+b%26c"},
		{"leaves feeder placeholders untouched", "http://x/u/{{id}}", map[string]string{"k": "v"}, "http://x/u/{{id}}?k=v"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := appendQuery(c.url, c.params); got != c.want {
				t.Fatalf("appendQuery = %q, want %q", got, c.want)
			}
		})
	}
}

func TestLoadAppliesQueryParams(t *testing.T) {
	var mu sync.Mutex
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotURL = r.URL.String()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfgPath := writeCfg(t, ""+
		"phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n"+
		"target:\n  method: GET\n  url: "+srv.URL+"/search\n"+
		"  query:\n    q: golang\n    limit: \"10\"\n")
	test, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := test.Scenario(context.Background()); err != nil {
		t.Fatalf("scenario: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	// Keys are sorted: limit before q.
	if gotURL != "/search?limit=10&q=golang" {
		t.Fatalf("server saw %q, want /search?limit=10&q=golang", gotURL)
	}
}

func TestLoadEmptyQueryKeyRejected(t *testing.T) {
	cfgPath := writeCfg(t, ""+
		"phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n"+
		"target:\n  method: GET\n  url: http://example.com/\n"+
		"  query:\n    \"\": value\n")
	_, err := Load(cfgPath)
	if !errors.Is(err, errEmptyQueryKey) {
		t.Fatalf("expected errEmptyQueryKey, got %v", err)
	}
}

func TestPerRequestTimeoutFires(t *testing.T) {
	// Server sleeps longer than the configured timeout, but honors cancellation.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	cfgPath := writeCfg(t, ""+
		"phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n"+
		"target:\n  method: GET\n  url: "+srv.URL+"\n  timeout: 50ms\n")
	test, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	start := time.Now()
	code, err := test.Scenario(context.Background())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected a timeout error, got code=%d", code)
	}
	if elapsed > time.Second {
		t.Fatalf("request did not time out promptly: took %v", elapsed)
	}
}

func TestPerRequestTimeoutHonorsRunCancellation(t *testing.T) {
	// With no per-request timeout, an already-cancelled context still aborts.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	cfgPath := writeCfg(t, ""+
		"phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n"+
		"target:\n  method: GET\n  url: "+srv.URL+"\n  timeout: 5s\n")
	test, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	if _, err := test.Scenario(ctx); err == nil {
		t.Fatal("expected cancellation to abort the request")
	} else if !strings.Contains(err.Error(), "context") {
		t.Fatalf("expected a context error, got %v", err)
	}
}
