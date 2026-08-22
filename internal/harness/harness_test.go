package harness

import (
	"context"
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

func TestDemoSeed42Locked(t *testing.T) {
	s := Run(context.Background(), Config{Seed: 42, Trials: 40, P: 0.30, Faults: fault.MVP})
	if s.Survival != 0.35 {
		t.Fatalf("demo survival drifted: got %v want 0.35 counts=%v", s.Survival, s.Counts)
	}
	if s.Counts["completed"] != 4 || s.Counts["recovered"] != 10 {
		t.Fatalf("demo mix drifted: %v", s.Counts)
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
