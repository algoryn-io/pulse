package pulse

import "sync/atomic"

// Feeder supplies values to concurrent scenario invocations in a thread-safe,
// allocation-free manner. Use NewFeeder for a fixed dataset that cycles
// round-robin, or NewFeederFunc for generated or random values.
type Feeder[T any] struct {
	items []T
	fn    func() T
	idx   atomic.Uint64
}

// NewFeeder returns a Feeder that cycles through items in order, wrapping
// around when the end is reached. Safe for concurrent use. Panics if items
// is empty.
func NewFeeder[T any](items []T) *Feeder[T] {
	if len(items) == 0 {
		panic("pulse: NewFeeder requires at least one item")
	}
	cp := make([]T, len(items))
	copy(cp, items)
	return &Feeder[T]{items: cp}
}

// NewFeederFunc returns a Feeder that calls fn on every Next call. fn must be
// safe for concurrent use. Panics if fn is nil.
func NewFeederFunc[T any](fn func() T) *Feeder[T] {
	if fn == nil {
		panic("pulse: NewFeederFunc requires a non-nil function")
	}
	return &Feeder[T]{fn: fn}
}

// Next returns the next value. For slice-backed feeders it cycles round-robin;
// for function-backed feeders it calls the function.
func (f *Feeder[T]) Next() T {
	if f.fn != nil {
		return f.fn()
	}
	i := f.idx.Add(1) - 1
	return f.items[i%uint64(len(f.items))]
}
