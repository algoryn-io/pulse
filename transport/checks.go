package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// ErrCheckFailed is the sentinel wrapped around every failed response check.
// Detect it with errors.Is to distinguish check failures (a non-matching but
// otherwise successful response) from transport or status errors. Pulse maps it
// to the "check_failed" error category.
var ErrCheckFailed = errors.New("check failed")

// Checks describes a set of response assertions evaluated after a request
// completes. A zero-valued field is skipped, so a Checks value enables only the
// assertions it sets. Run reports the first failing check wrapped with
// ErrCheckFailed; when every configured check passes it returns nil.
//
// Checks is the declarative counterpart to the AssertStatus/AssertHeader/
// AssertBodyContains helpers: it is what the YAML `checks:` block compiles to,
// but it is equally usable directly from Go scenarios.
type Checks struct {
	// Status is the expected HTTP status code. Zero skips the status check.
	Status int

	// HeaderEquals maps a header key to its expected exact value. Keys are
	// canonicalized automatically (e.g. "content-type" matches "Content-Type").
	HeaderEquals map[string]string

	// BodyContains lists substrings that must all be present in the body.
	BodyContains []string

	// JSONEquals maps a top-level JSON field name to its expected value rendered
	// as text. String fields compare directly; numbers and booleans compare by
	// their textual form (e.g. 5 matches "5", true matches "true").
	JSONEquals map[string]string
}

// IsZero reports whether no check is configured.
func (c Checks) IsZero() bool {
	return c.Status == 0 && len(c.HeaderEquals) == 0 &&
		len(c.BodyContains) == 0 && len(c.JSONEquals) == 0
}

// HasStatus reports whether a status-code check is configured.
func (c Checks) HasStatus() bool { return c.Status != 0 }

// Run evaluates every configured assertion against resp and returns the first
// failure wrapped with ErrCheckFailed, or nil if all checks pass. Map-based
// checks are evaluated in sorted key order so a failing run reports a stable
// reason.
func (c Checks) Run(resp *Response) error {
	if c.Status != 0 {
		if err := AssertStatus(resp, c.Status); err != nil {
			return fmt.Errorf("%w: %v", ErrCheckFailed, err)
		}
	}
	for _, k := range sortedKeys(c.HeaderEquals) {
		if err := AssertHeader(resp, k, c.HeaderEquals[k]); err != nil {
			return fmt.Errorf("%w: %v", ErrCheckFailed, err)
		}
	}
	for _, sub := range c.BodyContains {
		if err := AssertBodyContains(resp, sub); err != nil {
			return fmt.Errorf("%w: %v", ErrCheckFailed, err)
		}
	}
	for _, field := range sortedKeys(c.JSONEquals) {
		got, err := jsonFieldText(resp, field)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrCheckFailed, err)
		}
		if want := c.JSONEquals[field]; got != want {
			return fmt.Errorf("%w: json field %q = %q, want %q", ErrCheckFailed, field, got, want)
		}
	}
	return nil
}

// jsonFieldText returns the textual form of a top-level JSON field. Strings are
// returned verbatim; other scalar values use their default fmt rendering.
func jsonFieldText(resp *Response, field string) (string, error) {
	var doc map[string]any
	if err := json.Unmarshal(resp.Body, &doc); err != nil {
		return "", fmt.Errorf("body is not valid JSON: %w", err)
	}
	v, ok := doc[field]
	if !ok {
		return "", fmt.Errorf("json field %q not found", field)
	}
	if s, ok := v.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", v), nil
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
