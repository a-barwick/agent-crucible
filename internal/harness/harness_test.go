package harness

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/judge"
)

func TestP0AllComplete(t *testing.T) {
	s := Run(context.Background(), Config{Seed: 42, Trials: 24, P: 0, Faults: fault.MVP})
	if s.Survival != 1 {
		t.Fatalf("p=0 survival %v counts=%v", s.Survival, s.Counts)
	}
	for _, tr := range s.Trials {
		if tr.Outcome != judge.OutcomeCompleted {
			t.Fatalf("trial %d: %+v", tr.N, tr)
		}
	}
}

func TestReplayBitIdentical(t *testing.T) {
	cfg := Config{Seed: 99, Trials: 16, P: 0.25, Faults: fault.MVP}
	a := Run(context.Background(), cfg)
	b := Run(context.Background(), cfg)
	if a.Survival != b.Survival {
		t.Fatalf("survival %v vs %v", a.Survival, b.Survival)
	}
	for i := range a.Trials {
		if !reflect.DeepEqual(eventMsgs(a.Trials[i]), eventMsgs(b.Trials[i])) {
			t.Fatalf("trial %d traces diverged", i)
		}
		if a.Trials[i].Outcome != b.Trials[i].Outcome {
			t.Fatalf("trial %d outcome %s vs %s", i, a.Trials[i].Outcome, b.Trials[i].Outcome)
		}
	}
	one := Replay(context.Background(), cfg, 7)
	if !reflect.DeepEqual(eventMsgs(one), eventMsgs(a.Trials[7])) {
		t.Fatal("replay(7) did not match suite trial 7")
	}
}

func TestCollapseAtThirty(t *testing.T) {
	cfg := Config{Seed: 42, Trials: 40, P: 0.30, Faults: fault.MVP}
	s := Run(context.Background(), cfg)
	if s.Survival >= 0.75 {
		t.Fatalf("expected a collapse at p=0.30, survival=%v counts=%v", s.Survival, s.Counts)
	}
	if s.Survival >= s.CleanRate && s.CleanRate == 1 {
		t.Fatalf("injected faults did not reduce survival (%v)", s.Survival)
	}
}

// The demo ensemble is pinned so that a change in the injector, the judge or
// the sample agent cannot quietly move the numbers on the front page. These
// values are not meaningful on their own; when a deliberate change moves them,
// re-pin them and say so in the commit.
func TestDemoSeed42Locked(t *testing.T) {
	s := Run(context.Background(), Config{Seed: 42, Trials: 40, P: 0.30, Faults: fault.MVP})
	if s.Survival != 0.30 {
		t.Fatalf("demo survival drifted: got %v want 0.30 counts=%v", s.Survival, s.Counts)
	}
	if s.Counts["completed"] != 6 || s.Counts["recovered"] != 6 {
		t.Fatalf("demo mix drifted: %v", s.Counts)
	}
	if s.Errored != 0 {
		t.Fatalf("the in-process demo should never need infrastructure: %d errored (%s)", s.Errored, s.Error)
	}
}

func TestSweepMonotonicSurvival(t *testing.T) {
	sw := RunSweep(context.Background(), Config{Seed: 42, Trials: 20, Faults: fault.MVP}, 0.30, 0.10)
	if len(sw.Suites) < 4 {
		t.Fatalf("suites %d", len(sw.Suites))
	}
	if sw.Suites[0].Survival != 1 {
		t.Fatalf("p=0 survival %v", sw.Suites[0].Survival)
	}
	prev := sw.Suites[0].Survival
	for _, s := range sw.Suites[1:] {
		if s.Survival > prev+1e-9 {
			t.Fatalf("survival rose from %v to %v at p=%v", prev, s.Survival, s.Config.P)
		}
		prev = s.Survival
	}
}

