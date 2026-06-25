package pulse

import (
	"testing"

	"algoryn.io/pulse/transport"
)

func TestToDistributedHTTPScenarioForwardsChecks(t *testing.T) {
	cfg := &HTTPScenarioConfig{
		URL:    "http://api.internal/health",
		Method: "GET",
		Checks: &transport.Checks{
			Status:       200,
			HeaderEquals: map[string]string{"Content-Type": "application/json"},
			BodyContains: []string{"ok"},
			JSONEquals:   map[string]string{"status": "healthy"},
		},
	}

	got := toDistributedHTTPScenario(cfg)
	if got == nil || got.Checks == nil {
		t.Fatal("expected checks to be forwarded to the wire scenario")
	}
	if got.Checks.Status != 200 {
		t.Errorf("status = %d, want 200", got.Checks.Status)
	}
	if got.Checks.HeaderEquals["Content-Type"] != "application/json" {
		t.Errorf("headerEquals not forwarded: %#v", got.Checks.HeaderEquals)
	}
	if len(got.Checks.BodyContains) != 1 || got.Checks.BodyContains[0] != "ok" {
		t.Errorf("bodyContains not forwarded: %#v", got.Checks.BodyContains)
	}
	if got.Checks.JSONEquals["status"] != "healthy" {
		t.Errorf("jsonEquals not forwarded: %#v", got.Checks.JSONEquals)
	}
}

func TestToDistributedHTTPScenarioNilChecks(t *testing.T) {
	got := toDistributedHTTPScenario(&HTTPScenarioConfig{URL: "http://x", Method: "GET"})
	if got == nil {
		t.Fatal("expected a scenario")
	}
	if got.Checks != nil {
		t.Fatalf("expected nil checks, got %#v", got.Checks)
	}
}
