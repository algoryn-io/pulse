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

// Get performs an HTTP GET request.
func (c *HTTPClient) Get(ctx context.Context, url string) error {
	return c.do(ctx, http.MethodGet, url, nil)
}

// Post performs an HTTP POST request with the provided body.
func (c *HTTPClient) Post(ctx context.Context, url string, body io.Reader) error {
	return c.do(ctx, http.MethodPost, url, body)
}

func (c *HTTPClient) do(ctx context.Context, method, url string, body io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return err
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("transport: unexpected status code: %d", resp.StatusCode)
	}

	return nil
}
