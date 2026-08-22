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

func TestGenericRecordExpect(t *testing.T) {
	w := world.SeedFixture(world.Fixture{
		Records: []world.Record{
			{ID: "tkt-1", Collection: "tickets", Fields: map[string]any{"status": "Open", "assignee": "ada"}},
		},
	})
	_ = w.Invoke("update_ticket", map[string]any{"id": "tkt-1", "status": "Resolved", "assignee": "ada"})
	writes, emails := 1, 0
	v := Judge(Expect{
		RecordID:     "tkt-1",
		Status:       "Resolved",
		RecordFields: map[string]any{"assignee": "ada"},
		Writes:       &writes,
		Emails:       &emails,
		Notify:       boolPtr(false),
	}, w, trace.New(), agent.Result{Claimed: agent.Claim{Wrote: true}})
	if v.Outcome != OutcomeCompleted {
		t.Fatalf("%+v", v)
	}
}

func TestGenericRecordWrongStatus(t *testing.T) {
	w := world.SeedFixture(world.Fixture{
		Records: []world.Record{
			{ID: "tkt-1", Fields: map[string]any{"status": "Open"}},
		},
	})
	_ = w.Invoke("update_ticket", map[string]any{"id": "tkt-1", "status": "Open"})
	v := Judge(Expect{RecordID: "tkt-1", Status: "Resolved"}, w, trace.New(), agent.Result{Claimed: agent.Claim{Wrote: true}})
	if v.Outcome != OutcomeFailed {
		t.Fatalf("want failed, got %+v", v)
	}
	found := false
	for _, x := range v.Violations {
		if x == "record_wrong_status" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected record_wrong_status in %+v", v.Violations)
	}
}

func TestJudgeUsesExpectNotAcmeDefaults(t *testing.T) {
	w := world.SeedCloseAcme()
	_ = w.WriteDeal("deal-supplies-1", "Closed-Won", 1200, "2026-12-15", "owner-pat")
	_ = w.SendEmail("pat@vendor.example", "done", "ok")
	v := Judge(Expect{
		DealID:     "deal-supplies-1",
		AE:         "pat@vendor.example",
		Amount:     1200,
		OwnerID:    "owner-pat",
		DealAction: "close_won",
		Notify:     boolPtr(true),
	}, w, trace.New(), agent.Result{Claimed: agent.Claim{Wrote: true, Notified: true}})
	if v.Outcome != OutcomeCompleted {
		t.Fatalf("supplies close should complete without Acme fields: %+v", v)
	}
}

func boolPtr(b bool) *bool { return &b }
