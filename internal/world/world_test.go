package world

import "testing"

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
