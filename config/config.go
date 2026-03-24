package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	pulse "github.com/jmgo38/Pulse"
	"github.com/jmgo38/Pulse/transport"
	"gopkg.in/yaml.v3"
)

var (
	errNoPhases             = errors.New("config: at least one phase is required")
	errEmptyPhaseType       = errors.New("config: phase type is required")
	errNonPositivePhase     = errors.New("config: phase duration must be positive")
	errNonPositiveRate      = errors.New("config: phase arrival rate must be positive")
	errInvalidRamp          = errors.New("config: ramp phase from and to must be positive")
	errInvalidStep          = errors.New("config: step phase requires positive from, to and steps")
	errUnsupportedPhaseType = errors.New("config: unsupported phase type")
	errEmptyTargetMethod    = errors.New("config: target method is required")
	errEmptyTargetURL       = errors.New("config: target url is required")
	errUnsupportedMethod    = errors.New("config: unsupported target method")
)

type httpClient interface {
	Get(ctx context.Context, url string) (int, error)
	Post(ctx context.Context, url string, body io.Reader) (int, error)
	Do(ctx context.Context, method, url string, body io.Reader) (int, error)
}

type fileConfig struct {
	Phases         []phaseConfig    `yaml:"phases"`
	Target         targetConfig     `yaml:"target"`
	MaxConcurrency int              `yaml:"maxConcurrency"`
	Thresholds     thresholdsConfig `yaml:"thresholds"`
}

type phaseConfig struct {
	Type        string   `yaml:"type"`
	Duration    duration `yaml:"duration"`
	ArrivalRate int      `yaml:"arrivalRate"`
	From        int      `yaml:"from"`
	To          int      `yaml:"to"`
	Steps       int      `yaml:"steps"`
}

type targetConfig struct {
	Method  string            `yaml:"method"`
	URL     string            `yaml:"url"`
	Body    string            `yaml:"body"`
	Headers map[string]string `yaml:"headers"`
	Timeout duration          `yaml:"timeout"`
}

type thresholdsConfig struct {
	ErrorRate      float64  `yaml:"errorRate"`
	MaxMeanLatency duration `yaml:"maxMeanLatency"`
	MaxP95Latency  duration `yaml:"maxP95Latency"`
	MaxP99Latency  duration `yaml:"maxP99Latency"`
}

type duration struct {
	time.Duration
}

var newHTTPClient = func(cfg fileConfig) httpClient {
	return transport.NewHTTPClientWith(transport.HTTPClientConfig{
		Timeout: cfg.Target.Timeout.Duration,
		Headers: cfg.Target.Headers,
	})
}

func (d *duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("config: duration must be a string")
	}

	parsed, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("config: invalid duration %q: %w", node.Value, err)
	}

	d.Duration = parsed
	return nil
}

// Load reads a YAML file and maps it into a Pulse test definition.
func Load(path string) (pulse.Test, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pulse.Test{}, err
	}

	var cfg fileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return pulse.Test{}, err
	}

	method := strings.ToUpper(strings.TrimSpace(cfg.Target.Method))

	if err := validateConfig(cfg, method); err != nil {
		return pulse.Test{}, err
	}

	client := newHTTPClient(cfg)
	test := pulse.Test{
		Config: pulse.Config{
			Phases:         toPulsePhases(cfg.Phases),
			MaxConcurrency: cfg.MaxConcurrency,
			Thresholds: pulse.Thresholds{
				ErrorRate:      cfg.Thresholds.ErrorRate,
				MaxMeanLatency: cfg.Thresholds.MaxMeanLatency.Duration,
				MaxP95Latency:  cfg.Thresholds.MaxP95Latency.Duration,
				MaxP99Latency:  cfg.Thresholds.MaxP99Latency.Duration,
			},
		},
		Scenario: func(ctx context.Context) (int, error) {
			switch method {
			case http.MethodGet:
				return client.Get(ctx, cfg.Target.URL)
			case http.MethodPost:
				return client.Post(ctx, cfg.Target.URL, strings.NewReader(cfg.Target.Body))
			default:
				var body io.Reader
				if cfg.Target.Body != "" {
					body = strings.NewReader(cfg.Target.Body)
				}
				return client.Do(ctx, method, cfg.Target.URL, body)
			}
		},
	}

	return test, nil
}

func validateConfig(cfg fileConfig, method string) error {
	if len(cfg.Phases) == 0 {
		return errNoPhases
	}

	for _, phase := range cfg.Phases {
		if strings.TrimSpace(phase.Type) == "" {
			return errEmptyPhaseType
		}

		if phase.Duration.Duration <= 0 {
			return errNonPositivePhase
		}

		pt := strings.ToLower(strings.TrimSpace(phase.Type))
		switch pt {
		case string(pulse.PhaseTypeRamp):
			if phase.From <= 0 || phase.To <= 0 {
				return errInvalidRamp
			}
		case string(pulse.PhaseTypeConstant):
			if phase.ArrivalRate <= 0 {
				return errNonPositiveRate
			}
		case string(pulse.PhaseTypeStep):
			if phase.From <= 0 || phase.To <= 0 || phase.Steps <= 0 {
				return errInvalidStep
			}
		default:
			return errUnsupportedPhaseType
		}
	}

	if method == "" {
		return errEmptyTargetMethod
	}

	if strings.TrimSpace(cfg.Target.URL) == "" {
		return errEmptyTargetURL
	}

	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
		return nil
	default:
		return fmt.Errorf("%w: %s", errUnsupportedMethod, method)
	}
}

func toPulsePhases(phases []phaseConfig) []pulse.Phase {
	result := make([]pulse.Phase, len(phases))
	for i := range phases {
		result[i] = pulse.Phase{
			Type:        pulse.PhaseType(strings.ToLower(strings.TrimSpace(phases[i].Type))),
			Duration:    phases[i].Duration.Duration,
			ArrivalRate: phases[i].ArrivalRate,
			From:        phases[i].From,
			To:          phases[i].To,
			Steps:       phases[i].Steps,
		}
	}

	return result
}
