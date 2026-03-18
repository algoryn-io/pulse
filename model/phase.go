package model

// PhaseType describes how a phase should be executed.
type PhaseType string

const (
	// PhaseTypeConstant represents a constant arrival-rate phase.
	PhaseTypeConstant PhaseType = "constant"
)
