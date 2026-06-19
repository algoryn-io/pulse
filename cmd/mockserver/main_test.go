package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthyHandlerReturns200(t *testing.T) {
	h, err := newHandler(modeHealthy, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestMixedErrorsHandlerAlternates(t *testing.T) {
	h, err := newHandler(modeMixedErrors, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Request 1 (count=1, odd) → 200
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("request 1: expected 200, got %d", rec.Code)
	}

	// Request 2 (count=2, even) → 500
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("request 2: expected 500, got %d", rec.Code)
	}

	// Request 3 (count=3, odd) → 200 again
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("request 3: expected 200, got %d", rec.Code)
	}
}

func TestSlowHandlerIntroducesDelay(t *testing.T) {
	delay := 40 * time.Millisecond
	h, err := newHandler(modeSlow, delay, 0)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if elapsed < delay {
		t.Fatalf("expected elapsed >= %v, got %v", delay, elapsed)
	}
}

func TestSlowHandlerRespectsContextCancellation(t *testing.T) {
	// With a very long delay, cancelling the context should make the handler
	// return promptly rather than waiting for the full delay.
	delay := 10 * time.Second
	h, err := newHandler(modeSlow, delay, 0)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately to simulate client disconnect

	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(rec, req)
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("expected fast return on cancelled context, got %v", elapsed)
	}
}

func TestFlakyHandlerAlwaysFailsAtRateOne(t *testing.T) {
	h, err := newHandler(modeFlaky, 0, 1.0)
	if err != nil {
		t.Fatal(err)
	}

	for range 10 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 with flaky-rate=1.0, got %d", rec.Code)
		}
	}
}

func TestFlakyHandlerNeverFailsAtRateZero(t *testing.T) {
	h, err := newHandler(modeFlaky, 0, 0.0)
	if err != nil {
		t.Fatal(err)
	}

	for range 10 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 with flaky-rate=0.0, got %d", rec.Code)
		}
	}
}

func TestDownHandlerAlwaysReturns503(t *testing.T) {
	h, err := newHandler(modeDown, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	for range 5 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", rec.Code)
		}
	}
}

func TestUnknownModeReturnsError(t *testing.T) {
	_, err := newHandler("turbo", 0, 0)
	if err == nil {
		t.Fatal("expected error for unknown mode, got nil")
	}
}
