package cluster

import (
	"testing"

	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/judge"
)

func TestGroupByShape(t *testing.T) {
	trials := []TrialRef{
		{N: 0, Outcome: judge.OutcomeCompleted},
		{N: 1, Outcome: judge.OutcomeFailed, Faults: []fault.Type{fault.Malformed}, Violations: []string{"incomplete_write"}},
		{N: 2, Outcome: judge.OutcomeFailed, Faults: []fault.Type{fault.Malformed}, Violations: []string{"incomplete_write"}},
		{N: 3, Outcome: judge.OutcomeAborted, Faults: []fault.Type{fault.Timeout}, Violations: []string{"deal_not_closed"}},
	}
	cs := Group(trials)
	if len(cs) != 3 {
		t.Fatalf("clusters %d: %+v", len(cs), cs)
	}
	if cs[0].N != 2 || cs[0].Fault != fault.Malformed {
		t.Fatalf("expected malformed pair first, got %+v", cs[0])
	}
}

func TestByFaultRollup(t *testing.T) {
	trials := []TrialRef{
		{N: 0, Outcome: judge.OutcomeFailed, Faults: []fault.Type{fault.Timeout, fault.Malformed}},
		{N: 1, Outcome: judge.OutcomeCompleted},
	}
	cs := ByFault(trials)
	if len(cs) != 3 {
		t.Fatalf("%+v", cs)
	}
}
