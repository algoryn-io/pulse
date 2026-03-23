package pulse

import "fmt"

// ThresholdViolationError is returned when a configured threshold is exceeded.
// Description matches the corresponding ThresholdOutcome description (e.g. "mean_latency < 200ms").
type ThresholdViolationError struct {
	Description string
	Actual      any
	Limit       any
}

func (e *ThresholdViolationError) Error() string {
	return fmt.Sprintf("pulse: threshold violated (%s): got %v, limit %v", e.Description, e.Actual, e.Limit)
}
