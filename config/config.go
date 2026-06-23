package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	pulse "algoryn.io/pulse"
	"algoryn.io/pulse/transport"
	"gopkg.in/yaml.v3"
)

var (
	errEmptyTargetMethod     = errors.New("config: target method is required")
	errEmptyTargetURL        = errors.New("config: target url is required")
	errUnsupportedMethod     = errors.New("config: unsupported target method")
	errNegativeTargetTimeout = errors.New("config: target timeout must not be negative")
	errInvalidTargetURL      = errors.New("config: target url must be an absolute http or https URL")
)

const maxConfigBytes = 1 << 20

type httpClient interface {
	Get(ctx context.Context, url string) (int, error)
	Post(ctx context.Context, url string, body io.Reader) (int, error)
	Do(ctx context.Context, method, url string, body io.Reader) (int, error)
}

type fileConfig struct {
	Phases           []phaseConfig    `yaml:"phases"`
	Target           targetConfig     `yaml:"target"`
	MaxConcurrency   int              `yaml:"maxConcurrency"`
	SaturationPolicy string           `yaml:"saturationPolicy"`
	Thresholds       thresholdsConfig `yaml:"thresholds"`
	Abort            abortConfig      `yaml:"abort"`
	Percentiles      []float64        `yaml:"percentiles"`
	Reporting        reportingConfig  `yaml:"reporting"`
	Seed             *int64           `yaml:"seed"`
	// Workers is an optional list of distributed worker addresses ("host:port").
	// When set, `pulse run` fans out the load test to these workers instead of
	// executing locally. Workers must be running `pulse worker --addr <addr>`.
	Workers []string `yaml:"workers"`
}

type phaseConfig struct {
	Type          string   `yaml:"type"`
	Duration      duration `yaml:"duration"`
	ArrivalRate   int      `yaml:"arrivalRate"`
	From          int      `yaml:"from"`
	To            int      `yaml:"to"`
	Steps         int      `yaml:"steps"`
	SpikeAt       duration `yaml:"spikeAt"`
	SpikeDuration duration `yaml:"spikeDuration"`
}

type targetConfig struct {
	Method              string            `yaml:"method"`
	URL                 string            `yaml:"url"`
	Body                string            `yaml:"body"`
	Headers             map[string]string `yaml:"headers"`
	Timeout             duration          `yaml:"timeout"`
	MaxIdleConns        int               `yaml:"maxIdleConns"`
	MaxIdleConnsPerHost int               `yaml:"maxIdleConnsPerHost"`
	DisableKeepAlives   bool              `yaml:"disableKeepAlives"`
}

type thresholdsConfig struct {
	ErrorRate      float64  `yaml:"errorRate"`
	MaxMeanLatency duration `yaml:"maxMeanLatency"`
	MaxP95Latency  duration `yaml:"maxP95Latency"`
	MaxP99Latency  duration `yaml:"maxP99Latency"`
	MaxDroppedRate float64  `yaml:"maxDroppedRate"`
}

// abortConfig configures fail-fast: stop the run early when a reporting
// interval breaches a limit. Requires reporting.interval > 0.
type abortConfig struct {
	MaxErrorRate float64  `yaml:"maxErrorRate"`
	MaxP99       duration `yaml:"maxP99"`
	MinRequests  int64    `yaml:"minRequests"`
}

type reportingConfig struct {
	Interval duration `yaml:"interval"`
}

type duration struct {
	time.Duration
}

