package world

import (
	"testing"

	"github.com/a-barwick/agent-crucible/internal/schema"
)

func TestHappyWriteAndEmail(t *testing.T) {
	w := SeedCloseAcme()
	got := w.LookupContact("Acme Corp")
	if !got.OK || got.Data["id"] != "ct-acme" {
		t.Fatalf("lookup: %+v", got)
	}
	deal := w.GetDeal("ct-acme")
	if !deal.OK || deal.Data["id"] != "deal-acme-1" {
		t.Fatalf("deal: %+v", deal)
	}
	wr := w.WriteDeal("deal-acme-1", "Closed-Won", 48000, "2026-09-01", "owner-jordan")
	if !wr.OK {
		t.Fatalf("write: %+v", wr)
	}
	em := w.SendEmail("jordan@vendor.example", "done", "ok")
	if !em.OK {
		t.Fatalf("email: %+v", em)
	}
	d, _ := w.Deal("deal-acme-1")
	if d.Status != "Closed-Won" || w.EmailsTo("jordan@vendor.example") != 1 {
		t.Fatalf("world after close: %+v emails=%d", d, w.EmailsTo("jordan@vendor.example"))
	}
}

func TestWriteDenied(t *testing.T) {
	w := SeedCloseAcme()
	w.Revoke("agent-bot", PermWrite)
	res := w.WriteDeal("deal-acme-1", "Closed-Won", 48000, "2026-09-01", "owner-jordan")
	if res.Error != "permission_denied" || w.UnauthorizedAttempts != 1 {
		t.Fatalf("expected deny, got %+v attempts=%d", res, w.UnauthorizedAttempts)
	}
}

func TestCloneIsolates(t *testing.T) {
	a := SeedCloseAcme()
	b := a.Clone()
	_ = a.WriteDeal("deal-acme-1", "Closed-Won", 48000, "2026-09-01", "owner-jordan")
	d, _ := b.Deal("deal-acme-1")
	if d.Status != "Negotiation" {
		t.Fatal("clone shared deals")
	}
}

func TestGenericRecordWrite(t *testing.T) {
	w := SeedFixture(Fixture{
		Records: []Record{
			{ID: "tkt-1", Collection: "tickets", Fields: map[string]any{"company": "Acme Corp", "status": "Open"}},
			{ID: "tkt-2", Collection: "tickets", Fields: map[string]any{"company": "Globex", "status": "Open"}},
		},
		Tools: []schema.Tool{{Name: "search_ticket"}, {Name: "update_ticket"}},
	})
	got := w.Invoke("search_ticket", map[string]any{"query": "Acme Corp"})
	if !got.OK || schema.StringField(got.Data, "id") != "tkt-1" {
		t.Fatalf("search: %+v", got)
	}
	wr := w.Invoke("update_ticket", map[string]any{"id": "tkt-1", "status": "Resolved"})
	if !wr.OK {
		t.Fatalf("update: %+v", wr)
	}
	rec, ok := w.Record("tkt-1")
	if !ok || rec.Status() != "Resolved" || w.WritesFor("tkt-1") != 1 {
		t.Fatalf("record %+v ok=%v writes=%d", rec, ok, w.WritesFor("tkt-1"))
	}
	other, _ := w.Record("tkt-2")
	if other.Status() != "Open" {
		t.Fatal("lookalike mutated")
	}
}

func TestGenericUnknownToolNotFound(t *testing.T) {
	w := SeedFixture(Fixture{Records: []Record{{ID: "x", Fields: map[string]any{"status": "a"}}}})
	got := w.Invoke("search_ticket", map[string]any{"query": "nope"})
	if got.OK || got.Error != "not_found" {
		t.Fatalf("%+v", got)
	}
}

func TestInvokeCRMStillWorks(t *testing.T) {
	w := SeedCloseAcme()
	got := w.Invoke("lookup_contact", map[string]any{"company": "Acme Corp"})
	if !got.OK || got.Data["id"] != "ct-acme" {
		t.Fatalf("%+v", got)
	}
}
