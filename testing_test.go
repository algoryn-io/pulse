package pulse

import (
	"context"
	"flag"
	"fmt"
	"testing"
	"time"
)

type fakeT struct {
	testing.TB
	fatalCalled bool
	skipCalled  bool
	logs        []string
}

func (f *fakeT) Helper() {}

func (f *fakeT) Fatalf(format string, args ...any) {
	f.fatalCalled = true
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
}

func (f *fakeT) Logf(format string, args ...any) {
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
}

func (f *fakeT) Skip(args ...any) {
	f.skipCalled = true
}

func TestRunTPassesWithoutThresholds(t *testing.T) {
	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
		},
		Scenario: newHealthyHTTPScenario(t),
	}

	result := RunT(t, test)
	if result.Total <= 0 {
		t.Fatalf("expected Total > 0, got %d", result.Total)
	}
}

func TestRunTFatalsWhenThresholdFails(t *testing.T) {
	tb := &fakeT{}

	test := Test{
		Config: Config{
			Phases: []Phase{
				{Type: PhaseTypeConstant, Duration: 80 * time.Millisecond, ArrivalRate: 20},
			},
			MaxConcurrency: 2,
			Thresholds: Thresholds{
				ErrorRate: 1e-9,
			},
		},
		Scenario: func(context.Context) (int, error) {
			time.Sleep(5 * time.Millisecond)
			return 0, fmt.Errorf("scenario failed")
		},
	}

	result := RunT(tb, test)

	if !tb.fatalCalled {
		t.Fatal("expected Fatalf to be called")
	}
	if result.Total <= 0 {
		t.Fatalf("expected Total > 0, got %d", result.Total)
	}
}

func TestSkipIfShortSkipsInShortMode(t *testing.T) {
	shortFlag := flag.Lookup("test.short")
	if shortFlag == nil {
		t.Fatal("expected test.short flag to be registered")
	}

	original := shortFlag.Value.String()
	if err := shortFlag.Value.Set("true"); err != nil {
		t.Fatalf("set short flag: %v", err)
	}
	defer func() {
		if err := shortFlag.Value.Set(original); err != nil {
			t.Fatalf("restore short flag: %v", err)
		}
	}()

	tb := &fakeT{}
	SkipIfShort(tb)

	if !tb.skipCalled {
		t.Fatal("expected Skip to be called")
	}
}
