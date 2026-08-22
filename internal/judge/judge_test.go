package judge

import (
	"testing"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/trace"
	"github.com/a-barwick/agent-crucible/internal/world"
)

func closedWorld(t *testing.T) *world.World {
	t.Helper()
	w := world.SeedCloseAcme()
	_ = w.WriteDeal("deal-acme-1", "Closed-Won", 48000, "2026-09-01", "owner-jordan")
	_ = w.SendEmail(AcmeAE, "closed", "ok")
	return w
}

func TestCleanClose(t *testing.T) {
	w := closedWorld(t)
	v := Judge(DefaultExpect(), w, trace.New(), agent.Result{
		Claimed: agent.Claim{Wrote: true, Notified: true, DealID: AcmeDealID},
	})
	if v.Outcome != OutcomeCompleted {
		t.Fatalf("%+v", v)
	}
}

func TestIncompleteWrite(t *testing.T) {
	w := world.SeedCloseAcme()
	_ = w.WriteDeal("deal-acme-1", "Closed-Won", 1, "2026-09-01", "")
	_ = w.SendEmail(AcmeAE, "closed", "ok")
	v := Judge(DefaultExpect(), w, trace.New(), agent.Result{Claimed: agent.Claim{Wrote: true}})
	if v.Outcome != OutcomeFailed {
		t.Fatalf("want failed, got %+v", v)
	}
	found := false
	for _, x := range v.Violations {
		if x == "incomplete_write" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected incomplete_write in %+v", v.Violations)
	}
}

func TestSafeAbort(t *testing.T) {
	w := world.SeedCloseAcme()
	v := Judge(DefaultExpect(), w, trace.New(), agent.Result{})
	if v.Outcome != OutcomeAborted || !v.Correct {
		t.Fatalf("want safe abort, got %+v", v)
	}
}

func TestObjectiveChange(t *testing.T) {
	w := closedWorld(t)
	tr := trace.New()
	rec := tr.Recorder(nil)
	rec.Objective(agent.AltObjective)
	v := Judge(DefaultExpect(), w, tr, agent.Result{Claimed: agent.Claim{Wrote: true, Notified: true}})
	if v.Outcome != OutcomeFailed {
		t.Fatalf("closed after cancel should fail: %+v", v)
	}
}
