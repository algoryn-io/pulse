package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

const (
	DefaultTimeout          = 30 * time.Second
	DefaultMaxResponseBytes = int64(1 << 20)
)

var ErrResponseBodyTooLarge = errors.New("pulse: response body exceeds configured limit")

// HTTPClientConfig holds optional HTTP client settings for Pulse scenarios.
type HTTPClientConfig struct {
	Timeout          time.Duration
	MaxResponseBytes int64
	Headers          map[string]string
}

// HTTPClient is the minimal HTTP transport for Pulse scenarios.
type HTTPClient struct {
	client           *http.Client
	headers          map[string]string
	maxResponseBytes int64
}

// NewHTTPClient creates an HTTP client with defensive defaults.
func NewHTTPClient() *HTTPClient {
	return NewHTTPClientWith(HTTPClientConfig{})
}

// NewHTTPClientWith builds a client using the given config. Zero timeout and
// response body limit values use defensive defaults.
func NewHTTPClientWith(cfg HTTPClientConfig) *HTTPClient {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = DefaultMaxResponseBytes
	}
	return &HTTPClient{
		client:           &http.Client{Timeout: cfg.Timeout},
		headers:          cloneHeaderMap(cfg.Headers),
		maxResponseBytes: cfg.MaxResponseBytes,
	}
}

func cloneHeaderMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Get performs an HTTP GET request. On success it returns the response status
// code and a nil error. If the request fails before a response is received,
// the status code is 0.
func (c *HTTPClient) Get(ctx context.Context, url string) (int, error) {
	return c.do(ctx, http.MethodGet, url, nil)
}

// Post performs an HTTP POST request with the provided body. On success it
// returns the response status code and a nil error. If the request fails
// before a response is received, the status code is 0.
func (c *HTTPClient) Post(ctx context.Context, url string, body io.Reader) (int, error) {
	return c.do(ctx, http.MethodPost, url, body)
}

// Put performs an HTTP PUT request with the provided body.
func (c *HTTPClient) Put(ctx context.Context, url string, body io.Reader) (int, error) {
	return c.do(ctx, http.MethodPut, url, body)
}

// Delete performs an HTTP DELETE request.
func (c *HTTPClient) Delete(ctx context.Context, url string) (int, error) {
	return c.do(ctx, http.MethodDelete, url, nil)
}

// Patch performs an HTTP PATCH request with the provided body.
func (c *HTTPClient) Patch(ctx context.Context, url string, body io.Reader) (int, error) {
	return c.do(ctx, http.MethodPatch, url, body)
}

// Do performs an HTTP request with the provided method and optional body.
func (c *HTTPClient) Do(ctx context.Context, method, url string, body io.Reader) (int, error) {
	return c.do(ctx, method, url, body)
}

func (c *HTTPClient) do(ctx context.Context, method, url string, body io.Reader) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, err
	}

	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return 0, err
	}
	defer resp.Body.Close()

	maxResponseBytes := c.maxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	limited := io.LimitReader(resp.Body, maxResponseBytes+1)
	written, err := io.Copy(io.Discard, limited)
	if err != nil {
		return resp.StatusCode, err
	}
	if written > maxResponseBytes {
		return resp.StatusCode, ErrResponseBodyTooLarge
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return resp.StatusCode, &HTTPStatusError{StatusCode: resp.StatusCode}
	}

	return resp.StatusCode, nil
}
