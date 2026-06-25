package config

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// loadFeederScenario writes a data file and a YAML config pointing at srvURL,
// then returns the built scenario.
func loadFeederScenario(t *testing.T, srvURL, urlPath, body, format, dataName, data, mode string) func(context.Context) (int, error) {
	t.Helper()
	dataPath := writeFile(t, dataName, data)
	yaml := fmt.Sprintf(""+
		"phases:\n"+
		"  - type: constant\n"+
		"    duration: 1s\n"+
		"    arrivalRate: 1\n"+
		"target:\n"+
		"  method: %s\n"+
		"  url: %s%s\n",
		map[bool]string{true: "POST", false: "GET"}[body != ""], srvURL, urlPath)
	if body != "" {
		yaml += "  body: '" + body + "'\n"
	}
	yaml += "feeder:\n  format: " + format + "\n  path: " + dataPath + "\n"
	if mode != "" {
		yaml += "  mode: " + mode + "\n"
	}
	cfgPath := writeFile(t, "cfg.yaml", yaml)
	test, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return test.Scenario
}

func TestFeederCSVRoundRobinSubstitutesURL(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	scenario := loadFeederScenario(t, srv.URL, "/users/{{id}}", "", "csv", "users.csv",
		"id,name\n1,alice\n2,bob\n3,carol\n", "")

	for i := 0; i < 6; i++ {
		if _, err := scenario(context.Background()); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	want := []string{"/users/1", "/users/2", "/users/3", "/users/1", "/users/2", "/users/3"}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("round-robin paths = %v, want %v", paths, want)
	}
}

func TestFeederJSONLSubstitutesBody(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	scenario := loadFeederScenario(t, srv.URL, "/submit", `{"user":"{{name}}","n":{{count}}}`, "jsonl", "data.jsonl",
		`{"name":"alice","count":5}`+"\n"+`{"name":"bob","count":10}`+"\n", "")

	for i := 0; i < 2; i++ {
		if _, err := scenario(context.Background()); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if bodies[0] != `{"user":"alice","n":5}` {
		t.Fatalf("body[0] = %q", bodies[0])
	}
	if bodies[1] != `{"user":"bob","n":10}` {
		t.Fatalf("body[1] = %q", bodies[1])
	}
}

func TestFeederMissingVariableRejectedAtLoad(t *testing.T) {
	dataPath := writeFile(t, "users.csv", "id,name\n1,alice\n")
	yaml := "" +
		"phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n" +
		"target:\n  method: GET\n  url: http://example.com/users/{{missing}}\n" +
		"feeder:\n  format: csv\n  path: " + dataPath + "\n"
	cfgPath := writeFile(t, "cfg.yaml", yaml)
	_, err := Load(cfgPath)
	if err == nil || !strings.Contains(err.Error(), `missing variable "missing"`) {
		t.Fatalf("expected a missing-variable error, got %v", err)
	}
}

func TestFeederHeaderPlaceholderRejected(t *testing.T) {
	dataPath := writeFile(t, "users.csv", "token\nabc\n")
	yaml := "" +
		"phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n" +
		"target:\n  method: GET\n  url: http://example.com/\n  headers:\n    X-Token: '{{token}}'\n" +
		"feeder:\n  format: csv\n  path: " + dataPath + "\n"
	cfgPath := writeFile(t, "cfg.yaml", yaml)
	_, err := Load(cfgPath)
	if !errors.Is(err, errFeederHeaderTemplate) {
		t.Fatalf("expected errFeederHeaderTemplate, got %v", err)
	}
}

func TestFeederValidationErrors(t *testing.T) {
	dataPath := writeFile(t, "d.csv", "id\n1\n")
	base := "phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n" +
		"target:\n  method: GET\n  url: http://example.com/{{id}}\n"

	cases := []struct {
		name    string
		feeder  string
		wantErr error
	}{
		{"unknown format", "feeder:\n  format: xml\n  path: " + dataPath + "\n", errFeederUnknownFormat},
		{"missing path", "feeder:\n  format: csv\n", errFeederPathRequired},
		{"unknown mode", "feeder:\n  format: csv\n  path: " + dataPath + "\n  mode: shuffle\n", errFeederUnknownMode},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfgPath := writeFile(t, "cfg.yaml", base+c.feeder)
			_, err := Load(cfgPath)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("got %v, want %v", err, c.wantErr)
			}
		})
	}
}

func TestFeederEmptyFileRejected(t *testing.T) {
	dataPath := writeFile(t, "empty.csv", "id,name\n") // header only, no rows
	yaml := "phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n" +
		"target:\n  method: GET\n  url: http://example.com/{{id}}\n" +
		"feeder:\n  format: csv\n  path: " + dataPath + "\n"
	cfgPath := writeFile(t, "cfg.yaml", yaml)
	_, err := Load(cfgPath)
	if !errors.Is(err, errFeederNoRows) {
		t.Fatalf("expected errFeederNoRows, got %v", err)
	}
}

func TestFeederDistributedRejected(t *testing.T) {
	dataPath := writeFile(t, "d.csv", "id\n1\n")
	yaml := "phases:\n  - type: constant\n    duration: 1s\n    arrivalRate: 1\n" +
		"target:\n  method: GET\n  url: http://example.com/{{id}}\n" +
		"workers: [\"127.0.0.1:9300\"]\n" +
		"feeder:\n  format: csv\n  path: " + dataPath + "\n"
	cfgPath := writeFile(t, "cfg.yaml", yaml)
	_, err := Load(cfgPath)
	if !errors.Is(err, errFeederDistributed) {
		t.Fatalf("expected errFeederDistributed, got %v", err)
	}
}

func TestSubstituteVars(t *testing.T) {
	row := map[string]string{"id": "42", "name": "alice"}
	out, err := substituteVars("/u/{{id}}/{{ name }}", row)
	if err != nil || out != "/u/42/alice" {
		t.Fatalf("got %q, %v", out, err)
	}
	if _, err := substituteVars("{{missing}}", row); err == nil {
		t.Fatal("expected missing-variable error")
	}
}

func TestFeederPlaceholders(t *testing.T) {
	got := feederPlaceholders("/a/{{x}}/{{y}}?z={{x}}")
	if strings.Join(got, ",") != "x,y" {
		t.Fatalf("placeholders = %v, want [x y] distinct in order", got)
	}
	if feederPlaceholders("/no/vars") != nil {
		t.Fatal("expected nil for a template without placeholders")
	}
}

func TestScalarToString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"hi", "hi"},
		{float64(5), "5"},
		{true, "true"},
		{nil, ""},
		{map[string]any{"nested": 1}, ""},
		{[]any{1, 2}, ""},
	}
	for _, c := range cases {
		if got := scalarToString(c.in); got != c.want {
			t.Errorf("scalarToString(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildRowFeederRandomDeterministicWithSeed(t *testing.T) {
	rows := []map[string]string{{"id": "1"}, {"id": "2"}, {"id": "3"}, {"id": "4"}}
	seed := int64(99)
	seqOf := func() []string {
		f := buildRowFeeder(rows, "random", &seed)
		var out []string
		for i := 0; i < 12; i++ {
			out = append(out, f.Next()["id"])
		}
		return out
	}
	a, b := seqOf(), seqOf()
	if strings.Join(a, "") != strings.Join(b, "") {
		t.Fatalf("seeded random not reproducible: %v vs %v", a, b)
	}
}
