package pulse

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUserErrorWrapping(t *testing.T) {
	if UserError(nil) != nil {
		t.Fatal("UserError(nil) should be nil")
	}
	base := errors.New("invalid fixture")
	wrapped := UserError(base)
	if !errors.Is(wrapped, ErrUser) {
		t.Fatal("wrapped error should match ErrUser")
	}
	if !errors.Is(wrapped, base) {
		t.Fatal("wrapped error should still match the original error")
	}
}

// A scenario that returns a UserError is counted under "user_error" in the run
// result, distinct from unknown_error.
func TestUserErrorCountedInResult(t *testing.T) {
	scenario := func(ctx context.Context) (int, error) {
		return 0, UserError(errors.New("business rule violated"))
	}
	res, err := RunContext(context.Background(), Test{
		Config: Config{
			Phases:         []Phase{{Type: PhaseTypeConstant, Duration: 50 * time.Millisecond, ArrivalRate: 100}},
			MaxConcurrency: 10,
		},
		Scenario: scenario,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ErrorCounts["user_error"] == 0 {
		t.Fatalf("expected user_error failures, got %#v", res.ErrorCounts)
	}
	if res.ErrorCounts["unknown_error"] != 0 {
		t.Fatalf("user errors must not fall into unknown_error: %#v", res.ErrorCounts)
	}
	if res.Failed == 0 {
		t.Fatal("user errors should count as failures")
	}
}
