package agent

import (
	"context"
	"testing"

	"github.com/a-barwick/agent-crucible/internal/clock"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/rng"
	"github.com/a-barwick/agent-crucible/internal/trace"
	"github.com/a-barwick/agent-crucible/internal/world"
)

func TestCRMHappyPath(t *testing.T) {
	clk := clock.New()
	w := world.SeedCloseAcme()
	tr := trace.New()
	rec := tr.Recorder(clk.Now)
	inj := fault.New(rng.Stream(1, 0), 0, fault.MVP)
	bus := &FaultBus{World: w, Inj: inj, Rec: rec, Clock: clk}
	res, err := NewCRM(clk).Run(context.Background(), State{Objective: DefaultObjective}, bus, rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Terminal != "end" || !res.Claimed.Wrote || !res.Claimed.Notified {
		t.Fatalf("result %+v", res)
	}
	d, _ := w.Deal("deal-acme-1")
	if d.Status != "Closed-Won" || w.EmailsTo("jordan@vendor.example") != 1 {
		t.Fatalf("world %+v emails=%d", d, w.EmailsTo("jordan@vendor.example"))
	}
}

func TestParseIntentCancel(t *testing.T) {
	in := ParseIntent(AltObjective)
	if in.DealAction != "on_hold" || in.Notify {
		t.Fatalf("%+v", in)
	}
}

func TestStaleMemoryWins(t *testing.T) {
	clk := clock.New()
	w := world.SeedCloseAcme()
	tr := trace.New()
	rec := tr.Recorder(clk.Now)
	inj := fault.New(rng.Stream(1, 0), 0, fault.MVP)
	bus := &FaultBus{World: w, Inj: inj, Rec: rec, Clock: clk}
	st := State{
		Objective: DefaultObjective,
		Memory:    Memory{DealID: "deal-acme-1", Amount: 1, OwnerID: "nobody", DealStatus: "Qualified"},
	}
	_, err := NewCRM(clk).Run(context.Background(), st, bus, rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := w.Deal("deal-acme-1")
	if d.Amount != 1 || d.OwnerID != "nobody" {
		t.Fatalf("expected stale fields written, got %+v", d)
	}
}
