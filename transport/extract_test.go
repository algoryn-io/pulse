package transport

import (
	"net/http"
	"strings"
	"testing"
)

func respWith(body string, headers map[string]string) *Response {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	return &Response{StatusCode: 200, Header: h, Body: []byte(body)}
}

// -- ExtractHeader --

func TestExtractHeaderReturnsValue(t *testing.T) {
	resp := respWith("", map[string]string{"X-Request-ID": "req-123"})
	got, err := ExtractHeader(resp, "X-Request-ID")
	if err != nil || got != "req-123" {
		t.Fatalf("expected (req-123, nil), got (%q, %v)", got, err)
	}
}

func TestExtractHeaderCanonicalizesKey(t *testing.T) {
	resp := respWith("", map[string]string{"Content-Type": "application/json"})
	got, err := ExtractHeader(resp, "content-type")
	if err != nil || got != "application/json" {
		t.Fatalf("expected (application/json, nil), got (%q, %v)", got, err)
	}
}

func TestExtractHeaderMissingReturnsError(t *testing.T) {
	resp := respWith("", nil)
	_, err := ExtractHeader(resp, "X-Missing")
	if err == nil || !strings.Contains(err.Error(), "X-Missing") {
		t.Fatalf("expected error naming header, got %v", err)
	}
}

// -- ExtractJSONString --

func TestExtractJSONStringReturnsField(t *testing.T) {
	resp := respWith(`{"token":"abc123","expires":3600}`, nil)
	got, err := ExtractJSONString(resp, "token")
	if err != nil || got != "abc123" {
		t.Fatalf("expected (abc123, nil), got (%q, %v)", got, err)
	}
}

func TestExtractJSONStringMissingFieldReturnsError(t *testing.T) {
	resp := respWith(`{"other":"value"}`, nil)
	_, err := ExtractJSONString(resp, "token")
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("expected error naming field, got %v", err)
	}
}

func TestExtractJSONStringNonStringFieldReturnsError(t *testing.T) {
	resp := respWith(`{"count":42}`, nil)
	_, err := ExtractJSONString(resp, "count")
	if err == nil {
		t.Fatal("expected error for non-string field, got nil")
	}
}

func TestExtractJSONStringInvalidJSONReturnsError(t *testing.T) {
	resp := respWith(`not json`, nil)
	_, err := ExtractJSONString(resp, "token")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// -- ExtractRegexp --

func TestExtractRegexpReturnsFirstCaptureGroup(t *testing.T) {
	resp := respWith(`<input name="csrf" value="tok-xyz"/>`, nil)
	got, err := ExtractRegexp(resp, `value="([^"]+)"`)
	if err != nil || got != "tok-xyz" {
		t.Fatalf("expected (tok-xyz, nil), got (%q, %v)", got, err)
	}
}

func TestExtractRegexpNoMatchReturnsError(t *testing.T) {
	resp := respWith(`<html>no token here</html>`, nil)
	_, err := ExtractRegexp(resp, `value="([^"]+)"`)
	if err == nil {
		t.Fatal("expected error for no match, got nil")
	}
}

func TestExtractRegexpNoCaptureGroupReturnsError(t *testing.T) {
	resp := respWith(`hello world`, nil)
	_, err := ExtractRegexp(resp, `hello`)
	if err == nil || !strings.Contains(err.Error(), "capture group") {
		t.Fatalf("expected capture group error, got %v", err)
	}
}

func TestExtractRegexpInvalidPatternReturnsError(t *testing.T) {
	resp := respWith(`anything`, nil)
	_, err := ExtractRegexp(resp, `[invalid`)
	if err == nil {
		t.Fatal("expected error for invalid pattern, got nil")
	}
}

func TestExtractRegexpWorksOnJSONBody(t *testing.T) {
	resp := respWith(`{"session_id":"sess-99","user":"alice"}`, nil)
	got, err := ExtractRegexp(resp, `"session_id":"([^"]+)"`)
	if err != nil || got != "sess-99" {
		t.Fatalf("expected (sess-99, nil), got (%q, %v)", got, err)
	}
}
