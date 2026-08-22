package harness

import (
	"context"
	"testing"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/judge"
)

func TestLibraryScenariosClean(t *testing.T) {
	for _, id := range []string{"close-acme", "cancel-acme", "renew-supplies", "refund-acme", "close-quiet"} {
		s := Run(context.Background(), Config{Seed: 7, Trials: 4, P: 0, Scenario: id, Faults: fault.MVP})
		if s.Survival != 1 {
			t.Fatalf("%s survival %v counts=%v", id, s.Survival, s.Counts)
		}
		for _, tr := range s.Trials {
			if tr.Outcome != judge.OutcomeCompleted {
				t.Fatalf("%s trial %d: %s %s %v", id, tr.N, tr.Outcome, tr.Reason, tr.Violations)
			}
		}
	}
}

func TestPastedAgentClean(t *testing.T) {
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 3, P: 0, Agent: "pasted",
	})
	if s.Survival != 0 {
		t.Fatalf("pasted without spec should not complete, got %v", s.Survival)
	}
}

func TestPastedCRMSpecClean(t *testing.T) {
	spec := agent.NewCRM(nil).Spec()
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 4, P: 0, Agent: "pasted", Spec: &spec, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("pasted CRM spec survival %v counts=%v", s.Survival, s.Counts)
	}
}