var newHTTPClient = func(cfg fileConfig) httpClient {
	return transport.NewHTTPClientWith(transport.HTTPClientConfig{
		Timeout:             cfg.Target.Timeout.Duration,
		Headers:             cfg.Target.Headers,
		MaxIdleConns:        cfg.Target.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.Target.MaxIdleConnsPerHost,
		DisableKeepAlives:   cfg.Target.DisableKeepAlives,
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
	file, err := os.Open(path)
	if err != nil {
		return pulse.Test{}, err
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return pulse.Test{}, err
	}
	if len(data) > maxConfigBytes {
		return pulse.Test{}, errors.New("config: file must not exceed 1MiB")
	}

	data, err = expandEnv(data)
	if err != nil {
		return pulse.Test{}, err
	}

	var cfg fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return pulse.Test{}, err
	}

	method := strings.ToUpper(strings.TrimSpace(cfg.Target.Method))

	// Validate target-specific fields (method, URL, timeout). Phase, threshold,
	// concurrency, and reporting validation is delegated to pulse.ValidateConfig
	// below, which avoids duplicating those rules here.
	if err := validateConfig(cfg, method); err != nil {
		return pulse.Test{}, err
	}

	pulseCfg := pulse.Config{
		Phases:           toPulsePhases(cfg.Phases),
		MaxConcurrency:   cfg.MaxConcurrency,
		Seed:             cfg.Seed,
		SaturationPolicy: pulse.SaturationPolicy(strings.ToLower(strings.TrimSpace(cfg.SaturationPolicy))),
		Thresholds: pulse.Thresholds{
			ErrorRate:      cfg.Thresholds.ErrorRate,
			MaxMeanLatency: cfg.Thresholds.MaxMeanLatency.Duration,
			MaxP95Latency:  cfg.Thresholds.MaxP95Latency.Duration,
			MaxP99Latency:  cfg.Thresholds.MaxP99Latency.Duration,
			MaxDroppedRate: cfg.Thresholds.MaxDroppedRate,
		},
		Abort: pulse.AbortConfig{
			MaxErrorRate: cfg.Abort.MaxErrorRate,
			MaxP99:       cfg.Abort.MaxP99.Duration,
			MinRequests:  cfg.Abort.MinRequests,
		},
		Percentiles: cfg.Percentiles,
		Reporting: pulse.ReportingConfig{
			Interval: cfg.Reporting.Interval.Duration,
		},
		Workers: cfg.Workers,
		// Always populate DistributedHTTPScenario so that distributed runs
		// (workers: [...] in YAML or --workers on CLI) can forward the target
		// to CLI workers that have no pre-registered scenario.
		DistributedHTTPScenario: &pulse.HTTPScenarioConfig{
			URL:     cfg.Target.URL,
			Method:  method,
			Headers: cfg.Target.Headers,
			Body:    cfg.Target.Body,
		},
	}

	if err := pulse.ValidateConfig(pulseCfg); err != nil {
		return pulse.Test{}, err
	}

	client := newHTTPClient(cfg)
	test := pulse.Test{
		Config: pulseCfg,
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

// validateConfig checks the target-specific fields of a YAML config: the HTTP
// method, URL (must be absolute http/https), and timeout. All other validation
// (phases, thresholds, concurrency, saturation policy, reporting) is delegated
// to pulse.ValidateConfig, which is called after the pulse.Config is built in
// Load.
func validateConfig(cfg fileConfig, method string) error {
	if cfg.Target.Timeout.Duration < 0 {
		return errNegativeTargetTimeout
	}
	if method == "" {
		return errEmptyTargetMethod
	}
	if strings.TrimSpace(cfg.Target.URL) == "" {
		return errEmptyTargetURL
	}
	targetURL, err := url.Parse(cfg.Target.URL)
	if err != nil || targetURL.Host == "" ||
		(targetURL.Scheme != "http" && targetURL.Scheme != "https") {
		return errInvalidTargetURL
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
			Type:          pulse.PhaseType(strings.ToLower(strings.TrimSpace(phases[i].Type))),
			Duration:      phases[i].Duration.Duration,
			ArrivalRate:   phases[i].ArrivalRate,
			From:          phases[i].From,
			To:            phases[i].To,
			Steps:         phases[i].Steps,
			SpikeAt:       phases[i].SpikeAt.Duration,
			SpikeDuration: phases[i].SpikeDuration.Duration,
		}
	}

	return result
}
