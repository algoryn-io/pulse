package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// -- DoWithResponse --

func TestDoWithResponseReturnsBodyAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewHTTPClient()
	resp, err := c.DoWithResponse(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if string(resp.Body) != `{"ok":true}` {
		t.Fatalf("unexpected body: %s", resp.Body)
	}
}

func TestDoWithResponseDoesNotErrorOn4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewHTTPClient()
	resp, err := c.DoWithResponse(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("expected no error for 404, got %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestDoWithResponseReturnsHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "pulse")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewHTTPClient()
	resp, err := c.DoWithResponse(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.Header.Get("X-Custom"); got != "pulse" {
		t.Fatalf("expected X-Custom: pulse, got %q", got)
	}
}

func TestDoWithResponseErrorsOnBodyTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// write more than the 1-byte limit set on the client
		w.Write([]byte("ab"))
	}))
	defer srv.Close()

	c := NewHTTPClientWith(HTTPClientConfig{MaxResponseBytes: 1})
	_, err := c.DoWithResponse(context.Background(), http.MethodGet, srv.URL, nil)
	if err != ErrResponseBodyTooLarge {
		t.Fatalf("expected ErrResponseBodyTooLarge, got %v", err)
	}
}

// -- AssertStatus --

func TestAssertStatusPass(t *testing.T) {
	resp := &Response{StatusCode: 200}
	if err := AssertStatus(resp, 200); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAssertStatusFail(t *testing.T) {
	resp := &Response{StatusCode: 404}
	err := AssertStatus(resp, 200)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "expected status 200, got 404") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// -- AssertBodyContains --

func TestAssertBodyContainsPass(t *testing.T) {
	resp := &Response{Body: []byte(`{"status":"ok"}`)}
	if err := AssertBodyContains(resp, `"status":"ok"`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAssertBodyContainsFail(t *testing.T) {
	resp := &Response{Body: []byte(`{"status":"error"}`)}
	err := AssertBodyContains(resp, `"status":"ok"`)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "does not contain") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// -- AssertBodyJSON --

func TestAssertBodyJSONPass(t *testing.T) {
	resp := &Response{Body: []byte(`{"id":42,"name":"pulse"}`)}
	var got struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := AssertBodyJSON(resp, &got); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != 42 || got.Name != "pulse" {
		t.Fatalf("unexpected decoded value: %+v", got)
	}
}

func TestAssertBodyJSONFail(t *testing.T) {
	resp := &Response{Body: []byte(`not json`)}
	var got map[string]any
	err := AssertBodyJSON(resp, &got)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestAssertBodyJSONRoundTrip(t *testing.T) {
	type payload struct {
		Count int `json:"count"`
	}
	original := payload{Count: 7}
	b, _ := json.Marshal(original)
	resp := &Response{Body: b}

	var decoded payload
	if err := AssertBodyJSON(resp, &decoded); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded.Count != 7 {
		t.Fatalf("expected Count=7, got %d", decoded.Count)
	}
}

// -- AssertHeader --

func TestAssertHeaderPass(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	resp := &Response{Header: h}
	if err := AssertHeader(resp, "Content-Type", "application/json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAssertHeaderCanonicalizesKey(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	resp := &Response{Header: h}
	if err := AssertHeader(resp, "content-type", "application/json"); err != nil {
		t.Fatalf("expected key canonicalization to work, got: %v", err)
	}
}

func TestAssertHeaderFail(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "text/plain")
	resp := &Response{Header: h}
	err := AssertHeader(resp, "Content-Type", "application/json")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "got") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestAssertHeaderMissing(t *testing.T) {
	resp := &Response{Header: http.Header{}}
	err := AssertHeader(resp, "X-Request-ID", "abc123")
	if err == nil {
		t.Fatal("expected error for missing header, got nil")
	}
}
