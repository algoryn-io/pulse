package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeTempYAML: %v", err)
	}
	return path
}

func TestExpandEnvSubstitutes(t *testing.T) {
	t.Setenv("BASE_URL", "http://localhost:8080")
	out, err := expandEnv([]byte("url: ${BASE_URL}"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "url: http://localhost:8080" {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestExpandEnvUsesDefault(t *testing.T) {
	out, err := expandEnv([]byte("timeout: ${TIMEOUT:-30s}"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "timeout: 30s" {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestExpandEnvEnvOverridesDefault(t *testing.T) {
	t.Setenv("TIMEOUT", "60s")
	out, err := expandEnv([]byte("timeout: ${TIMEOUT:-30s}"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "timeout: 60s" {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestExpandEnvErrorsOnMissingRequired(t *testing.T) {
	_, err := expandEnv([]byte("token: ${API_TOKEN_MISSING_XYZ}"))
	if err == nil {
		t.Fatal("expected error for unset required variable, got nil")
	}
	if !strings.Contains(err.Error(), "API_TOKEN_MISSING_XYZ") {
		t.Fatalf("error should name the variable, got: %v", err)
	}
}

func TestExpandEnvMultipleVars(t *testing.T) {
	t.Setenv("HOST", "api.example.com")
	t.Setenv("PORT", "443")
	out, err := expandEnv([]byte("url: https://${HOST}:${PORT}/v1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "url: https://api.example.com:443/v1" {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestExpandEnvEmptyDefault(t *testing.T) {
	out, err := expandEnv([]byte("label: ${LABEL:-}"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "label: " {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestExpandEnvNoPlaceholders(t *testing.T) {
	input := []byte("url: http://localhost:8080\ntimeout: 30s")
	out, err := expandEnv(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(input) {
		t.Fatalf("input should be unchanged, got: %s", out)
	}
}

func TestExpandEnvInvalidSyntaxLeftAsIs(t *testing.T) {
	// ${ without closing } is not matched and left as-is
	input := []byte("note: ${ unclosed")
	out, err := expandEnv(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(input) {
		t.Fatalf("unmatched placeholder should be unchanged, got: %s", out)
	}
}

func TestLoadExpandsEnvVarsInYAML(t *testing.T) {
	t.Setenv("TARGET_URL", "http://localhost:9999")
	t.Setenv("ARRIVAL_RATE", "10")

	yaml := `
phases:
  - type: constant
    duration: 1s
    arrivalRate: ${ARRIVAL_RATE}
target:
  url: ${TARGET_URL}
  method: GET
maxConcurrency: 1
`
	f := writeTempYAML(t, yaml)
	test, err := Load(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if test.Scenario == nil {
		t.Fatal("expected non-nil scenario")
	}
	if len(test.Config.Phases) != 1 || test.Config.Phases[0].ArrivalRate != 10 {
		t.Fatalf("expected arrivalRate=10 after env expansion, got: %+v", test.Config.Phases)
	}
}

func TestLoadErrorsOnMissingEnvVar(t *testing.T) {
	yaml := `
phases:
  - type: constant
    duration: 1s
    arrivalRate: 5
target:
  url: ${UNSET_URL_XYZ_MISSING}
  method: GET
maxConcurrency: 1
`
	f := writeTempYAML(t, yaml)
	_, err := Load(f)
	if err == nil {
		t.Fatal("expected error for unset env var, got nil")
	}
	if !strings.Contains(err.Error(), "UNSET_URL_XYZ_MISSING") {
		t.Fatalf("error should name the variable, got: %v", err)
	}
}
