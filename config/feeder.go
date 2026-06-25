package config

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"regexp"
	"strings"
	"sync"

	pulse "algoryn.io/pulse"
)

// maxFeederBytes caps the size of a feeder data file to bound memory. Feeder
// files hold external test data (CSV/JSONL), so the limit is more generous than
// the 1 MiB config-file cap.
const maxFeederBytes = 50 << 20 // 50 MiB

var (
	errFeederPathRequired   = errors.New("config: feeder.path is required")
	errFeederUnknownFormat  = errors.New("config: feeder.format must be \"csv\" or \"jsonl\"")
	errFeederUnknownMode    = errors.New("config: feeder.mode must be \"round-robin\" or \"random\"")
	errFeederNoRows         = errors.New("config: feeder file contains no data rows")
	errFeederHeaderTemplate = errors.New("config: feeder variables ({{...}}) are not supported in target.headers")
	errFeederTooLarge       = fmt.Errorf("config: feeder file must not exceed %d bytes", maxFeederBytes)
	errFeederDistributed    = errors.New("config: feeder is not supported in distributed mode (workers); the data file is local to the coordinator")
)

// feederConfig declares a data source whose rows parameterize the built-in HTTP
// scenario. Each scenario iteration draws the next row and substitutes
// {{variable}} placeholders in the target URL and body.
//
// The {{...}} delimiter is used (not ${...}) because ${...} is already consumed
// by environment-variable interpolation when the YAML file is loaded.
type feederConfig struct {
	Format string `yaml:"format"` // "csv" | "jsonl"
	Path   string `yaml:"path"`
	Mode   string `yaml:"mode"` // "round-robin" (default) | "random"
	Seed   *int64 `yaml:"seed"` // optional seed for random mode
}

var feederVarPattern = regexp.MustCompile(`\{\{\s*([\w.-]+)\s*\}\}`)

// feederPlaceholders returns the distinct variable names referenced by a
// template string, in first-seen order.
func feederPlaceholders(s string) []string {
	matches := feederVarPattern.FindAllStringSubmatch(s, -1)
	if matches == nil {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var names []string
	for _, m := range matches {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// substituteVars replaces every {{name}} placeholder in tmpl with row[name].
// It returns an error naming the first variable absent from the row.
func substituteVars(tmpl string, row map[string]string) (string, error) {
	var missing string
	out := feederVarPattern.ReplaceAllStringFunc(tmpl, func(match string) string {
		name := feederVarPattern.FindStringSubmatch(match)[1]
		v, ok := row[name]
		if !ok {
			if missing == "" {
				missing = name
			}
			return match
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("config: feeder row is missing variable %q", missing)
	}
	return out, nil
}

// loadFeederRows reads and parses the feeder file into rows of string-valued
// variables. CSV uses the header row for column names; JSONL treats each line as
// a flat JSON object whose scalar values become strings.
func loadFeederRows(format, path string) ([]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: open feeder file: %w", err)
	}
	defer func() { _ = f.Close() }()

	limited := io.LimitReader(f, maxFeederBytes+1)
	switch format {
	case "csv":
		return parseCSVRows(limited)
	case "jsonl":
		return parseJSONLRows(limited)
	default:
		return nil, errFeederUnknownFormat
	}
}

func parseCSVRows(r io.Reader) ([]map[string]string, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate ragged rows; missing columns become absent vars
	header, err := cr.Read()
	if err == io.EOF {
		return nil, errFeederNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("config: read feeder header: %w", err)
	}
	var total int64
	var rows []map[string]string
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("config: read feeder row: %w", err)
		}
		row := make(map[string]string, len(header))
		for i, col := range header {
			if i < len(rec) {
				row[col] = rec[i]
			}
		}
		rows = append(rows, row)
		if total++; total > maxFeederRows {
			return nil, errFeederTooLarge
		}
	}
	if len(rows) == 0 {
		return nil, errFeederNoRows
	}
	return rows, nil
}

func parseJSONLRows(r io.Reader) ([]map[string]string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxFeederBytes)
	var rows []map[string]string
	var n int64
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return nil, fmt.Errorf("config: parse JSONL row %d: %w", n+1, err)
		}
		row := make(map[string]string, len(obj))
		for k, v := range obj {
			row[k] = scalarToString(v)
		}
		rows = append(rows, row)
		if n++; n > maxFeederRows {
			return nil, errFeederTooLarge
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("config: read feeder file: %w", err)
	}
	if len(rows) == 0 {
		return nil, errFeederNoRows
	}
	return rows, nil
}

// maxFeederRows bounds row count independently of bytes so a pathological file
// cannot exhaust memory through many tiny rows.
const maxFeederRows = 5_000_000

// scalarToString renders a JSON scalar as text. Strings pass through; numbers
// and booleans use their default formatting; nested objects/arrays and null
// become empty strings (only scalar substitution is supported).
func scalarToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	case map[string]any, []any:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

// buildRowFeeder returns a pulse.Feeder over rows according to mode. The default
// (round-robin) cycles deterministically; random draws with replacement using a
// seeded source for reproducibility.
func buildRowFeeder(rows []map[string]string, mode string, seed *int64) *pulse.Feeder[map[string]string] {
	switch mode {
	case "random":
		var mu sync.Mutex
		var s int64
		if seed != nil {
			s = *seed
		}
		rng := rand.New(rand.NewPCG(uint64(s), uint64(s)^0x9e3779b97f4a7c15))
		return pulse.NewFeederFunc(func() map[string]string {
			mu.Lock()
			i := rng.IntN(len(rows))
			mu.Unlock()
			return rows[i]
		})
	default: // round-robin
		return pulse.NewFeeder(rows)
	}
}
