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
	"path/filepath"
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
	Stress           stressConfig     `yaml:"stress"`
	Percentiles      []float64        `yaml:"percentiles"`
	Reporting        reportingConfig  `yaml:"reporting"`
	Feeder           *feederConfig    `yaml:"feeder"`
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

// toDistributedChecks adapts the YAML checks into the pointer form carried by
// pulse.HTTPScenarioConfig for forwarding to distributed workers.
func toDistributedChecks(c *checksConfig) *transport.Checks {
	if c == nil {
		return nil
	}
	checks := c.toChecks()
	return &checks
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

// stressConfig configures ramp-to-failure capacity discovery: climb the arrival
// rate until a failure threshold is breached. Requires reporting.interval > 0
// and is mutually exclusive with adaptive / distributed mode.
type stressConfig struct {
	StepRPS            int      `yaml:"stepRPS"`
	MaxRPS             int      `yaml:"maxRPS"`
	MaxErrorRate       float64  `yaml:"maxErrorRate"`
	MaxP99             duration `yaml:"maxP99"`
	SustainedIntervals int      `yaml:"sustainedIntervals"`
	MinRequests        int64    `yaml:"minRequests"`
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
		Stress: pulse.StressConfig{
			StepRPS:            cfg.Stress.StepRPS,
			MaxRPS:             cfg.Stress.MaxRPS,
			MaxErrorRate:       cfg.Stress.MaxErrorRate,
			MaxP99:             cfg.Stress.MaxP99.Duration,
			SustainedIntervals: cfg.Stress.SustainedIntervals,
			MinRequests:        cfg.Stress.MinRequests,
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
			Checks:  toDistributedChecks(cfg.Target.Checks),
		},
	}

	if err := pulse.ValidateConfig(pulseCfg); err != nil {
		return pulse.Test{}, err
	}

	feeder, err := loadFeeder(cfg, filepath.Dir(path))
	if err != nil {
		return pulse.Test{}, err
	}

	client := newHTTPClient(cfg)
	scenario := buildScenario(client, method, cfg.Target.URL, cfg.Target.Body, cfg.Target.Checks, feeder)
	test := pulse.Test{
		Config:   pulseCfg,
		Scenario: scenario,
	}

	return test, nil
}

// loadFeeder validates the feeder config, loads its rows, checks that every
// {{placeholder}} in the URL and body resolves for every row (and that none
// appear in headers), and returns a row feeder. It returns (nil, nil) when no
// feeder is configured.
func loadFeeder(cfg fileConfig, configDir string) (*pulse.Feeder[map[string]string], error) {
	fc := cfg.Feeder
	if fc == nil {
		return nil, nil
	}
	if strings.TrimSpace(fc.Path) == "" {
		return nil, errFeederPathRequired
	}
	if fc.Format != "csv" && fc.Format != "jsonl" {
		return nil, errFeederUnknownFormat
	}
	if fc.Mode != "" && fc.Mode != "round-robin" && fc.Mode != "random" {
		return nil, errFeederUnknownMode
	}
	if len(cfg.Workers) > 0 {
		return nil, errFeederDistributed
	}

	// Header values cannot be templated (the client applies them once), so reject
	// placeholders there instead of silently sending a literal "{{var}}".
	for _, v := range cfg.Target.Headers {
		if len(feederPlaceholders(v)) > 0 {
			return nil, errFeederHeaderTemplate
		}
	}

	// Feeder paths are resolved relative to the config file's directory (not the
	// process CWD), so a config and its data file can live together.
	dataPath := fc.Path
	if !filepath.IsAbs(dataPath) {
		dataPath = filepath.Join(configDir, dataPath)
	}
	rows, err := loadFeederRows(fc.Format, dataPath)
	if err != nil {
		return nil, err
	}

	// Every variable referenced in the URL or body must exist in every row, so a
	// run never silently sends an unresolved placeholder.
	needed := append(feederPlaceholders(cfg.Target.URL), feederPlaceholders(cfg.Target.Body)...)
	for _, name := range needed {
		for i, row := range rows {
			if _, ok := row[name]; !ok {
				return nil, fmt.Errorf("config: feeder row %d is missing variable %q referenced in the target", i+1, name)
			}
		}
	}

	seed := fc.Seed
	if seed == nil {
		seed = cfg.Seed // fall back to the global config seed for reproducibility
	}
	return buildRowFeeder(rows, fc.Mode, seed), nil
}

// buildScenario returns the scenario function for the built-in HTTP target.
// When checks are configured it uses DoWithResponse so the full response can be
// asserted; otherwise it takes the lighter Get/Post/Do path. When feeder is
// non-nil, each iteration draws a row and substitutes {{variable}} placeholders
// in the URL and body.
func buildScenario(client httpClient, method, targetURL, body string, checksCfg *checksConfig, feeder *pulse.Feeder[map[string]string]) func(context.Context) (int, error) {
	var checks transport.Checks
	hasChecks := checksCfg != nil
	if hasChecks {
		checks = checksCfg.toChecks()
	}

	// do issues one request to a (possibly templated) URL/body and applies checks.
	do := func(ctx context.Context, url, bodyStr string) (int, error) {
		if !hasChecks {
			switch method {
			case http.MethodGet:
				return client.Get(ctx, url)
			case http.MethodPost:
				return client.Post(ctx, url, strings.NewReader(bodyStr))
			default:
				return client.Do(ctx, method, url, bodyReader(bodyStr))
			}
		}
		resp, err := client.DoWithResponse(ctx, method, url, bodyReader(bodyStr))
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

	if feeder == nil {
		return func(ctx context.Context) (int, error) {
			return do(ctx, targetURL, body)
		}
	}
	return func(ctx context.Context) (int, error) {
		row := feeder.Next()
		url, err := substituteVars(targetURL, row)
		if err != nil {
			return 0, err
		}
		b, err := substituteVars(body, row)
		if err != nil {
			return 0, err
		}
		return do(ctx, url, b)
	}
}

func bodyReader(s string) io.Reader {
	if s == "" {
		return nil
	}
	return strings.NewReader(s)
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
