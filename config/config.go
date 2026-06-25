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
	errInvalidCheckStatus    = errors.New("config: check status must be a valid HTTP status code (100-599)")
	errEmptyCheckHeaderKey   = errors.New("config: check headerEquals key must not be empty")
)

const maxConfigBytes = 1 << 20

type httpClient interface {
	Get(ctx context.Context, url string) (int, error)
	Post(ctx context.Context, url string, body io.Reader) (int, error)
	Do(ctx context.Context, method, url string, body io.Reader) (int, error)
	DoWithResponse(ctx context.Context, method, url string, body io.Reader) (*transport.Response, error)
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
	// WorkerWeights optionally assigns a relative capacity to each worker, in the
	// same order as Workers. When empty, workers are weighted equally.
	WorkerWeights []int `yaml:"workerWeights"`
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
	Checks              *checksConfig     `yaml:"checks"`
}

// checksConfig declares response assertions evaluated after each request for the
// built-in HTTP scenario. A failing check marks the request as failed and is
// counted under the "check_failed" error category. When no `status` check is
// set, the default behaviour is preserved: a response status >= 400 still fails.
type checksConfig struct {
	Status       int               `yaml:"status"`
	HeaderEquals map[string]string `yaml:"headerEquals"`
	BodyContains []string          `yaml:"bodyContains"`
	JSONEquals   map[string]string `yaml:"jsonEquals"`
}

func (c *checksConfig) toChecks() transport.Checks {
	return transport.Checks{
		Status:       c.Status,
		HeaderEquals: c.HeaderEquals,
		BodyContains: c.BodyContains,
		JSONEquals:   c.JSONEquals,
	}
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
		Workers:       cfg.Workers,
		WorkerWeights: cfg.WorkerWeights,
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
	scenario := buildScenario(client, method, cfg.Target.URL, cfg.Target.Body, cfg.Target.Checks)
	test := pulse.Test{
		Config:   pulseCfg,
		Scenario: scenario,
	}

	return test, nil
}

// buildScenario returns the scenario function for the built-in HTTP target.
// When checks are configured it uses DoWithResponse so the full response can be
// asserted; otherwise it takes the lighter Get/Post/Do path.
func buildScenario(client httpClient, method, targetURL, body string, checksCfg *checksConfig) func(context.Context) (int, error) {
	if checksCfg == nil {
		return func(ctx context.Context) (int, error) {
			switch method {
			case http.MethodGet:
				return client.Get(ctx, targetURL)
			case http.MethodPost:
				return client.Post(ctx, targetURL, strings.NewReader(body))
			default:
				var r io.Reader
				if body != "" {
					r = strings.NewReader(body)
				}
				return client.Do(ctx, method, targetURL, r)
			}
		}
	}

	checks := checksCfg.toChecks()
	return func(ctx context.Context) (int, error) {
		var r io.Reader
		if body != "" {
			r = strings.NewReader(body)
		}
		resp, err := client.DoWithResponse(ctx, method, targetURL, r)
		if err != nil {
			return 0, err
		}
		if err := checks.Run(resp); err != nil {
			// The request completed; report its status code so status-count
			// metrics stay accurate, but mark it failed via the check error.
			return resp.StatusCode, err
		}
		// Preserve the default "4xx/5xx is a failure" semantics unless the user
		// took explicit control of the status via a status check.
		if !checks.HasStatus() && resp.StatusCode >= http.StatusBadRequest {
			return resp.StatusCode, &transport.HTTPStatusError{StatusCode: resp.StatusCode}
		}
		return resp.StatusCode, nil
	}
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
	default:
		return fmt.Errorf("%w: %s", errUnsupportedMethod, method)
	}
	if c := cfg.Target.Checks; c != nil {
		if c.Status != 0 && (c.Status < 100 || c.Status > 599) {
			return fmt.Errorf("%w: %d", errInvalidCheckStatus, c.Status)
		}
		for k := range c.HeaderEquals {
			if strings.TrimSpace(k) == "" {
				return errEmptyCheckHeaderKey
			}
		}
	}
	return nil
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
