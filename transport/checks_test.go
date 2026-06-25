package transport

import (
	"errors"
	"net/http"
	"testing"
)

func resp(status int, header http.Header, body string) *Response {
	if header == nil {
		header = http.Header{}
	}
	return &Response{StatusCode: status, Header: header, Body: []byte(body)}
}

func TestChecksIsZeroAndHasStatus(t *testing.T) {
	if !(Checks{}).IsZero() {
		t.Fatal("empty Checks should be zero")
	}
	if (Checks{BodyContains: []string{"x"}}).IsZero() {
		t.Fatal("Checks with a body substring should not be zero")
	}
	if (Checks{}).HasStatus() {
		t.Fatal("empty Checks should not have a status check")
	}
	if !(Checks{Status: 200}).HasStatus() {
		t.Fatal("Checks{Status:200} should have a status check")
	}
}

func TestChecksRunAllPass(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	r := resp(200, h, `{"status":"healthy","count":5,"ok":true}`)
	c := Checks{
		Status:       200,
		HeaderEquals: map[string]string{"content-type": "application/json"},
		BodyContains: []string{"healthy", "count"},
		JSONEquals:   map[string]string{"status": "healthy", "count": "5", "ok": "true"},
	}
	if err := c.Run(r); err != nil {
		t.Fatalf("expected all checks to pass, got %v", err)
	}
}

func TestChecksRunFailures(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "text/plain")
	base := resp(200, h, `{"status":"degraded"}`)

	cases := []struct {
		name   string
		checks Checks
	}{
		{"status mismatch", Checks{Status: 201}},
		{"header mismatch", Checks{HeaderEquals: map[string]string{"Content-Type": "application/json"}}},
		{"body missing substring", Checks{BodyContains: []string{"healthy"}}},
		{"json field mismatch", Checks{JSONEquals: map[string]string{"status": "healthy"}}},
		{"json field absent", Checks{JSONEquals: map[string]string{"missing": "x"}}},
		{"json not parseable", Checks{JSONEquals: map[string]string{"status": "x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			if tc.name == "json not parseable" {
				r = resp(200, h, "not json")
			}
			err := tc.checks.Run(r)
			if err == nil {
				t.Fatal("expected a check failure")
			}
			if !errors.Is(err, ErrCheckFailed) {
				t.Fatalf("expected ErrCheckFailed, got %v", err)
			}
		})
	}
}

func TestChecksRunStopsAtFirstFailureDeterministically(t *testing.T) {
	// Two header checks both fail; sorted-key order makes the reported reason
	// stable across runs (A-Header before B-Header).
	r := resp(200, http.Header{}, "")
	c := Checks{HeaderEquals: map[string]string{
		"B-Header": "b",
		"A-Header": "a",
	}}
	err := c.Run(r)
	if err == nil || !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("expected ErrCheckFailed, got %v", err)
	}
	if want := `"A-Header"`; !contains(err.Error(), want) {
		t.Fatalf("expected the A-Header failure first, got %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
