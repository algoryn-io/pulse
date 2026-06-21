package pulse_test

import (
	"sync"
	"sync/atomic"
	"testing"

	pulse "algoryn.io/pulse"
)

func TestFeederCyclesRoundRobin(t *testing.T) {
	f := pulse.NewFeeder([]string{"a", "b", "c"})
	want := []string{"a", "b", "c", "a", "b", "c"}
	for i, w := range want {
		if got := f.Next(); got != w {
			t.Fatalf("call %d: expected %q, got %q", i, w, got)
		}
	}
}

func TestFeederSingleItem(t *testing.T) {
	f := pulse.NewFeeder([]int{42})
	for range 5 {
		if got := f.Next(); got != 42 {
			t.Fatalf("expected 42, got %d", got)
		}
	}
}

func TestFeederDoesNotMutateInput(t *testing.T) {
	items := []string{"x", "y"}
	f := pulse.NewFeeder(items)
	items[0] = "mutated"
	if got := f.Next(); got != "x" {
		t.Fatalf("feeder should not reflect mutation of original slice, got %q", got)
	}
}

func TestFeederPanicsOnEmpty(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for empty items")
		}
	}()
	pulse.NewFeeder([]string{})
}

func TestFeederConcurrentAccess(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	f := pulse.NewFeeder(items)

	const goroutines = 50
	const callsEach = 200
	var wg sync.WaitGroup
	var total atomic.Int64

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range callsEach {
				v := f.Next()
				if v < 1 || v > 5 {
					t.Errorf("out of range value: %d", v)
				}
				total.Add(int64(v))
			}
		}()
	}
	wg.Wait()

	if total.Load() == 0 {
		t.Fatal("expected non-zero total")
	}
}

func TestFeederFuncCallsOnEveryNext(t *testing.T) {
	var calls int
	f := pulse.NewFeederFunc(func() int {
		calls++
		return calls
	})
	for i := 1; i <= 5; i++ {
		if got := f.Next(); got != i {
			t.Fatalf("expected %d, got %d", i, got)
		}
	}
	if calls != 5 {
		t.Fatalf("expected 5 calls, got %d", calls)
	}
}

func TestFeederFuncPanicsOnNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil fn")
		}
	}()
	pulse.NewFeederFunc[int](nil)
}

func TestFeederWithStructs(t *testing.T) {
	type User struct {
		ID   int
		Name string
	}
	users := []User{{1, "alice"}, {2, "bob"}}
	f := pulse.NewFeeder(users)

	u := f.Next()
	if u.ID != 1 || u.Name != "alice" {
		t.Fatalf("unexpected first user: %+v", u)
	}
	u = f.Next()
	if u.ID != 2 || u.Name != "bob" {
		t.Fatalf("unexpected second user: %+v", u)
	}
	u = f.Next()
	if u.ID != 1 {
		t.Fatalf("expected wrap-around to first user, got: %+v", u)
	}
}
