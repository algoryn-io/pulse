// Package har imports HTTP Archive (HAR) files as Pulse scenarios.
// HAR files can be exported from browser DevTools (Network tab → Save as HAR).
// Each recorded request becomes a named step in a pulse.Flow, replayed in
// the order they appear in the archive.
//
// WARNING: HAR files often contain authentication headers and session tokens.
// Treat them as sensitive — do not commit them to version control.
package har

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	pulse "algoryn.io/pulse"
)

// skipHeaders are hop-by-hop or connection-level headers that must not be
// forwarded when replaying recorded requests.
var skipHeaders = map[string]struct{}{
	"host":                {},
	"content-length":      {},
	"transfer-encoding":   {},
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"upgrade":             {},
}

// har is the root of the HAR JSON structure.
type har struct {
	Log harLog `json:"log"`
}

type harLog struct {
	Entries []Entry `json:"entries"`
}

// Entry represents a single recorded HTTP transaction.
type Entry struct {
	Request Request `json:"request"`
}

// Request holds the recorded HTTP request data.
type Request struct {
	Method   string   `json:"method"`
	URL      string   `json:"url"`
	Headers  []Header `json:"headers"`
	PostData PostData `json:"postData"`
}

// Header is a name/value pair.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PostData holds the request body recorded in the HAR.
type PostData struct {
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}

// Config controls how HAR entries are imported.
type Config struct {
	// Filter, when non-nil, is called for each entry. Return false to skip it.
	// Use this to exclude static assets, analytics beacons, or third-party requests.
	Filter func(req Request) bool
	// Client is the HTTP client used to replay requests.
	// When nil, a client with a 30 s timeout is used.
	Client *http.Client
}

// LoadFile parses a HAR file at path and returns a Scenario that replays all
// included requests in sequence. See Load for details.
func LoadFile(path string, cfg Config) (pulse.Scenario, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("har: open %q: %w", path, err)
	}
	defer f.Close()
	return Load(f, cfg)
}

// Load parses HAR data from r and returns a Scenario that replays all included
// requests in sequence using pulse.Flow. Each step is named "<METHOD> <URL>"
// so failures are identifiable in the result error map.
//
// Hop-by-hop headers (Host, Content-Length, etc.) are stripped. All other
// recorded headers, including Authorization, are forwarded as-is.
func Load(r io.Reader, cfg Config) (pulse.Scenario, error) {
	var h har
	if err := json.NewDecoder(r).Decode(&h); err != nil {
		return nil, fmt.Errorf("har: decode: %w", err)
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	var steps []pulse.Step
	for _, entry := range h.Log.Entries {
		req := entry.Request
		if cfg.Filter != nil && !cfg.Filter(req) {
			continue
		}
		steps = append(steps, pulse.Step{
			Name: req.Method + " " + req.URL,
			Do:   buildStep(client, req),
		})
	}

	if len(steps) == 0 {
		return nil, fmt.Errorf("har: no entries to replay (check Filter or empty archive)")
	}

	return pulse.Flow(steps...), nil
}

func buildStep(client *http.Client, req Request) pulse.Scenario {
	return func(ctx context.Context) (int, error) {
		var body io.Reader
		if req.PostData.Text != "" {
			body = strings.NewReader(req.PostData.Text)
		}

		httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, body)
		if err != nil {
			return 0, fmt.Errorf("har: build request: %w", err)
		}

		for _, h := range req.Headers {
			if _, skip := skipHeaders[strings.ToLower(h.Name)]; skip {
				continue
			}
			httpReq.Header.Set(h.Name, h.Value)
		}
		if req.PostData.MimeType != "" && httpReq.Header.Get("Content-Type") == "" {
			httpReq.Header.Set("Content-Type", req.PostData.MimeType)
		}

		resp, err := client.Do(httpReq)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body) //nolint:errcheck

		if resp.StatusCode >= http.StatusBadRequest {
			return resp.StatusCode, fmt.Errorf("har: HTTP %d", resp.StatusCode)
		}
		return resp.StatusCode, nil
	}
}
