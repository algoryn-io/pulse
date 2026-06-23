package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Response holds a completed HTTP response with the body pre-read into memory.
// The underlying connection is released before DoWithResponse returns, so it
// is safe to inspect after the call without draining or closing anything.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// DoWithResponse performs an HTTP request and returns the full response.
// Unlike Do, a status code >= 400 does not produce an error — use AssertStatus
// to enforce expected codes. The body is read into memory (up to
// MaxResponseBytes) so assertion helpers can inspect it without re-reading.
func (c *HTTPClient) DoWithResponse(ctx context.Context, method, url string, body io.Reader) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	maxBytes := c.maxResponseBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxResponseBytes
	}
	limited := io.LimitReader(resp.Body, maxBytes+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxBytes {
		return nil, ErrResponseBodyTooLarge
	}
	return &Response{
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       b,
	}, nil
}

// AssertStatus returns an error if resp.StatusCode does not equal expected.
func AssertStatus(resp *Response, expected int) error {
	if resp.StatusCode != expected {
		return fmt.Errorf("assertion failed: expected status %d, got %d", expected, resp.StatusCode)
	}
	return nil
}

// AssertBodyContains returns an error if resp.Body does not contain substr.
func AssertBodyContains(resp *Response, substr string) error {
	if !bytes.Contains(resp.Body, []byte(substr)) {
		return fmt.Errorf("assertion failed: body does not contain %q", substr)
	}
	return nil
}

// AssertBodyJSON unmarshals resp.Body into v. Returns an error if the body is
// not valid JSON or cannot be decoded into the target type.
func AssertBodyJSON(resp *Response, v any) error {
	if err := json.Unmarshal(resp.Body, v); err != nil {
		return fmt.Errorf("assertion failed: body is not valid JSON: %w", err)
	}
	return nil
}

// AssertHeader returns an error if resp.Header.Get(key) does not equal expected.
// The key is canonicalized automatically (e.g. "content-type" → "Content-Type").
func AssertHeader(resp *Response, key, expected string) error {
	got := resp.Header.Get(key)
	if got != expected {
		return fmt.Errorf("assertion failed: expected header %q = %q, got %q", key, expected, got)
	}
	return nil
}
