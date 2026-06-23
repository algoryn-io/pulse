package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
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

	// Transport is an optional custom http.RoundTripper. When set it takes
	// precedence over MaxIdleConns, MaxIdleConnsPerHost, and DisableKeepAlives.
	// Use this to wrap the transport with additional behaviour such as SSRF
	// protection:
	//
	//   import "algoryn.io/pulse/internal/ssrf"
	//   rt := ssrf.NewRoundTripper(ssrf.DefaultDenyPrivatePolicy(), nil)
	//   client := transport.NewHTTPClientWith(transport.HTTPClientConfig{Transport: rt})
	Transport http.RoundTripper

	// MaxIdleConns is the maximum number of idle (keep-alive) connections
	// across all hosts. Zero means no limit. When this or MaxIdleConnsPerHost
	// or DisableKeepAlives is set, http.DefaultTransport is cloned and only
	// the specified fields are overridden, so TLS, proxy, and dial settings
	// remain at their standard values.
	// Ignored when Transport is set.
	MaxIdleConns int

	// MaxIdleConnsPerHost is the maximum number of idle connections kept per
	// host. The Go default is 2, which is often too low for load testing. Set
	// this to at least the expected concurrency for the target host to avoid
	// connection churn under high arrival rates.
	// Ignored when Transport is set.
	MaxIdleConnsPerHost int

	// DisableKeepAlives, when true, disables HTTP keep-alives and opens a
	// fresh TCP connection for every request. Useful for measuring per-request
	// overhead without the benefit of connection reuse.
	// Ignored when Transport is set.
	DisableKeepAlives bool

	// Jar is an optional cookie jar attached to the client. When set, cookies
	// from responses are stored and resent on subsequent requests made through
	// the same client. For per-virtual-user sessions (independent cookies per
	// scenario invocation), prefer calling Session() inside the scenario rather
	// than sharing one jar across all concurrent invocations.
	Jar http.CookieJar
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
	httpClient := &http.Client{Timeout: cfg.Timeout, Jar: cfg.Jar}
	switch {
	case cfg.Transport != nil:
		httpClient.Transport = cfg.Transport
	case cfg.MaxIdleConns > 0 || cfg.MaxIdleConnsPerHost > 0 || cfg.DisableKeepAlives:
		// Clone the default transport so TLS, proxy, and dial settings are
		// preserved, then apply only the fields the caller explicitly set.
		t := http.DefaultTransport.(*http.Transport).Clone()
		if cfg.MaxIdleConns > 0 {
			t.MaxIdleConns = cfg.MaxIdleConns
		}
		if cfg.MaxIdleConnsPerHost > 0 {
			t.MaxIdleConnsPerHost = cfg.MaxIdleConnsPerHost
		}
		t.DisableKeepAlives = cfg.DisableKeepAlives
		httpClient.Transport = t
	}
	return &HTTPClient{
		client:           httpClient,
		headers:          cloneHeaderMap(cfg.Headers),
		maxResponseBytes: cfg.MaxResponseBytes,
	}
}

// Session returns a new HTTPClient that shares this client's underlying
// transport (so the connection pool and TLS settings are reused) but has its
// own fresh in-memory cookie jar. Call it at the start of a scenario to give
// each virtual-user iteration an isolated session: cookies set during the
// iteration (e.g. a login response) are resent on later requests in the same
// iteration, but are not shared with other concurrent iterations.
//
//	base := transport.NewHTTPClientWith(cfg) // shared, pooled transport
//	scenario := func(ctx context.Context) (int, error) {
//	    s := base.Session()
//	    if _, err := s.Do(ctx, http.MethodPost, loginURL, body); err != nil {
//	        return 0, err
//	    }
//	    return s.Do(ctx, http.MethodGet, profileURL, nil) // sends the login cookie
//	}
func (c *HTTPClient) Session() *HTTPClient {
	jar, _ := cookiejar.New(nil) // error is always nil for nil options
	sessionClient := &http.Client{
		Transport: c.client.Transport,
		Timeout:   c.client.Timeout,
		Jar:       jar,
	}
	return &HTTPClient{
		client:           sessionClient,
		headers:          c.headers,
		maxResponseBytes: c.maxResponseBytes,
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
			_ = resp.Body.Close()
		}
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

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
