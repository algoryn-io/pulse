package pulse

import (
	"errors"
	"testing"
	"time"
)

func baseStressConfig() Config {
	return Config{
		Phases:    []Phase{{Type: PhaseTypeConstant, Duration: time.Minute, ArrivalRate: 50}},
		Reporting: ReportingConfig{Interval: time.Second},
		Stress:    StressConfig{StepRPS: 25, MaxP99: 250 * time.Millisecond},
	}
}

func TestValidateStressRequiresInterval(t *testing.T) {
	cfg := baseStressConfig()
	cfg.Reporting.Interval = 0
	if err := ValidateConfig(cfg); !errors.Is(err, errStressRequiresInterval) {
		t.Fatalf("got %v, want errStressRequiresInterval", err)
	}
}

func TestValidateStressMutuallyExclusiveWithAdaptive(t *testing.T) {
	cfg := baseStressConfig()
	cfg.Adaptive = AdaptiveConfig{MaxErrorRate: 0.1}
	if err := ValidateConfig(cfg); !errors.Is(err, errStressAdaptiveExclusive) {
		t.Fatalf("got %v, want errStressAdaptiveExclusive", err)
	}
}

func TestValidateStressRejectedInDistributedMode(t *testing.T) {
	cfg := baseStressConfig()
	cfg.Workers = []string{"127.0.0.1:9300"}
	if err := ValidateConfig(cfg); !errors.Is(err, errStressDistributedUnsupported) {
		t.Fatalf("got %v, want errStressDistributedUnsupported", err)
	}
}

func TestValidateStressInvalidRates(t *testing.T) {
	cfg := baseStressConfig()
	cfg.Stress.MaxErrorRate = 1.5
	if err := ValidateConfig(cfg); !errors.Is(err, errStressInvalidErrorRate) {
		t.Fatalf("got %v, want errStressInvalidErrorRate", err)
	}

	cfg = baseStressConfig()
	cfg.Stress.MaxP99 = -1
	if err := ValidateConfig(cfg); !errors.Is(err, errStressInvalidP99) {
		t.Fatalf("got %v, want errStressInvalidP99", err)
	}

	cfg = baseStressConfig()
	cfg.Stress.StepRPS = -5
	if err := ValidateConfig(cfg); !errors.Is(err, errStressNegativeRate) {
		t.Fatalf("got %v, want errStressNegativeRate", err)
	}
}

func TestValidateStressValid(t *testing.T) {
	if err := ValidateConfig(baseStressConfig()); err != nil {
		t.Fatalf("valid stress config rejected: %v", err)
	}
}
