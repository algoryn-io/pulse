package pulse

import (
	"fmt"
	"sync"
)

// Extractor is a thread-safe value container for passing data extracted from
// one scenario step to subsequent steps (e.g. an auth token from a login
// response used in all following requests).
//
// Declare one extractor per value to correlate, share it across steps via
// closure, and call Set inside the producing step and Get or MustGet in the
// consuming steps.
type Extractor[T any] struct {
	mu  sync.RWMutex
	val T
	ok  bool
}

// Set stores v. Safe for concurrent use.
func (e *Extractor[T]) Set(v T) {
	e.mu.Lock()
	e.val = v
	e.ok = true
	e.mu.Unlock()
}

// Get returns the stored value and true, or the zero value and false if Set
// has not been called yet.
func (e *Extractor[T]) Get() (T, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.val, e.ok
}

// MustGet returns the stored value. Panics if Set has not been called.
func (e *Extractor[T]) MustGet() T {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !e.ok {
		panic(fmt.Sprintf("pulse: Extractor[%T].MustGet called before Set", e.val))
	}
	return e.val
}
