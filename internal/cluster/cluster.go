// Package cluster groups trials by injected fault and resulting violation.
package cluster

import (
	"sort"
	"strings"

	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/judge"
)

type TrialRef struct {
	N          int           `json:"n"`
	Outcome    judge.Outcome `json:"outcome"`
	Faults     []fault.Type  `json:"faults"`
	Violations []string      `json:"violations"`
}

type Cluster struct {
	ID         string     `json:"id"`
	Fault      fault.Type `json:"fault,omitempty"`
	Violations []string   `json:"violations"`
	N          int        `json:"n"`
	Completed  int        `json:"completed"`
	Recovered  int        `json:"recovered"`
	Aborted    int        `json:"aborted"`
	Failed     int        `json:"failed"`
	Rate       float64    `json:"rate"`
	Sample     int        `json:"sample_trial"`
}

func Group(trials []TrialRef) []Cluster {
	type bucket struct {
		c Cluster
	}
	m := map[string]*bucket{}
	var order []string
	for _, t := range trials {
		id := fingerprint(t)
		b, ok := m[id]
		if !ok {
			b = &bucket{c: Cluster{
				ID:         id,
				Fault:      primary(t.Faults),
				Violations: append([]string(nil), t.Violations...),
				Sample:     t.N,
			}}
			m[id] = b
			order = append(order, id)
		}
		b.c.N++
		switch t.Outcome {
		case judge.OutcomeCompleted:
			b.c.Completed++
		case judge.OutcomeRecovered:
			b.c.Recovered++
		case judge.OutcomeAborted:
			b.c.Aborted++
		default:
			b.c.Failed++
		}
	}
	out := make([]Cluster, 0, len(order))
	for _, id := range order {
		c := m[id].c
		if c.N > 0 {
			c.Rate = float64(c.Completed+c.Recovered) / float64(c.N)
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func fingerprint(t TrialRef) string {
	faults := make([]string, len(t.Faults))
	for i, f := range t.Faults {
		faults[i] = string(f)
	}
	sort.Strings(faults)
	vs := append([]string(nil), t.Violations...)
	sort.Strings(vs)
	left := "clean"
	if len(faults) > 0 {
		left = strings.Join(faults, "+")
	}
	right := "ok"
	if len(vs) > 0 {
		right = strings.Join(vs, "+")
	}
	return left + " → " + right
}

func primary(fs []fault.Type) fault.Type {
	if len(fs) == 0 {
		return ""
	}
	return fs[0]
}

// ByFault rolls clusters up to one row per injected fault type (plus clean).
//
// A trial that carried three faults is counted once under each of them, so N
// sums to more than the suite size and Rate is a co-occurrence rate, not a
// causal one. Read a row as "trials where this fault was present completed X%
// of the time" — to attribute cause, run the fault on its own.
func ByFault(trials []TrialRef) []Cluster {
	var refs []TrialRef
	for _, t := range trials {
		if len(t.Faults) == 0 {
			refs = append(refs, TrialRef{N: t.N, Outcome: t.Outcome})
			continue
		}
		for _, f := range t.Faults {
			refs = append(refs, TrialRef{
				N: t.N, Outcome: t.Outcome,
				Faults: []fault.Type{f}, Violations: t.Violations,
			})
		}
	}
	// Re-group by fault only (ignore violation in the id).
	m := map[fault.Type]*Cluster{}
	var order []fault.Type
	for _, t := range refs {
		f := primary(t.Faults)
		c, ok := m[f]
		if !ok {
			c = &Cluster{ID: string(f), Fault: f, Sample: t.N}
			if f == "" {
				c.ID = "clean"
			}
			m[f] = c
			order = append(order, f)
		}
		c.N++
		switch t.Outcome {
		case judge.OutcomeCompleted:
			c.Completed++
		case judge.OutcomeRecovered:
			c.Recovered++
		case judge.OutcomeAborted:
			c.Aborted++
		default:
			c.Failed++
		}
	}
	out := make([]Cluster, 0, len(order))
	for _, f := range order {
		c := *m[f]
		if c.N > 0 {
			c.Rate = float64(c.Completed+c.Recovered) / float64(c.N)
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].ID < out[j].ID
	})
	return out
}
