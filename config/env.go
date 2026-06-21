package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var envPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// expandEnv replaces ${VAR} and ${VAR:-default} placeholders in data with
// values from the environment. Returns an error if a required variable (one
// with no default) is not set.
func expandEnv(data []byte) ([]byte, error) {
	var expandErr error
	result := envPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		if expandErr != nil {
			return match
		}
		inner := string(match[2 : len(match)-1]) // strip ${ and }
		name, def, hasDefault := strings.Cut(inner, ":-")
		val, ok := os.LookupEnv(name)
		if !ok {
			if hasDefault {
				return []byte(def)
			}
			expandErr = fmt.Errorf("config: environment variable %q is not set", name)
			return match
		}
		return []byte(val)
	})
	if expandErr != nil {
		return nil, expandErr
	}
	return result, nil
}
