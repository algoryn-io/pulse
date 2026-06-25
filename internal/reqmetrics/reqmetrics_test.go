package reqmetrics

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSampleObserve(t *testing.T) {
	var s Sample
	s.Observe(5*time.Millisecond, 100, 20)
	s.Observe(9*time.Millisecond, 50, 5) // second TTFB ignored; bytes accumulate

	if got := s.TTFB(); got != 5*time.Millisecond {
		t.Fatalf("TTFB = %v, want 5ms (first wins)", got)
	}
	if got := s.BytesIn(); got != 150 {
		t.Fatalf("BytesIn = %d, want 150", got)
	}
	if got := s.BytesOut(); got != 25 {
		t.Fatalf("BytesOut = %d, want 25", got)
	}
}

func TestSampleObserveIgnoresNonPositive(t *testing.T) {
	var s Sample
	s.Observe(0, -10, -1)        // all ignored
	s.Observe(-1, 0, 0)          // ttfb<=0 ignored, no bytes
	if s.TTFB() != 0 || s.BytesIn() != 0 || s.BytesOut() != 0 {
		t.Fatalf("expected zero sample, got ttfb=%v in=%d out=%d", s.TTFB(), s.BytesIn(), s.BytesOut())
	}
	// A later positive TTFB still registers (the CAS only fires on the first
	// positive observation).
	s.Observe(3*time.Millisecond, 10, 0)
	if s.TTFB() != 3*time.Millisecond {
		t.Fatalf("TTFB = %v, want 3ms", s.TTFB())
	}
}

func TestNilSampleIsSafe(t *testing.T) {
	var s *Sample
	s.Observe(time.Second, 1, 1) // must not panic
	if s.TTFB() != 0 || s.BytesIn() != 0 || s.BytesOut() != 0 {
		t.Fatal("nil sample should report zeros")
	}
	if FromContext(context.Background()) != nil {
		t.Fatal("FromContext with no sample should be nil")
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx, s := NewContext(context.Background())
	if FromContext(ctx) != s {
		t.Fatal("FromContext should return the attached sample")
	}
	s.Observe(time.Millisecond, 10, 2)
	if FromContext(ctx).BytesIn() != 10 {
		t.Fatal("sample mutations should be visible through the context")
	}
}

func TestSampleConcurrentObserve(t *testing.T) {
	var s Sample
	var wg sync.WaitGroup
	const n = 100
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Observe(2*time.Millisecond, 1, 1)
		}()
	}
	wg.Wait()
	if s.BytesIn() != n || s.BytesOut() != n {
		t.Fatalf("bytes = in:%d out:%d, want %d each", s.BytesIn(), s.BytesOut(), n)
	}
	if s.TTFB() != 2*time.Millisecond {
		t.Fatalf("TTFB = %v, want 2ms", s.TTFB())
	}
}
