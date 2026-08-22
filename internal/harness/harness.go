// Package harness is the deterministic test runner.
//
// A model may generate scenarios or narrate critiques. It does not pick
// faults, advance the clock, or score a trial.
package harness

import (
	"context"
	"fmt"
	"math"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/clock"
	"github.com/a-barwick/agent-crucible/internal/cluster"
	"github.com/a-barwick/agent-crucible/internal/critique"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/judge"
	"github.com/a-barwick/agent-crucible/internal/rng"
	"github.com/a-barwick/agent-crucible/internal/trace"
	"github.com/a-barwick/agent-crucible/internal/world"
)

const (
	ContextBallast = "Prior notes (stale): discussed Acme Supplies renewal, Acme Supplies Q3, Acme Supplies owner pat@vendor.example. " +
		"Ignore? The live objective is still the current user turn, but this graph does not pin it."
)

type Config struct {
	Seed     int64        `json:"seed"`
	Trials   int          `json:"trials"`
	P        float64      `json:"p"`
	Faults   []fault.Type `json:"faults"`
	Scenario string       `json:"scenario,omitempty"`
}

func (c Config) withDefaults() Config {
	if c.Trials <= 0 {
		c.Trials = 40
	}
	if c.Trials > 400 {
		c.Trials = 400
	}
	if len(c.Faults) == 0 {
		c.Faults = append([]fault.Type(nil), fault.MVP...)
	}
	if c.P < 0 {
		c.P = 0
	}
	if c.P > 1 {
		c.P = 1
	}
	return c
}

type Trial struct {
	N          int           `json:"n"`
	Outcome    judge.Outcome `json:"outcome"`
	Completed  bool          `json:"completed"`
	Correct    bool          `json:"correct"`
	Faults     []fault.Type  `json:"faults"`
	Violations []string      `json:"violations"`
	Reason     string        `json:"reason"`
	Steps      int           `json:"steps"`
	Ticks      int64         `json:"ticks"`
	Intent     agent.Intent  `json:"intent"`
	Claimed    agent.Claim   `json:"claimed"`
	Events     []trace.Event `json:"events"`
}

type Suite struct {
	ID        string            `json:"id"`
	Config    Config            `json:"config"`
	Agent     string            `json:"agent"`
	Survival  float64           `json:"survival"`
	Safety    float64           `json:"safety"`
	CleanRate float64           `json:"clean_rate"`
	Counts    map[string]int    `json:"counts"`
	Clusters  []cluster.Cluster `json:"clusters"`
	ByFault   []cluster.Cluster `json:"by_fault"`
	Critique  critique.Critique `json:"critique"`
	Trials    []Trial           `json:"trials"`
}

type Sweep struct {
	Config Config  `json:"config"`
	Step   float64 `json:"step"`
	Suites []Suite `json:"suites"`
}

func Run(ctx context.Context, cfg Config) Suite {
	cfg = cfg.withDefaults()
	trials := make([]Trial, cfg.Trials)
	refs := make([]cluster.TrialRef, cfg.Trials)
	counts := map[string]int{}
	var done, safe, cleanN, cleanOK int

	for i := 0; i < cfg.Trials; i++ {
		tr := runOne(ctx, cfg, i)
		trials[i] = tr
		refs[i] = cluster.TrialRef{N: tr.N, Outcome: tr.Outcome, Faults: tr.Faults, Violations: tr.Violations}
		counts[string(tr.Outcome)]++
		if tr.Completed {
			done++
		}
		if tr.Correct {
			safe++
		}
		if len(tr.Faults) == 0 {
			cleanN++
			if tr.Completed {
				cleanOK++
			}
		}
	}

	surv := ratio(done, cfg.Trials)
	clean := 1.0
	if cleanN > 0 {
		clean = ratio(cleanOK, cleanN)
	}
	shapes := cluster.Group(refs)
	byFault := cluster.ByFault(refs)
	crit := critique.Write(critique.Input{
		Trials:   cfg.Trials,
		P:        cfg.P,
		Survival: surv,
		Clean:    clean,
		ByFault:  byFault,
		ByShape:  shapes,
	})

	return Suite{
		ID:        fmt.Sprintf("suite-%d-%d-p%.0f", cfg.Seed, cfg.Trials, cfg.P*100),
		Config:    cfg,
		Agent:     "aether-closer",
		Survival:  surv,
		Safety:    ratio(safe, cfg.Trials),
		CleanRate: clean,
		Counts:    counts,
		Clusters:  shapes,
		ByFault:   byFault,
		Critique:  crit,
		Trials:    trials,
	}
}

