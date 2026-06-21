package pulse_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	pulse "algoryn.io/pulse"
)

var ctx = context.Background()

func ok(code int) pulse.Scenario {
	return func(_ context.Context) (int, error) { return code, nil }
}

func fail(code int, err error) pulse.Scenario {
	return func(_ context.Context) (int, error) { return code, err }
}

// -- Chain --

func TestChainRunsAllSteps(t *testing.T) {
	var order []int
	step := func(n int) pulse.Scenario {
		return func(_ context.Context) (int, error) {
			order = append(order, n)
			return 200, nil
		}
	}
	code, err := pulse.Sequence(step(1), step(2), step(3))(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 200 {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("unexpected execution order: %v", order)
	}
}

func TestChainStopsOnFirstError(t *testing.T) {
	sentinel := errors.New("step failed")
	var called bool
	third := func(_ context.Context) (int, error) {
		called = true
		return 200, nil
	}
	code, err := pulse.Sequence(ok(200), fail(500, sentinel), third)(ctx)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if code != 500 {
		t.Fatalf("expected status from failing step (500), got %d", code)
	}
	if called {
		t.Fatal("third step must not run after a failure")
	}
}

func TestChainReturnsLastStatusCode(t *testing.T) {
	code, err := pulse.Sequence(ok(200), ok(201), ok(204))(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 204 {
		t.Fatalf("expected last step status 204, got %d", code)
	}
}

func TestChainSingleStep(t *testing.T) {
	code, err := pulse.Sequence(ok(202))(ctx)
	if err != nil || code != 202 {
		t.Fatalf("expected (202, nil), got (%d, %v)", code, err)
	}
}

func TestChainPanicsOnEmpty(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for empty steps")
		}
	}()
	pulse.Sequence()
}

func TestChainRespectsContextCancellation(t *testing.T) {
	cctx, cancel := context.WithCancel(ctx)
	cancel()

	scenario := pulse.Sequence(func(c context.Context) (int, error) {
		return 0, c.Err()
	})
	_, err := scenario(cctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// -- Flow --

func TestFlowRunsAllSteps(t *testing.T) {
	var order []string
	named := func(name string) pulse.Step {
		return pulse.Step{Name: name, Do: func(_ context.Context) (int, error) {
			order = append(order, name)
			return 200, nil
		}}
	}
	_, err := pulse.Flow(named("login"), named("fetch"), named("logout"))(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(order, ",") != "login,fetch,logout" {
		t.Fatalf("unexpected order: %v", order)
	}
}

func TestFlowWrapsErrorWithStepName(t *testing.T) {
	sentinel := errors.New("unauthorized")
	_, err := pulse.Flow(
		pulse.Step{Name: "login", Do: fail(401, sentinel)},
		pulse.Step{Name: "fetch", Do: ok(200)},
	)(ctx)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), "login") {
		t.Fatalf("error should contain step name, got: %v", err)
	}
}

func TestFlowStopsOnFirstError(t *testing.T) {
	var fetchCalled bool
	_, err := pulse.Flow(
		pulse.Step{Name: "login", Do: fail(500, errors.New("server error"))},
		pulse.Step{Name: "fetch", Do: func(_ context.Context) (int, error) {
			fetchCalled = true
			return 200, nil
		}},
	)(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if fetchCalled {
		t.Fatal("fetch must not run after login failure")
	}
}

func TestFlowUnnamedStepDoesNotWrapError(t *testing.T) {
	sentinel := errors.New("bare error")
	_, err := pulse.Flow(pulse.Step{Do: fail(500, sentinel)})(ctx)
	if err.Error() != sentinel.Error() {
		t.Fatalf("unnamed step should not wrap error, got: %v", err)
	}
}

func TestFlowPanicsOnEmpty(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for empty steps")
		}
	}()
	pulse.Flow()
}

func TestFlowPanicsOnNilDo(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil Do")
		}
	}()
	pulse.Flow(pulse.Step{Name: "bad", Do: nil})
}
