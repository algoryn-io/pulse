package coordinator

import (
	"testing"

	"algoryn.io/pulse/distributed"
)

func sumInts(xs []int) int {
	s := 0
	for _, x := range xs {
		s += x
	}
	return s
}

func TestSplitIntPreservesTotalAndSpreadsRemainder(t *testing.T) {
	cases := []struct {
		total   int
		weights []int
		want    []int
	}{
		{10, []int{1, 1, 1}, []int{4, 3, 3}}, // remainder spread, not dumped on one
		{7, []int{1, 1, 1}, []int{3, 2, 2}},
		{9, []int{1, 1, 1}, []int{3, 3, 3}}, // exact
		{10, []int{2, 1, 1}, []int{5, 3, 2}}, // weighted 2:1:1
		{0, []int{1, 1}, []int{0, 0}},
		{5, []int{1}, []int{5}},
	}
	for _, c := range cases {
		got := splitInt(c.total, c.weights)
		if sumInts(got) != c.total {
			t.Errorf("splitInt(%d,%v) sum = %d, want %d (got %v)", c.total, c.weights, sumInts(got), c.total, got)
		}
		if len(got) != len(c.want) {
			t.Fatalf("splitInt(%d,%v) len = %d, want %d", c.total, c.weights, len(got), len(c.want))
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("splitInt(%d,%v) = %v, want %v", c.total, c.weights, got, c.want)
				break
			}
		}
	}
}

func TestSplitRatesEqualPreservesTotalRate(t *testing.T) {
	req := distributed.RunRequest{
		Phases: []distributed.Phase{
			{Type: "constant", ArrivalRate: 100},
			{Type: "ramp", From: 10, To: 50},
		},
		MaxConcurrency: 7,
	}
	reqs := splitRates(req, nil, 3) // equal weighting

	if len(reqs) != 3 {
		t.Fatalf("expected 3 worker requests, got %d", len(reqs))
	}
	// Total arrival rate must be exactly preserved per phase.
	var arrival, from, to, conc int
	for _, r := range reqs {
		arrival += r.Phases[0].ArrivalRate
		from += r.Phases[1].From
		to += r.Phases[1].To
		conc += r.MaxConcurrency
	}
	if arrival != 100 {
		t.Errorf("total arrival = %d, want 100", arrival)
	}
	if from != 10 || to != 50 {
		t.Errorf("ramp endpoints = %d->%d, want 10->50", from, to)
	}
	if conc != 7 {
		t.Errorf("total concurrency = %d, want 7", conc)
	}
	// Remainder must not all land on worker 0 (100/3 -> 34,33,33).
	if reqs[0].Phases[0].ArrivalRate != 34 {
		t.Errorf("worker 0 arrival = %d, want 34", reqs[0].Phases[0].ArrivalRate)
	}
}

func TestSplitRatesWeighted(t *testing.T) {
	req := distributed.RunRequest{
		Phases:         []distributed.Phase{{Type: "constant", ArrivalRate: 100}},
		MaxConcurrency: 100,
	}
	reqs := splitRates(req, []int{3, 1}, 2) // 3:1 capacity

	a0 := reqs[0].Phases[0].ArrivalRate
	a1 := reqs[1].Phases[0].ArrivalRate
	if a0+a1 != 100 {
		t.Fatalf("total arrival = %d, want 100", a0+a1)
	}
	if a0 != 75 || a1 != 25 {
		t.Errorf("weighted split = %d:%d, want 75:25", a0, a1)
	}
}
