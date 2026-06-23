package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"algoryn.io/pulse/distributed"
)

func newRunBody(t *testing.T) *bytes.Reader {
	t.Helper()
	req := distributed.RunRequest{
		Phases: []distributed.Phase{
			{Type: "constant", ArrivalRate: 1, Duration: 1_000_000}, // 1ms
		},
		HTTPScenario: &distributed.HTTPScenario{URL: "http://127.0.0.1:1", Method: "GET"},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.NewReader(b)
}

func TestAuthorized(t *testing.T) {
	cases := []struct {
		name   string
		token  string
		header string
		want   bool
	}{
		{"no token configured allows all", "", "", true},
		{"no token ignores header", "", "Bearer whatever", true},
		{"correct token", "s3cret", "Bearer s3cret", true},
		{"wrong token", "s3cret", "Bearer nope", false},
		{"missing header", "s3cret", "", false},
		{"missing bearer prefix", "s3cret", "s3cret", false},
		{"empty bearer", "s3cret", "Bearer ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewWithOptions(nil, Options{AuthToken: c.token})
			r := httptest.NewRequest(http.MethodGet, "/ping", nil)
			if c.header != "" {
				r.Header.Set("Authorization", c.header)
			}
			if got := s.authorized(r); got != c.want {
				t.Fatalf("authorized = %v, want %v", got, c.want)
			}
		})
	}
}

func TestHandlePingRejectsUnauthenticated(t *testing.T) {
	s := NewWithOptions(nil, Options{AuthToken: "s3cret"})

	w := httptest.NewRecorder()
	s.handlePing(w, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("ping without token: got %d, want 401", w.Code)
	}

	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ping", nil)
	r.Header.Set("Authorization", "Bearer s3cret")
	s.handlePing(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("ping with token: got %d, want 200", w.Code)
	}
}

func TestHandleRunRejectsUnauthenticated(t *testing.T) {
	s := NewWithOptions(nil, Options{AuthToken: "s3cret"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/run", newRunBody(t))
	s.handleRun(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("run without token: got %d, want 401", w.Code)
	}
}

func TestHandleRunRejectsOversizedBody(t *testing.T) {
	s := NewWithOptions(nil, Options{}) // no auth so we reach the body decode
	huge := bytes.NewReader(append([]byte(`{"phases":[],"junk":"`), bytes.Repeat([]byte("a"), maxRequestBytes+1)...))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/run", huge)
	s.handleRun(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized body: got %d, want 400", w.Code)
	}
}

func TestBuildHTTPScenarioDenyPrivateBlocksMetadata(t *testing.T) {
	s := NewWithOptions(nil, Options{DenyPrivate: true})
	scenario := s.buildHTTPScenario(&distributed.HTTPScenario{
		URL:    "http://169.254.169.254/latest/meta-data/",
		Method: "GET",
	})
	_, err := scenario(context.Background())
	if err == nil {
		t.Fatal("expected SSRF-blocked request to a metadata endpoint to fail")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected an SSRF block error, got %v", err)
	}
}