// Raising p on a fixed seed must not reshuffle the ensemble. Each decision site
// draws from its own sub-stream keyed by (site, target, visit), so any site both
// runs reach sees the same die roll and the same candidate fault; only the
// verdict on that roll changes, and it changes in one direction.
//
// Which faults you *observe* is inevitably path-dependent — a 403 at authorize
// means the write node never runs, and a site that is never reached cannot
// fire. So the invariant is asserted where it is exact: on the decisions
// themselves. With a single shared stream this fails on the first trial where
// a fault adds a retry, because every later draw shifts by one.
func TestDecisionsAreStableAsPRises(t *testing.T) {
	cfg := Config{Seed: 42, Trials: 24, Faults: fault.All}
	ps := []float64{0, 0.1, 0.2, 0.4, 0.7, 1}
	runs := make([]map[int]Trial, len(ps))
	for i, p := range ps {
		c := cfg
		c.P = p
		s := Run(context.Background(), c)
		runs[i] = map[int]Trial{}
		for _, tr := range s.Trials {
			runs[i][tr.N] = tr
		}
	}
	shared, vanished := 0, 0
	for i := 1; i < len(ps); i++ {
		for n, lo := range runs[i-1] {
			hiTrial := runs[i][n]
			hi := decisionsByKey(hiTrial)
			loKeys, loOrder := decisionsByKey(lo), decisionOrder(lo)
			for key, a := range loKeys {
				b, ok := hi[key]
				if !ok {
					// The higher-p run never reached this site. That is only
					// legitimate behind a fault that fired earlier and cut the
					// run short — otherwise the ensemble reshuffled, which is
					// what this test exists to catch.
					if !firedBefore(hiTrial, loOrder[key]) {
						t.Fatalf("trial %d site %s: reached at p=%v but not at p=%v, with no earlier fault to explain it (p=%v decisions %v)",
							n, key, ps[i-1], ps[i], ps[i], decisionKeys(hiTrial))
					}
					vanished++
					continue
				}
				shared++
				if a.U != b.U || a.Type != b.Type {
					t.Fatalf("trial %d site %s: draw changed with p (%v/%s at p=%v vs %v/%s at p=%v)",
						n, key, a.U, a.Type, ps[i-1], b.U, b.Type, ps[i])
				}
				if a.Fired && !b.Fired {
					t.Fatalf("trial %d site %s: fired at p=%v but not at p=%v (u=%v)",
						n, key, ps[i-1], ps[i], a.U)
				}
			}
		}
	}
	if shared == 0 {
		t.Fatal("no sites were shared between runs; the test compared nothing")
	}
	t.Logf("compared %d shared sites; %d vanished behind an earlier fault", shared, vanished)
}

// firedBefore reports whether tr fired a fault at a decision index below idx.
// That is the one thing allowed to make a site the lower-p run reached
// unreachable: the run stopped before getting there.
func firedBefore(tr Trial, idx int) bool {
	for i, d := range tr.Decisions {
		if i >= idx {
			break
		}
		if d.Fired {
			return true
		}
	}
	// The arming of a cost ceiling is logged at preflight but only bites on a
	// later call, so a run can end early with its only fired decision after the
	// site that disappeared.
	for _, d := range tr.Decisions {
		if d.Fired && d.Type == fault.CostCeiling {
			return true
		}
	}
	return false
}

func decisionsByKey(tr Trial) map[string]fault.Decision {
	out := map[string]fault.Decision{}
	for _, d := range tr.Decisions {
		out[decisionKey(d)] = d
	}
	return out
}

func decisionOrder(tr Trial) map[string]int {
	out := map[string]int{}
	for i, d := range tr.Decisions {
		out[decisionKey(d)] = i
	}
	return out
}

func decisionKeys(tr Trial) []string {
	out := make([]string, 0, len(tr.Decisions))
	for _, d := range tr.Decisions {
		mark := ""
		if d.Fired {
			mark = "!"
		}
		out = append(out, mark+decisionKey(d))
	}
	return out
}

func decisionKey(d fault.Decision) string {
	return fmt.Sprintf("%s|%s|%d", d.Site, d.Target, d.Visit)
}

func TestSingleFaultMalformed(t *testing.T) {
	s := Run(context.Background(), Config{
		Seed: 1, Trials: 20, P: 1, Faults: []fault.Type{fault.Malformed},
	})
	if s.Survival > 0.2 {
		t.Fatalf("p=1 malformed should almost never complete, got %v", s.Survival)
	}
	found := false
	for _, p := range s.Critique.Paragraphs {
		if contains(p, "missing fields") || contains(p, "transport success") {
			found = true
		}
	}
	if !found && !contains(s.Critique.Headline, "missing fields") {
		t.Fatalf("critique missed malformed architecture note: %+v", s.Critique)
	}
}

// TestArmedCostBudgetIsWhatTheTimelineSays: the budget scales with the tool
// surface, but the armed event logged the package default, so the timeline for a
// two-tool agent claimed a ceiling of 3 while the run actually stopped at 1.
func TestArmedCostBudgetIsWhatTheTimelineSays(t *testing.T) {
	bundle := ticketBundle(1, 0, false) // two tools -> budget 1
	s := Run(context.Background(), Config{
		Seed: 5, Trials: 24, P: 1, Agent: "pasted", Bundle: &bundle,
		Faults: []fault.Type{fault.CostCeiling},
	})
	want := costBudget(2)
	if want == fault.DefaultCostBudget {
		t.Fatalf("pick a tool surface whose budget differs from the default, got %d", want)
	}
	saw := 0
	for _, tr := range s.Trials {
		for _, ev := range tr.Events {
			if ev.Message != "cost ceiling armed" {
				continue
			}
			saw++
			if got := ev.Data["budget"]; fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("trial %d logged budget %v, armed %v", tr.N, got, want)
			}
		}
	}
	if saw == 0 {
		t.Fatal("no cost ceiling was armed at p=1")
	}
}

func eventMsgs(tr Trial) []string {
	out := make([]string, len(tr.Events))
	for i, ev := range tr.Events {
		out[i] = ev.Message
	}
	return out
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})())
}