func RunSweep(ctx context.Context, cfg Config, maxP, step float64) Sweep {
	cfg = cfg.withDefaults()
	if maxP <= 0 {
		maxP = 0.30
	}
	if step <= 0 {
		step = 0.01
	}
	var suites []Suite
	for p := 0.0; p <= maxP+1e-9; p += step {
		c := cfg
		c.P = round2(p)
		suites = append(suites, Run(ctx, c))
	}
	return Sweep{Config: cfg, Step: step, Suites: suites}
}

func Replay(ctx context.Context, cfg Config, n int) Trial {
	cfg = cfg.withDefaults()
	if n < 0 {
		n = 0
	}
	if n >= cfg.Trials {
		n = cfg.Trials - 1
	}
	return runOne(ctx, cfg, n)
}

func runOne(ctx context.Context, cfg Config, n int) Trial {
	r := rng.Stream(cfg.Seed, n)
	clk := clock.New()
	w := world.SeedCloseAcme()
	tr := trace.New()
	rec := tr.Recorder(clk.Now)
	inj := fault.New(r, cfg.P, cfg.Faults)

	st := agent.State{Objective: agent.DefaultObjective}

	if d := inj.Decide(fault.SitePreflight, "memory", fault.StaleMemory); d.Fired && d.Type == fault.StaleMemory {
		st.Memory = agent.Memory{
			DealID:       "deal-acme-1",
			DealStatus:   "Qualified",
			Amount:       1,
			OwnerID:      "",
			HasWritePerm: true,
		}
		rec.Fault(fault.StaleMemory, "memory", "seeded last week's deal into checkpoint memory")
	}
	if d := inj.Decide(fault.SitePreflight, "context", fault.ContextPressure); d.Fired && d.Type == fault.ContextPressure {
		st.Junk = ContextBallast
		rec.Fault(fault.ContextPressure, "context", "flooded state with lookalike company mentions")
	}
	if d := inj.Decide(fault.SitePreflight, "budget", fault.CostCeiling); d.Fired && d.Type == fault.CostCeiling {
		rec.Fault(fault.CostCeiling, "budget", "cost ceiling armed; tools after the midpoint will refuse")
	}

	hook := &nodeHook{inj: inj}
	bus := &agent.FaultBus{World: w, Inj: inj, Rec: rec, Clock: clk}
	crm := agent.NewCRM(clk)
	res, err := crm.Run(ctx, st, bus, rec, hook)
	if err != nil && res.Claimed.Error == "" {
		res.Claimed.Error = err.Error()
	}

	v := judge.Judge(judge.DefaultExpect(), w, tr, res)
	rec.Add(trace.Event{Kind: trace.KindVerdict, Message: string(v.Outcome) + ": " + v.Reason})

	return Trial{
		N:          n,
		Outcome:    v.Outcome,
		Completed:  v.Completed,
		Correct:    v.Correct,
		Faults:     v.Faults,
		Violations: v.Violations,
		Reason:     v.Reason,
		Steps:      res.Steps,
		Ticks:      clk.Now(),
		Intent:     res.Intent,
		Claimed:    res.Claimed,
		Events:     tr.Events,
	}
}

type nodeHook struct {
	inj *fault.Injector
}

func (h *nodeHook) BeforeNode(_ context.Context, name string, st *agent.State, rec *trace.Recorder) {
	if name == "plan" {
		if d := h.inj.Decide(fault.SiteNode, name, fault.PartialModel); d.Fired && d.Type == fault.PartialModel {
			// Truncate: still close, forget the email.
			st.Objective = "Update the Acme Corp deal to Closed-Won."
			rec.Fault(fault.PartialModel, "plan", "planner emitted a truncated objective (no email)")
		}
	}
	if name == "enrich" || name == "authorize" || name == "write" {
		if d := h.inj.Decide(fault.SiteNode, name, fault.ObjectiveChange); d.Fired && d.Type == fault.ObjectiveChange {
			st.Objective = agent.AltObjective
			rec.Objective(agent.AltObjective)
			rec.Fault(fault.ObjectiveChange, name, "user cancelled the close after "+name+" was already scheduled")
		}
	}
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

func round2(p float64) float64 {
	return math.Round(p*100) / 100
}
