package pulse

import (
	"fmt"
	"strings"
	"time"
)

// ThresholdViolationError is returned when a configured threshold is exceeded.
// Description matches the corresponding ThresholdOutcome description (e.g. "mean_latency < 200ms").
type ThresholdViolationError struct {
	Description string
	Actual      any
	Limit       any
}

func (e *ThresholdViolationError) Error() string {
	return fmt.Sprintf(
		"pulse: threshold violated (%s): got %s, limit %s",
		e.Description,
		formatThresholdValue(e.Description, e.Actual),
		formatThresholdValue(e.Description, e.Limit),
	)
}

func formatThresholdValue(description string, value any) string {
	switch v := value.(type) {
	case float64:
		if strings.HasPrefix(description, "error_rate < ") {
			return fmt.Sprintf("%.3f (%.1f%%)", v, v*100)
		}
		return fmt.Sprintf("%.3f", v)
	case float32:
		return formatThresholdValue(description, float64(v))
	case time.Duration:
		return v.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}
