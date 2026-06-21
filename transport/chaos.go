package transport

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"time"
)

// ErrChaosInjected is returned by ChaosRoundTripper when a synthetic error is
// injected. Use errors.Is to detect it in scenario error counts.
var ErrChaosInjected = errors.New("chaos: injected error")

// ChaosConfig controls fault injection for a ChaosRoundTripper.
// Both ErrorRate and LatencyRate are independent: a request may receive latency
// injection and still be forwarded normally (error injection short-circuits first).
type ChaosConfig struct {
	// ErrorRate is the fraction [0,1] of requests that return ErrChaosInjected
	// without being forwarded to the wrapped transport.
	ErrorRate float64
	// LatencyRate is the fraction [0,1] of requests that receive injected latency
	// before being forwarded.
	LatencyRate float64
	// Latency is the fixed additional delay injected for requests selected by
	// LatencyRate. Zero disables latency injection even when LatencyRate > 0.
	Latency time.Duration
}

// ChaosRoundTripper wraps an http.RoundTripper and injects synthetic faults
// according to ChaosConfig. Combine it with HTTPClientConfig.Transport to
// stress-test any Pulse HTTP scenario without touching scenario logic:
//
//	chaos := transport.NewChaosRoundTripper(nil, transport.ChaosConfig{
//	    ErrorRate:   0.05,
//	    LatencyRate: 0.10,
//	    Latency:     100 * time.Millisecond,
//	})
//	client := transport.NewHTTPClientWith(transport.HTTPClientConfig{Transport: chaos})
type ChaosRoundTripper struct {
	inner  http.RoundTripper
	config ChaosConfig
	rng    func() float64
}

// NewChaosRoundTripper creates a ChaosRoundTripper wrapping inner.
// If inner is nil, http.DefaultTransport is used.
// Panics if ErrorRate or LatencyRate is outside [0,1].
func NewChaosRoundTripper(inner http.RoundTripper, cfg ChaosConfig) *ChaosRoundTripper {
	if cfg.ErrorRate < 0 || cfg.ErrorRate > 1 {
		panic("transport: ChaosConfig.ErrorRate must be in [0,1]")
	}
	if cfg.LatencyRate < 0 || cfg.LatencyRate > 1 {
		panic("transport: ChaosConfig.LatencyRate must be in [0,1]")
	}
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &ChaosRoundTripper{inner: inner, config: cfg, rng: rand.Float64}
}

// RoundTrip implements http.RoundTripper. It applies fault injection before
// delegating to the wrapped transport.
func (c *ChaosRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if c.config.ErrorRate > 0 && c.rng() < c.config.ErrorRate {
		return nil, ErrChaosInjected
	}
	if c.config.LatencyRate > 0 && c.config.Latency > 0 && c.rng() < c.config.LatencyRate {
		t := time.NewTimer(c.config.Latency)
		defer t.Stop()
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-t.C:
		}
	}
	return c.inner.RoundTrip(req)
}
