package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"sync/atomic"
	"time"
)

const (
	modeHealthy     = "healthy"
	modeMixedErrors = "mixed-errors"
	modeSlow        = "slow"
	modeFlaky       = "flaky"
	modeDown        = "down"
)

func main() {
	mode := flag.String("mode", modeHealthy,
		"server mode: healthy, mixed-errors, slow, flaky, down")
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	slowDelay := flag.Duration("slow-delay", 120*time.Millisecond,
		"artificial response delay for slow mode")
	flakyRate := flag.Float64("flaky-rate", 0.3,
		"fraction of requests that return 500 in flaky mode (0.0–1.0)")
	flag.Parse()

	handler, err := newHandler(*mode, *slowDelay, *flakyRate)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("mock server listening on %s (mode=%s)\n", *addr, *mode)
	server := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}

// newHandler returns an http.Handler for the given mode. slowDelay and
// flakyRate are only used by their respective modes and are ignored otherwise.
func newHandler(mode string, slowDelay time.Duration, flakyRate float64) (http.Handler, error) {
	switch mode {
	case modeHealthy:
		return healthyHandler(), nil
	case modeMixedErrors:
		return mixedErrorsHandler(), nil
	case modeSlow:
		return slowHandler(slowDelay), nil
	case modeFlaky:
		return flakyHandler(flakyRate), nil
	case modeDown:
		return downHandler(), nil
	default:
		return nil, fmt.Errorf(
			"unsupported mode %q (expected healthy, mixed-errors, slow, flaky, or down)", mode)
	}
}

// healthyHandler always returns 200 OK.
func healthyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
}

// mixedErrorsHandler alternates between 200 and 500 on successive requests.
// Useful for testing retry and error-rate threshold behaviour.
func mixedErrorsHandler() http.Handler {
	var count atomic.Uint64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if count.Add(1)%2 == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
}

// slowHandler waits delay before responding. The wait respects context
// cancellation so the server goroutine is released when the client disconnects.
// Useful for testing latency thresholds and timeout behaviour.
func slowHandler(delay time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
}

// flakyHandler returns 500 for approximately rate × 100% of requests and 200
// for the rest. rate must be in [0.0, 1.0]. Useful for testing circuit breakers
// and saturation policies under realistic partial-failure conditions.
func flakyHandler(rate float64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rand.Float64() < rate {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
}

// downHandler always returns 503 Service Unavailable. Useful for testing
// circuit-breaker open/half-open transitions and dropped-rate thresholds.
func downHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("service unavailable\n"))
	})
}
