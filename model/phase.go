package model

// PhaseType describes how a phase should be executed.
type PhaseType string

const (
	// PhaseTypeConstant represents a constant arrival-rate phase.
	PhaseTypeConstant PhaseType = "constant"
	// PhaseTypeRamp represents a phase where arrival rate changes linearly from From to To.
	PhaseTypeRamp PhaseType = "ramp"
	// PhaseTypeStep represents a phase where arrival rate moves from From to To in discrete steps.
	PhaseTypeStep PhaseType = "step"
	// PhaseTypeSpike represents a phase with a temporary spike from From to To.
	PhaseTypeSpike PhaseType = "spike"
)
