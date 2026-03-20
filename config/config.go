package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	pulse "github.com/jmgo38/Pulse"
	"github.com/jmgo38/Pulse/transport"
	"gopkg.in/yaml.v3"
)

var (
	errNoPhases          = errors.New("config: at least one phase is required")
	errEmptyPhaseType    = errors.New("config: phase type is required")
	errEmptyTargetMethod = errors.New("config: target method is required")
	errEmptyTargetURL    = errors.New("config: target url is required")
	errUnsupportedMethod = errors.New("config: unsupported target method")
)

type httpClient interface {
	Get(ctx context.Context, url string) error
	Post(ctx context.Context, url string, body io.Reader) error
}

type fileConfig struct {
	Phases         []phaseConfig `yaml:"phases"`
	Target         targetConfig  `yaml:"target"`
	MaxConcurrency int           `yaml:"maxConcurrency"`
}

type phaseConfig struct {
	Type        string   `yaml:"type"`
	Duration    duration `yaml:"duration"`
	ArrivalRate int      `yaml:"arrivalRate"`
}

type targetConfig struct {
	Method string `yaml:"method"`
	URL    string `yaml:"url"`
	Body   string `yaml:"body"`
}

type duration struct {
	time.Duration
}

var newHTTPClient = func() httpClient {
	return transport.NewHTTPClient()
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

	if err := validateConfig(cfg); err != nil {
		return pulse.Test{}, err
	}

	client := newHTTPClient()
	method := strings.ToUpper(strings.TrimSpace(cfg.Target.Method))
	test := pulse.Test{
		Config: pulse.Config{
			Phases:         toPulsePhases(cfg.Phases),
			MaxConcurrency: cfg.MaxConcurrency,
		},
		Scenario: func(ctx context.Context) error {
			switch method {
			case "GET":
				return client.Get(ctx, cfg.Target.URL)
			case "POST":
				return client.Post(ctx, cfg.Target.URL, strings.NewReader(cfg.Target.Body))
			default:
				return fmt.Errorf("%w: %s", errUnsupportedMethod, method)
			}
		},
	}

	return test, nil
}

func validateConfig(cfg fileConfig) error {
	if len(cfg.Phases) == 0 {
		return errNoPhases
	}

	for _, phase := range cfg.Phases {
		if strings.TrimSpace(phase.Type) == "" {
			return errEmptyPhaseType
		}
	}

	if strings.TrimSpace(cfg.Target.Method) == "" {
		return errEmptyTargetMethod
	}

	if strings.TrimSpace(cfg.Target.URL) == "" {
		return errEmptyTargetURL
	}

	switch strings.ToUpper(strings.TrimSpace(cfg.Target.Method)) {
	case "GET", "POST":
		return nil
	default:
		return fmt.Errorf("%w: %s", errUnsupportedMethod, cfg.Target.Method)
	}
}

func toPulsePhases(phases []phaseConfig) []pulse.Phase {
	result := make([]pulse.Phase, len(phases))
	for i := range phases {
		result[i] = pulse.Phase{
			Type:        pulse.PhaseType(phases[i].Type),
			Duration:    phases[i].Duration.Duration,
			ArrivalRate: phases[i].ArrivalRate,
		}
	}

	return result
}
