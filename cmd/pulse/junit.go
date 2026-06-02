package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"

	pulse "algoryn.io/pulse"
)

// JUnit report model (minimal).
// We intentionally keep it small and deterministic so CI systems can consume it reliably.

type junitTestSuite struct {
	XMLName   xml.Name        `xml:"testsuite"`
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Errors    int             `xml:"errors,attr"`
	TimeSec   string          `xml:"time,attr,omitempty"`
	TestCases []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr,omitempty"`
	TimeSec   string        `xml:"time,attr,omitempty"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Error     *junitError   `xml:"error,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr,omitempty"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"`
}

type junitError struct {
	Message string `xml:"message,attr,omitempty"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"`
}

func writeJUnit(w io.Writer, result pulse.Result, runErr error) error {
	suite := junitFrom(result, runErr)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	if err := enc.Encode(suite); err != nil {
		return err
	}
	return enc.Flush()
}

func junitFrom(result pulse.Result, runErr error) junitTestSuite {
	suite := junitTestSuite{
		Name: "pulse",
	}
	if result.Duration > 0 {
		suite.TimeSec = fmt.Sprintf("%.3f", result.Duration.Seconds())
	}

	// Execution error (non-threshold): represent as one errored test.
	if runErr != nil && !isThresholdEvaluationFailureOnly(runErr) {
		suite.Tests = 1
		suite.Errors = 1
		timeSec := ""
		if result.Duration > 0 {
			timeSec = fmt.Sprintf("%.3f", result.Duration.Seconds())
		}
		suite.TestCases = []junitTestCase{{
			Name:      "run",
			ClassName: "pulse",
			TimeSec:   timeSec,
			Error: &junitError{
				Message: runErr.Error(),
				Type:    fmt.Sprintf("%T", runErr),
				Body:    runErr.Error(),
			},
		}}
		return suite
	}

	// Thresholds become individual testcases so CI shows exactly what failed.
	if len(result.ThresholdOutcomes) > 0 {
		suite.Tests = len(result.ThresholdOutcomes)
		suite.TestCases = make([]junitTestCase, 0, len(result.ThresholdOutcomes))
		for _, o := range result.ThresholdOutcomes {
			tc := junitTestCase{
				Name:      o.Description,
				ClassName: "threshold",
			}

			if !o.Pass {
				suite.Failures++
				actual, limit := thresholdActualLimit(result, o.Description, runErr)
				msg := o.Description
				body := o.Description
				if actual != "" || limit != "" {
					body = fmt.Sprintf("%s (actual=%s limit=%s)", o.Description, actual, limit)
				}
				tc.Failure = &junitFailure{
					Message: msg,
					Type:    "threshold_violation",
					Body:    body,
				}
			}
			suite.TestCases = append(suite.TestCases, tc)
		}
		return suite
	}

	// No thresholds: still emit one passing test so CI has a tangible artifact.
	suite.Tests = 1
	suite.TestCases = []junitTestCase{{
		Name:      "run",
		ClassName: "pulse",
		TimeSec:   suite.TimeSec,
	}}
	return suite
}

func thresholdActualLimit(result pulse.Result, description string, runErr error) (actual string, limit string) {
	// If runErr is a join of violations, try to find the one for this threshold description.
	leaves := unwrapErrorLeaves(runErr)
	for _, e := range leaves {
		var tv *pulse.ThresholdViolationError
		if !errors.As(e, &tv) {
			continue
		}
		if tv.Description != description {
			continue
		}
		if tv.Actual != nil {
			actual = fmt.Sprintf("%v", tv.Actual)
		}
		if tv.Limit != nil {
			limit = fmt.Sprintf("%v", tv.Limit)
		}
		return actual, limit
	}

	// Fall back to empty; description alone is still useful.
	_ = result
	return "", ""
}
