package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestContextWithResponseStatusRecordsStatusCode(t *testing.T) {
	var code int
	ctx := ContextWithResponseStatus(context.Background(), &code)

	client := &HTTPClient{
		client: &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return responseWithStatus(http.StatusAccepted, "x"), nil
			}),
		},
	}

	if err := client.Get(ctx, "http://pulse.test"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, code)
	}
}

func TestHTTPClientGetSuccess(t *testing.T) {
	client := &HTTPClient{
		client: &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatalf("expected GET, got %s", r.Method)
				}

				return responseWithStatus(http.StatusOK, "ok"), nil
			}),
		},
	}

	if err := client.Get(context.Background(), "http://pulse.test"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHTTPClientPostSuccess(t *testing.T) {
	client := &HTTPClient{
		client: &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost {
					t.Fatalf("expected POST, got %s", r.Method)
				}

				payload, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("expected readable body, got %v", err)
				}

				if string(payload) != "pulse" {
					t.Fatalf("expected body %q, got %q", "pulse", string(payload))
				}

				return responseWithStatus(http.StatusCreated, ""), nil
			}),
		},
	}

	if err := client.Post(context.Background(), "http://pulse.test", strings.NewReader("pulse")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHTTPClientReturnsErrorForFailingStatusCode(t *testing.T) {
	client := &HTTPClient{
		client: &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return responseWithStatus(http.StatusInternalServerError, ""), nil
			}),
		},
	}

	if err := client.Get(context.Background(), "http://pulse.test"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestHTTPClientRespectsContextCancellation(t *testing.T) {
	client := &HTTPClient{
		client: &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				<-r.Context().Done()
				return nil, r.Context().Err()
			}),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.Get(ctx, "http://pulse.test")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected %v, got %v", context.Canceled, err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func responseWithStatus(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}
