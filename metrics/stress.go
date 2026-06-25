package metrics

// StressResult reports the outcome of a ramp-to-failure (stress) run: the
// capacity the target sustained before its error rate or latency breached the
// configured failure thresholds. It is nil for non-stress runs.
type StressResult struct {
	// MaxHealthyRPS is the highest target arrival rate (requests/sec) at which an
	// interval completed without breaching a failure threshold.
	MaxHealthyRPS int
	// FailedAtRPS is the target arrival rate at which sustained failure was
	// detected. Zero when the run finished without failing (hit MaxRPS or the
	// phase duration).
	FailedAtRPS int
	// Reason identifies which threshold triggered the failure: "error_rate" or
	// "p99_latency". Empty when the run did not fail.
	Reason string
	// Failed is true when a failure point was found, false when the ramp completed
	// within its bounds without breaching (capacity is at least MaxHealthyRPS).
	Failed bool
}
