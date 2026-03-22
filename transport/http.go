package transport

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// HTTPClient is the minimal HTTP transport for Pulse scenarios.
type HTTPClient struct {
	client *http.Client
}

// NewHTTPClient creates an HTTP client backed by the default net/http client.
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{client: http.DefaultClient}
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

func (c *HTTPClient) do(ctx context.Context, method, url string, body io.Reader) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return 0, err
	}
	defer resp.Body.Close()

	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return resp.StatusCode, err
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return resp.StatusCode, fmt.Errorf("transport: unexpected status code: %d", resp.StatusCode)
	}

	return resp.StatusCode, nil
}
