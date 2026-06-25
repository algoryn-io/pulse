package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"algoryn.io/pulse/internal/reqmetrics"
)

func TestDoCapturesTTFBAndBytes(t *testing.T) {
	const respBody = "hello world response body"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond) // make TTFB measurably non-zero
		_, _ = w.Write([]byte(respBody))
	}))
	defer srv.Close()

	client := NewHTTPClient()
	ctx, sample := reqmetrics.NewContext(context.Background())
	code, err := client.Do(ctx, http.MethodPost, srv.URL, strings.NewReader("request-body"))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if sample.TTFB() <= 0 {
		t.Fatalf("expected a positive TTFB, got %v", sample.TTFB())
	}
	if got := sample.BytesIn(); got != int64(len(respBody)) {
		t.Fatalf("BytesIn = %d, want %d", got, len(respBody))
	}
	if got := sample.BytesOut(); got != int64(len("request-body")) {
		t.Fatalf("BytesOut = %d, want %d", got, len("request-body"))
	}
}

func TestDoWithResponseCapturesTTFBAndBytes(t *testing.T) {
	const respBody = `{"ok":true}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(respBody))
	}))
	defer srv.Close()

	client := NewHTTPClient()
	ctx, sample := reqmetrics.NewContext(context.Background())
	resp, err := client.DoWithResponse(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("DoWithResponse: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if sample.TTFB() <= 0 {
		t.Fatalf("expected a positive TTFB, got %v", sample.TTFB())
	}
	if got := sample.BytesIn(); got != int64(len(respBody)) {
		t.Fatalf("BytesIn = %d, want %d", got, len(respBody))
	}
}

func TestDoWithoutSampleDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	// No reqmetrics sample in the context: observe must be a no-op.
	if _, err := NewHTTPClient().Do(context.Background(), http.MethodGet, srv.URL, nil); err != nil {
		t.Fatalf("Do without sample: %v", err)
	}
}
