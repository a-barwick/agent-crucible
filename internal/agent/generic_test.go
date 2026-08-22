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

func TestPastedCRMSpecHappy(t *testing.T) {
	spec := NewCRM(nil).Spec()
	spec.Name = "pasted-closer"
	ag := NewFromSpec(spec, clock.New())
	clk := clock.New()
	w := world.SeedCloseAcme()
	tr := trace.New()
	rec := tr.Recorder(clk.Now)
	inj := fault.New(rng.Stream(1, 0), 0, fault.MVP)
	bus := &FaultBus{World: w, Inj: inj, Rec: rec, Clock: clk}
	res, err := ag.Run(context.Background(), State{Objective: DefaultObjective, ThreadID: "p1"}, bus, rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Claimed.Wrote || !res.Claimed.Notified {
		t.Fatalf("%+v", res)
	}
	d, _ := w.Deal("deal-acme-1")
	if d.Status != "Closed-Won" {
		t.Fatalf("%+v", d)
	}
}

func TestCheckpointerPersists(t *testing.T) {
	saver := NewMemorySaver()
	saver.Put("t1", Checkpoint{State: State{Memory: Memory{DealID: "x"}}, Node: "memory"})
	cp, ok := saver.Get("t1")
	if !ok || cp.State.Memory.DealID != "x" {
		t.Fatalf("%v %+v", ok, cp)
	}
}

func TestScriptedModelPartialDropsNotify(t *testing.T) {
	resp, err := ScriptedModel{}.Complete(context.Background(), ModelReq{Objective: DefaultObjective, Partial: true})
	if err != nil {
		t.Fatal(err)
	}
	in := ParseModelIntent(resp.Text, DefaultObjective, nil)
	if in.Notify {
		t.Fatalf("partial should drop notify: %s", resp.Text)
	}
}
