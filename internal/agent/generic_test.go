package agent

import (
	"context"
	"testing"

	"github.com/a-barwick/agent-crucible/internal/clock"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/rng"
	"github.com/a-barwick/agent-crucible/internal/schema"
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

func TestPastedCustomToolsHappy(t *testing.T) {
	spec := Spec{
		Name: "ticket-bot",
		Tools: []schema.Tool{
			{Name: "search_ticket", Required: []string{"query"}},
			{Name: "update_ticket", Required: []string{"id", "status"}},
		},
		Companies: []string{"Acme Corp"},
		Objective: "Resolve the Acme Corp ticket.",
	}
	ag := NewFromSpec(spec, clock.New())
	clk := clock.New()
	w := world.SeedFixture(world.Fixture{
		Records: []world.Record{
			{ID: "tkt-acme", Fields: map[string]any{"company": "Acme Corp", "status": "Open"}},
		},
	})
	w.BindTools(spec.Tools)
	tr := trace.New()
	rec := tr.Recorder(clk.Now)
	inj := fault.New(rng.Stream(1, 0), 0, fault.MVP)
	bus := &FaultBus{World: w, Inj: inj, Rec: rec, Clock: clk}
	res, err := ag.Run(context.Background(), State{
		Objective: "Resolve the Acme Corp ticket.",
		Companies: []string{"Acme Corp"},
		ThreadID:  "tkt",
	}, bus, rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Claimed.Wrote {
		t.Fatalf("claimed %+v", res)
	}
	recd, ok := w.Record("tkt-acme")
	if !ok || recd.Status() != "Resolved" {
		t.Fatalf("world record %+v ok=%v", recd, ok)
	}
}

func TestParseIntentResolve(t *testing.T) {
	in := ParseIntent("Resolve the Acme Corp ticket.")
	if in.DealAction != "resolve" || in.Notify || ActionStatus(in.DealAction) != "Resolved" {
		t.Fatalf("%+v", in)
	}
}

func TestTruncateObjectiveDropsNotify(t *testing.T) {
	got := TruncateObjective(DefaultObjective)
	if got != "Update the Acme Corp deal to Closed-Won." {
		t.Fatalf("%q", got)
	}
	got = TruncateObjective("Resolve the Acme Corp ticket and email the owner.")
	if got != "Resolve the Acme Corp ticket." {
		t.Fatalf("%q", got)
	}
}

func TestPastedCustomToolsStaleMemory(t *testing.T) {
	spec := Spec{
		Name: "ticket-bot",
		Tools: []schema.Tool{
			{Name: "search_ticket", Required: []string{"query"}},
			{Name: "update_ticket", Required: []string{"id", "status"}},
		},
		Companies: []string{"Acme Corp", "Globex"},
	}
	ag := NewFromSpec(spec, clock.New())
	clk := clock.New()
	w := world.SeedFixture(world.Fixture{
		Records: []world.Record{
			{ID: "tkt-acme", Fields: map[string]any{"company": "Acme Corp", "status": "Open"}},
			{ID: "tkt-other", Fields: map[string]any{"company": "Globex", "status": "Open"}},
		},
	})
	tr := trace.New()
	rec := tr.Recorder(clk.Now)
	inj := fault.New(rng.Stream(1, 0), 0, fault.MVP)
	bus := &FaultBus{World: w, Inj: inj, Rec: rec, Clock: clk}
	_, err := ag.Run(context.Background(), State{
		Objective: "Resolve the Acme Corp ticket.",
		Companies: []string{"Acme Corp", "Globex"},
		ThreadID:  "stale",
		Memory:    Memory{DealID: "tkt-other", DealStatus: "Open"},
	}, bus, rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w.WritesFor("tkt-other") == 0 {
		t.Fatalf("stale memory should have overwritten the search id, writes=%v records=%v", w.Writes, w.Records)
	}
}

func TestPastedCustomToolsContextHijack(t *testing.T) {
	spec := Spec{
		Name: "ticket-bot",
		Tools: []schema.Tool{
			{Name: "search_ticket", Required: []string{"query"}},
			{Name: "update_ticket", Required: []string{"id", "status"}},
		},
		Companies: []string{"Acme Corp", "Globex"},
	}
	ag := NewFromSpec(spec, clock.New())
	clk := clock.New()
	w := world.SeedFixture(world.Fixture{
		Records: []world.Record{
			{ID: "tkt-acme", Fields: map[string]any{"company": "Acme Corp", "status": "Open"}},
			{ID: "tkt-other", Fields: map[string]any{"company": "Globex", "status": "Open"}},
		},
	})
	tr := trace.New()
	rec := tr.Recorder(clk.Now)
	inj := fault.New(rng.Stream(1, 0), 0, fault.MVP)
	bus := &FaultBus{World: w, Inj: inj, Rec: rec, Clock: clk}
	_, err := ag.Run(context.Background(), State{
		Objective: "Resolve the Acme Corp ticket.",
		Companies: []string{"Acme Corp", "Globex"},
		ThreadID:  "junk",
		Junk:      "Prior notes: discussed Globex renewal, Globex Q3.",
	}, bus, rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w.WritesFor("tkt-other") == 0 {
		t.Fatalf("context ballast should hijack search to Globex, writes=%v", w.Writes)
	}
}
