package runtime

import (
	"context"
	"os"
	"testing"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/clock"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/judge"
	"github.com/a-barwick/agent-crucible/internal/rng"
	"github.com/a-barwick/agent-crucible/internal/trace"
	"github.com/a-barwick/agent-crucible/internal/world"
)

// TestMain reaps the sidecars these tests start. They are kept alive in package
// globals for reuse across a suite, so without this the test binary exits and
// leaves a Python server behind.
func TestMain(m *testing.M) {
	code := m.Run()
	StopAll()
	os.Exit(code)
}

func TestLangGraphHappyPath(t *testing.T) {
	if !HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	if FindDir() == "" {
		t.Skip("runtime dir not found")
	}
	ctx := context.Background()
	ag, err := NewRemote(ctx, RemoteOpts{Kind: "langgraph"})
	if err != nil {
		t.Fatal(err)
	}
	res, w := runRemote(t, ag)
	if !res.Claimed.Wrote || !res.Claimed.Notified {
		t.Fatalf("claimed %+v", res)
	}
	v := judge.Judge(judge.DefaultExpect(), w, trace.New(), res)
	if !v.Completed {
		t.Fatalf("judge %+v", v)
	}
}

func TestADKHappyPath(t *testing.T) {
	if !HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	if FindDir() == "" {
		t.Skip("runtime dir not found")
	}
	ctx := context.Background()
	ag, err := NewRemote(ctx, RemoteOpts{Kind: "adk"})
	if err != nil {
		t.Fatal(err)
	}
	res, w := runRemote(t, ag)
	if !res.Claimed.Wrote {
		t.Fatalf("claimed %+v", res)
	}
	d, _ := w.Deal("deal-acme-1")
	if d.Status != "Closed-Won" {
		t.Fatalf("world %+v", d)
	}
}

func runRemote(t *testing.T, ag agent.Agent) (agent.Result, *world.World) {
	t.Helper()
	clk := clock.New()
	w := world.SeedCloseAcme()
	tr := trace.New()
	rec := tr.Recorder(clk.Now)
	inj := fault.New(rng.Stream(1, 0), 0, fault.MVP)
	bus := &agent.FaultBus{World: w, Inj: inj, Rec: rec, Clock: clk}
	res, err := ag.Run(context.Background(), agent.State{
		Objective: agent.DefaultObjective,
		Companies: []string{"Acme Corp", "Acme Supplies"},
		ThreadID:  "itest",
	}, bus, rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res, w
}
