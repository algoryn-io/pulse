package transport

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestChaosRoundTripperInjectsErrorBelowThreshold(t *testing.T) {
	inner := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("inner should not be called when error is injected")
		return nil, nil
	})
	c := NewChaosRoundTripper(inner, ChaosConfig{ErrorRate: 0.5})
	c.rng = func() float64 { return 0.0 } // always below threshold

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	_, err := c.RoundTrip(req)
	if !errors.Is(err, ErrChaosInjected) {
		t.Fatalf("expected ErrChaosInjected, got %v", err)
	}
}

func TestChaosRoundTripperPassesThroughAboveErrorThreshold(t *testing.T) {
	var called bool
	inner := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return responseWithStatus(200, ""), nil
	})
	c := NewChaosRoundTripper(inner, ChaosConfig{ErrorRate: 0.5})
	c.rng = func() float64 { return 1.0 } // always above threshold

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := c.RoundTrip(req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	resp.Body.Close()
	if !called {
		t.Fatal("expected inner to be called")
	}
}

func TestChaosRoundTripperZeroErrorRateNeverInjects(t *testing.T) {
	var calls int
	inner := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return responseWithStatus(200, ""), nil
	})
	c := NewChaosRoundTripper(inner, ChaosConfig{ErrorRate: 0})
	// rng not overridden — default random, but rate=0 so it never fires

	for range 10 {
		req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
		resp, err := c.RoundTrip(req)
		if err != nil {
			t.Fatalf("expected no error with ErrorRate=0, got %v", err)
		}
		resp.Body.Close()
	}
	if calls != 10 {
		t.Fatalf("expected 10 inner calls, got %d", calls)
	}
}

func TestChaosRoundTripperInjectsLatency(t *testing.T) {
	var called bool
	inner := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return responseWithStatus(200, ""), nil
	})
	c := NewChaosRoundTripper(inner, ChaosConfig{
		LatencyRate: 1.0,
		Latency:     20 * time.Millisecond,
	})
	c.rng = func() float64 { return 0.0 } // always inject latency

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	start := time.Now()
	resp, err := c.RoundTrip(req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	resp.Body.Close()
	if !called {
		t.Fatal("expected inner to be called after latency")
	}
	if elapsed < 20*time.Millisecond {
		t.Fatalf("expected at least 20ms latency, got %v", elapsed)
	}
}

func TestChaosRoundTripperSkipsLatencyAboveThreshold(t *testing.T) {
	var called bool
	inner := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return responseWithStatus(200, ""), nil
	})
	c := NewChaosRoundTripper(inner, ChaosConfig{
		LatencyRate: 0.5,
		Latency:     5 * time.Second,
	})
	c.rng = func() float64 { return 1.0 } // always above threshold → no latency

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	start := time.Now()
	resp, err := c.RoundTrip(req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	resp.Body.Close()
	if !called {
		t.Fatal("expected inner to be called")
	}
	if elapsed >= time.Second {
		t.Fatalf("expected no latency injection, but took %v", elapsed)
	}
}

func TestChaosRoundTripperLatencyRespectsContextCancellation(t *testing.T) {
	inner := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("inner should not be called when context is cancelled during latency")
		return nil, nil
	})
	c := NewChaosRoundTripper(inner, ChaosConfig{
		LatencyRate: 1.0,
		Latency:     10 * time.Second,
	})
	c.rng = func() float64 { return 0.0 }

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com", nil)
	_, err := c.RoundTrip(req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestChaosRoundTripperErrorTakesPrecedenceOverLatency(t *testing.T) {
	// rng always returns 0 → error fires first, latency is never reached
	var latencyChecked bool
	inner := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("inner must not be called when error is injected")
		return nil, nil
	})
	c := NewChaosRoundTripper(inner, ChaosConfig{
		ErrorRate:   1.0,
		LatencyRate: 1.0,
		Latency:     5 * time.Second,
	})
	callCount := 0
	c.rng = func() float64 {
		callCount++
		if callCount == 1 {
			return 0.0 // triggers error
		}
		// second call would be for latency check — it shouldn't be reached
		latencyChecked = true
		return 0.0
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	_, err := c.RoundTrip(req)
	if !errors.Is(err, ErrChaosInjected) {
		t.Fatalf("expected ErrChaosInjected, got %v", err)
	}
	if latencyChecked {
		t.Fatal("latency should not be checked when error is already injected")
	}
}

func TestChaosRoundTripperNilInnerUsesDefaultTransport(t *testing.T) {
	c := NewChaosRoundTripper(nil, ChaosConfig{})
	if c.inner != http.DefaultTransport {
		t.Fatalf("expected http.DefaultTransport, got %T", c.inner)
	}
}

func TestNewChaosRoundTripperPanicsOnInvalidErrorRate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for ErrorRate > 1")
		}
	}()
	NewChaosRoundTripper(nil, ChaosConfig{ErrorRate: 1.5})
}

func TestNewChaosRoundTripperPanicsOnInvalidLatencyRate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for LatencyRate < 0")
		}
	}()
	NewChaosRoundTripper(nil, ChaosConfig{LatencyRate: -0.1})
}

func TestChaosRoundTripperForwardsInnerError(t *testing.T) {
	wantErr := errors.New("connection refused")
	inner := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return nil, wantErr
	})
	c := NewChaosRoundTripper(inner, ChaosConfig{ErrorRate: 0})

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	_, err := c.RoundTrip(req)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected inner error %v, got %v", wantErr, err)
	}
}
