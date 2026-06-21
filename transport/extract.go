package transport

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// ExtractHeader returns the value of the response header identified by key.
// The key is canonicalized automatically. Returns an error if the header is
// absent or empty.
func ExtractHeader(resp *Response, key string) (string, error) {
	val := resp.Header.Get(key)
	if val == "" {
		return "", fmt.Errorf("extract: header %q not found", key)
	}
	return val, nil
}

// ExtractJSONString extracts a top-level string field from a JSON response
// body. Returns an error if the body is not valid JSON, the field is absent,
// or the field value is not a string.
func ExtractJSONString(resp *Response, field string) (string, error) {
	var doc map[string]any
	if err := json.Unmarshal(resp.Body, &doc); err != nil {
		return "", fmt.Errorf("extract: body is not valid JSON: %w", err)
	}
	v, ok := doc[field]
	if !ok {
		return "", fmt.Errorf("extract: field %q not found in JSON body", field)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("extract: field %q is %T, expected string", field, v)
	}
	return s, nil
}

// ExtractRegexp extracts the first capture group from resp.Body using the
// provided regular expression pattern. Returns an error if the pattern does
// not compile, does not match, or has no capture group.
func ExtractRegexp(resp *Response, pattern string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("extract: invalid pattern %q: %w", pattern, err)
	}
	m := re.FindSubmatch(resp.Body)
	if m == nil {
		return "", fmt.Errorf("extract: pattern %q did not match", pattern)
	}
	if len(m) < 2 {
		return "", fmt.Errorf("extract: pattern %q has no capture group", pattern)
	}
	return string(m[1]), nil
}
