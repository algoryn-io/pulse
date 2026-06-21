package pulse_test

import (
	"sync"
	"testing"

	pulse "algoryn.io/pulse"
)

func TestExtractorGetBeforeSetReturnsFalse(t *testing.T) {
	var e pulse.Extractor[string]
	_, ok := e.Get()
	if ok {
		t.Fatal("expected ok=false before Set")
	}
}

func TestExtractorSetAndGet(t *testing.T) {
	var e pulse.Extractor[string]
	e.Set("tok-abc")
	val, ok := e.Get()
	if !ok || val != "tok-abc" {
		t.Fatalf("expected (tok-abc, true), got (%q, %v)", val, ok)
	}
}

func TestExtractorMustGet(t *testing.T) {
	var e pulse.Extractor[int]
	e.Set(42)
	if got := e.MustGet(); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestExtractorMustGetPanicsBeforeSet(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic before Set")
		}
	}()
	var e pulse.Extractor[string]
	e.MustGet()
}

func TestExtractorOverwrite(t *testing.T) {
	var e pulse.Extractor[string]
	e.Set("first")
	e.Set("second")
	val, _ := e.Get()
	if val != "second" {
		t.Fatalf("expected second, got %q", val)
	}
}

func TestExtractorConcurrentSetGet(t *testing.T) {
	var e pulse.Extractor[int]
	var wg sync.WaitGroup
	const n = 100

	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			e.Set(i)
			e.Get()
		}(i)
	}
	wg.Wait()
	_, ok := e.Get()
	if !ok {
		t.Fatal("expected ok=true after concurrent writes")
	}
}

func TestExtractorWorksWithStructs(t *testing.T) {
	type Token struct {
		Value  string
		Expiry int64
	}
	var e pulse.Extractor[Token]
	e.Set(Token{Value: "abc", Expiry: 9999})
	got, ok := e.Get()
	if !ok || got.Value != "abc" || got.Expiry != 9999 {
		t.Fatalf("unexpected value: %+v", got)
	}
}
