package scenario

import "testing"

func TestLibraryHasFive(t *testing.T) {
	lib := Library()
	if len(lib) < 5 {
		t.Fatalf("library %d", len(lib))
	}
	if Get("").ID != CloseAcmeID {
		t.Fatal("default is close-acme")
	}
	if Get("refund-acme").Expect.DealAction != "refund" {
		t.Fatal("refund scenario")
	}
	if Get("renew-supplies").Expect.DealID != "deal-supplies-1" {
		t.Fatal("supplies expect")
	}
	if _, ok := Lookup("gen-resolve-primary"); ok {
		t.Fatal("generated ids are not in the library")
	}
	if _, ok := Lookup(""); ok {
		t.Fatal("empty id is not a hit")
	}
	if Get(TicketID).Expect.RecordID != "tkt-acme" {
		t.Fatal("ticket expect is record-shaped")
	}
}

func TestWorldIsolated(t *testing.T) {
	a := Get(CloseAcmeID).World()
	b := Get(CloseAcmeID).World()
	_ = a.WriteDeal("deal-acme-1", "Closed-Won", 48000, "2026-09-01", "owner-jordan")
	d, _ := b.Deal("deal-acme-1")
	if d.Status != "Negotiation" {
		t.Fatal("worlds shared")
	}
}
