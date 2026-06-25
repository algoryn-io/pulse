// Package reqmetrics carries per-request transport observations (time-to-first-
// byte and byte counts) from the transport layer up to the engine through the
// request context. The engine attaches a Sample before invoking a scenario; the
// transport fills it in while performing HTTP requests; the engine reads it back
// after the scenario returns and feeds it to the metrics aggregator.
//
// This indirection keeps the scenario signature (func(context.Context) (int,
// error)) unchanged while still letting the transport report richer metrics.
package reqmetrics

import (
	"context"
	"sync/atomic"
	"time"
)

// Sample accumulates transport observations for a single scenario iteration.
// All methods are safe for concurrent use, so a scenario that issues several
// requests (even concurrently) reports consistent totals. Byte counts sum across
// every request; the time-to-first-byte is recorded once — the first request in
// the iteration wins — because the aggregator records exactly one TTFB sample
// per scenario iteration.
type Sample struct {
	ttfbNanos int64
	ttfbSet   int32
	bytesIn   int64
	bytesOut  int64
}

// Observe records one HTTP request's observations. ttfb <= 0 is ignored (e.g. a
// request that failed before the first response byte). bytesIn/bytesOut < 0 are
// treated as zero.
func (s *Sample) Observe(ttfb time.Duration, bytesIn, bytesOut int64) {
	if s == nil {
		return
	}
	if ttfb > 0 && atomic.CompareAndSwapInt32(&s.ttfbSet, 0, 1) {
		atomic.StoreInt64(&s.ttfbNanos, ttfb.Nanoseconds())
	}
	if bytesIn > 0 {
		atomic.AddInt64(&s.bytesIn, bytesIn)
	}
	if bytesOut > 0 {
		atomic.AddInt64(&s.bytesOut, bytesOut)
	}
}

// TTFB returns the recorded time-to-first-byte, or 0 if none was observed.
func (s *Sample) TTFB() time.Duration {
	if s == nil {
		return 0
	}
	return time.Duration(atomic.LoadInt64(&s.ttfbNanos))
}

// BytesIn returns the total response bytes read across the iteration.
func (s *Sample) BytesIn() int64 {
	if s == nil {
		return 0
	}
	return atomic.LoadInt64(&s.bytesIn)
}

// BytesOut returns the total request bytes sent across the iteration.
func (s *Sample) BytesOut() int64 {
	if s == nil {
		return 0
	}
	return atomic.LoadInt64(&s.bytesOut)
}

type ctxKey struct{}

// NewContext returns a child context carrying a fresh Sample, plus the Sample so
// the caller can read it back after the scenario completes.
func NewContext(ctx context.Context) (context.Context, *Sample) {
	s := &Sample{}
	return context.WithValue(ctx, ctxKey{}, s), s
}

// FromContext returns the Sample attached to ctx, or nil when none is present
// (e.g. a scenario run outside the engine). Callers must tolerate a nil Sample;
// its methods are nil-safe.
func FromContext(ctx context.Context) *Sample {
	s, _ := ctx.Value(ctxKey{}).(*Sample)
	return s
}
