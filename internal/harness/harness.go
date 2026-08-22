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
	"github.com/a-barwick/agent-crucible/internal/ai"
	"github.com/a-barwick/agent-crucible/internal/clock"
	"github.com/a-barwick/agent-crucible/internal/cluster"
	"github.com/a-barwick/agent-crucible/internal/critique"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/judge"
	"github.com/a-barwick/agent-crucible/internal/rng"
	"github.com/a-barwick/agent-crucible/internal/runtime"
	"github.com/a-barwick/agent-crucible/internal/scenario"
	"github.com/a-barwick/agent-crucible/internal/schema"
	"github.com/a-barwick/agent-crucible/internal/trace"
)

const (
	ContextBallast = "Prior notes (stale): discussed Acme Supplies renewal, Acme Supplies Q3, Acme Supplies owner pat@vendor.example. " +
		"Ignore? The live objective is still the current user turn, but this graph does not pin it."
)

type Config struct {
	Seed       int64               `json:"seed"`
	Trials     int                 `json:"trials"`
	P          float64             `json:"p"`
	Faults     []fault.Type        `json:"faults"`
	Scenario   string              `json:"scenario,omitempty"`
	Agent      string              `json:"agent,omitempty"`
	Spec       *agent.Spec         `json:"spec,omitempty"`
	Bundle     *scenario.Bundle    `json:"bundle,omitempty"`
	RuntimeURL string              `json:"runtime_url,omitempty"`
	AI         ai.Config           `json:"ai,omitempty"`
	Extra      []scenario.Scenario `json:"extra_scenarios,omitempty"`
}

func (c Config) withDefaults() Config {
	if c.Trials <= 0 {
		c.Trials = 40
	}
	if c.Trials > 400 {
		c.Trials = 400
	}
	if c.Agent == "" {
		c.Agent = agent.IDCloser
	}
	drop := agent.IsDropIn(c.Agent, specOf(c))
	if len(c.Faults) == 0 {
		if drop {
			c.Faults = append([]fault.Type(nil), fault.All...)
		} else {
			c.Faults = append([]fault.Type(nil), fault.MVP...)
		}
	}
	if c.Scenario == "" {
		if drop {
			c.Scenario = scenario.TicketID
		} else {
			c.Scenario = scenario.CloseAcmeID
		}
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
	Scenario  string            `json:"scenario"`
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
	samples := make([]ai.Evidence, 0, len(trials))
	for _, tr := range trials {
		var evs []string
		for _, e := range tr.Events {
			if e.Kind == "state" || e.Kind == "fault" {
				evs = append(evs, e.Message)
			}
		}
		samples = append(samples, ai.Evidence{
			N: tr.N, Outcome: tr.Outcome, Faults: tr.Faults,
			Violations: tr.Violations, Events: evs,
		})
	}
	crit := ai.Explain(ctx, ai.ExplainInput{
		Trials:   cfg.Trials,
		P:        cfg.P,
		Survival: surv,
		Clean:    clean,
		ByFault:  byFault,
		ByShape:  shapes,
		Samples:  samples,
		Tools:    toolNames(cfg),
		Agent:    cfg.Agent,
		Client:   ai.FromEnv(cfg.AI),
	})

	return Suite{
		ID:        fmt.Sprintf("suite-%d-%d-p%.0f", cfg.Seed, cfg.Trials, cfg.P*100),
		Config:    cfg,
		Agent:     cfg.Agent,
		Scenario:  cfg.Scenario,
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
	scn := resolveScenario(cfg)
	spec := specOf(cfg)
	scn.Expect = resolveExpect(scn)

	r := rng.Stream(cfg.Seed, n)
	clk := clock.New()
	w := scn.World()
	if spec != nil && len(spec.Tools) > 0 {
		w.BindTools(spec.Tools)
	}
	tr := trace.New()
	rec := tr.Recorder(clk.Now)
	inj := fault.New(r, cfg.P, cfg.Faults)
	saver := agent.NewMemorySaver()

	st := agent.State{
		Objective: scn.Objective,
		Companies: scn.Companies,
		ThreadID:  fmt.Sprintf("%d-%d", cfg.Seed, n),
	}

	if d := inj.Decide(fault.SitePreflight, "memory", fault.StaleMemory); d.Fired && d.Type == fault.StaleMemory {
		st.Memory = scn.StaleMemory
		if st.Memory.DealID == "" {
			st.Memory = scenario.DefaultStale()
		}
		saver.Put(st.ThreadID, agent.Checkpoint{State: st, Node: "memory"})
		rec.Fault(fault.StaleMemory, "memory", "seeded last week's deal into checkpoint memory")
	}
	if d := inj.Decide(fault.SitePreflight, "context", fault.ContextPressure); d.Fired && d.Type == fault.ContextPressure {
		st.Junk = scn.ContextBallast
		if st.Junk == "" {
			st.Junk = ContextBallast
		}
		rec.Fault(fault.ContextPressure, "context", "flooded state with lookalike company mentions")
	}
	if d := inj.Decide(fault.SitePreflight, "budget", fault.CostCeiling); d.Fired && d.Type == fault.CostCeiling {
		rec.Fault(fault.CostCeiling, "budget", "cost ceiling armed; tools after the midpoint will refuse")
	}

	hook := &nodeHook{inj: inj, alt: scn.AltObjective}
	bus := &agent.FaultBus{World: w, Inj: inj, Rec: rec, Clock: clk}
	ag, err := resolveAgent(ctx, cfg, clk, saver)
	if err != nil {
		res := agent.Result{Claimed: agent.Claim{Error: err.Error()}}
		v := judge.Judge(scn.Expect, w, tr, res)
		rec.Add(trace.Event{Kind: trace.KindVerdict, Message: "failed: " + err.Error()})
		return Trial{N: n, Outcome: v.Outcome, Reason: err.Error(), Events: tr.Events}
	}
	res, err := ag.Run(ctx, st, bus, rec, hook)
	if err != nil && res.Claimed.Error == "" {
		res.Claimed.Error = err.Error()
	}

	v := judge.Judge(scn.Expect, w, tr, res)
	if v.Ambiguous {
		var evs []string
		for _, e := range tr.Events {
			evs = append(evs, e.Message)
		}
		v = ai.Evaluate(ctx, v, res, evs, ai.FromEnv(cfg.AI))
	}
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
	alt string
}

func (h *nodeHook) BeforeNode(_ context.Context, name string, st *agent.State, rec *trace.Recorder) {
	if name == "plan" {
		if d := h.inj.Decide(fault.SiteNode, name, fault.PartialModel); d.Fired && d.Type == fault.PartialModel {
			st.Partial = true
			st.Objective = agent.TruncateObjective(st.Objective)
			rec.Fault(fault.PartialModel, "plan", "planner emitted a truncated objective (no email)")
		}
	}
	if name == "enrich" || name == "authorize" || name == "write" || schema.IsWriteLike(name) {
		if d := h.inj.Decide(fault.SiteNode, name, fault.ObjectiveChange); d.Fired && d.Type == fault.ObjectiveChange {
			obj := h.alt
			if obj == "" {
				obj = agent.AltObjective
			}
			st.Objective = obj
			rec.Objective(obj)
			rec.Fault(fault.ObjectiveChange, name, "user cancelled the objective after "+name+" was already scheduled")
		}
	}
}

func resolveScenario(cfg Config) scenario.Scenario {
	if cfg.Bundle != nil {
		b := cfg.Bundle.Scenario
		if b.Objective != "" || b.ID != "" || b.Expect.Specified() || b.Fixture != nil {
			if b.ID == "" && cfg.Scenario != "" {
				b.ID = cfg.Scenario
			}
			return b
		}
	}
	if agent.IsDropIn(cfg.Agent, specOf(cfg)) && (cfg.Scenario == "" || cfg.Scenario == scenario.CloseAcmeID) {
		return scenario.Ticket()
	}
	if s, ok := scenario.Lookup(cfg.Scenario); ok {
		return s
	}
	for _, s := range cfg.Extra {
		if s.ID == cfg.Scenario {
			return s
		}
	}
	return scenario.Get(cfg.Scenario)
}

func specOf(cfg Config) *agent.Spec {
	if cfg.Bundle != nil {
		s := cfg.Bundle.Spec
		if s.Name != "" || len(s.Tools) > 0 || len(s.Graph.Nodes) > 0 || s.Entry != "" || s.Endpoint != "" {
			return &s
		}
	}
	return cfg.Spec
}

func resolveExpect(scn scenario.Scenario) judge.Expect {
	e := scn.Expect
	if e.Objective == "" {
		e.Objective = scn.Objective
	}
	if e.Specified() {
		return e
	}
	if scn.Fixture != nil && len(scn.Fixture.Records) > 0 {
		rec := scn.Fixture.Records[0]
		if e.RecordID == "" {
			e.RecordID = rec.ID
		}
		if e.Status == "" {
			e.Status = agent.ActionStatus(agent.ParseIntent(e.Objective).DealAction)
		}
		return e
	}
	def := judge.DefaultExpect()
	if e.Objective != "" {
		def.Objective = e.Objective
	}
	return def
}

func resolveAgent(ctx context.Context, cfg Config, clk *clock.Clock, saver agent.Checkpointer) (agent.Agent, error) {
	name := cfg.Agent
	if cfg.Bundle != nil && (name == "" || name == agent.IDPasted) {
		name = agent.IDPasted
	}
	spec := hydrateSpec(cfg)

	if spec != nil && spec.Endpoint != "" {
		kind := spec.Runtime
		if kind == "" {
			kind = "langgraph"
		}
		return runtime.NewRemote(ctx, runtime.RemoteOpts{Kind: kind, URL: spec.Endpoint, Spec: spec})
	}
	if spec != nil && spec.Command != "" && (name == agent.IDForeignHTTP || name == agent.IDPasted || spec.Runtime == "wrap") {
		return runtime.NewWrap(ctx, spec)
	}

	switch name {
	case "", agent.IDCloser:
		c := agent.NewCRM(clk)
		c.Saver = saver
		c.Model = agent.ScriptedModel{}
		return c, nil
	case agent.IDPasted:
		if spec == nil {
			return nil, fmt.Errorf("pasted agent needs an entry file, an endpoint, or tool schemas")
		}
		if spec.Command != "" && spec.Runtime == "wrap" {
			return runtime.NewWrap(ctx, spec)
		}
		if spec.Entry != "" || spec.Runtime == "langgraph" || spec.Runtime == "adk" {
			return runtime.NewRemote(ctx, runtime.RemoteOpts{Kind: spec.Runtime, URL: cfg.RuntimeURL, Spec: spec})
		}
		ag := agent.NewFromSpec(*spec, clk)
		if g, ok := ag.(*agent.Generic); ok {
			g.Saver = saver
			g.Model = agent.ScriptedModel{}
		}
		return ag, nil
	case agent.IDCloserLangGraph, agent.IDTicketLangGraph, agent.IDNativeLangGraph, agent.IDNativeOpenAI, agent.IDNativeReact, agent.IDHTTPClosure:
		kind := "langgraph"
		if spec != nil && (agent.JSRuntime(spec.Runtime) || agent.JSEntry(spec.Entry)) {
			kind = "js"
		}
		return runtime.NewRemote(ctx, runtime.RemoteOpts{Kind: kind, URL: cfg.RuntimeURL, Spec: spec})
	case agent.IDCloserADK, agent.IDTicketADK, agent.IDNativeADK:
		return runtime.NewRemote(ctx, runtime.RemoteOpts{Kind: "adk", URL: cfg.RuntimeURL, Spec: spec})
	case agent.IDForeignHTTP:
		if spec != nil && spec.Entry != "" {
			return runtime.NewRemote(ctx, runtime.RemoteOpts{Kind: "langgraph", URL: cfg.RuntimeURL, Spec: spec})
		}
		return runtime.NewWrap(ctx, spec)
	case agent.IDNativeJS:
		url := cfg.RuntimeURL
		if spec != nil && spec.Endpoint != "" {
			url = spec.Endpoint
		}
		return runtime.NewRemote(ctx, runtime.RemoteOpts{Kind: "js", URL: url, Spec: spec})
	case agent.IDRemote:
		url := cfg.RuntimeURL
		if spec != nil && spec.Endpoint != "" {
			url = spec.Endpoint
		}
		if url == "" {
			return nil, fmt.Errorf("remote agent needs spec.endpoint or -endpoint")
		}
		kind := "langgraph"
		if spec != nil && spec.Runtime != "" {
			kind = spec.Runtime
		}
		return runtime.NewRemote(ctx, runtime.RemoteOpts{Kind: kind, URL: url, Spec: spec})
	default:
		if spec != nil && spec.Command != "" && spec.Runtime == "wrap" {
			return runtime.NewWrap(ctx, spec)
		}
		if spec != nil && (spec.Entry != "" || spec.Endpoint != "") {
			kind := spec.Runtime
			if kind == "" {
				kind = "langgraph"
			}
			if agent.JSRuntime(kind) || agent.JSEntry(spec.Entry) {
				kind = "js"
			}
			return runtime.NewRemote(ctx, runtime.RemoteOpts{Kind: kind, URL: cfg.RuntimeURL, Spec: spec})
		}
		return nil, fmt.Errorf("unknown agent %q — drop in an entry/endpoint, paste a spec, or pick a catalog id", name)
	}
}

func hydrateSpec(cfg Config) *agent.Spec {
	spec := specOf(cfg)
	switch cfg.Agent {
	case agent.IDTicketLangGraph, agent.IDNativeLangGraph:
		spec = overlaySpec(agent.TicketLangGraphSpec(), spec)
	case agent.IDTicketADK, agent.IDNativeADK:
		spec = overlaySpec(agent.TicketADKSpec(), spec)
	case agent.IDNativeOpenAI:
		spec = overlaySpec(agent.NativeOpenAISpec(), spec)
	case agent.IDNativeJS:
		spec = overlaySpec(agent.NativeJSSpec(), spec)
	case agent.IDNativeReact:
		spec = overlaySpec(agent.NativeReactSpec(), spec)
	case agent.IDHTTPClosure:
		spec = overlaySpec(agent.HTTPClosureSpec(), spec)
	case agent.IDForeignHTTP:
		spec = overlaySpec(agent.ForeignHTTPSpec(), spec)
	}
	if spec != nil && spec.Entry != "" {
		cp := *spec
		cp.Entry = runtime.FindEntry(spec.Entry)
		spec = &cp
	}
	return spec
}

func overlaySpec(base agent.Spec, over *agent.Spec) *agent.Spec {
	if over == nil {
		return &base
	}
	out := base
	if over.Name != "" {
		out.Name = over.Name
	}
	if over.Runtime != "" {
		out.Runtime = over.Runtime
	}
	if over.Entry != "" {
		out.Entry = over.Entry
	}
	if over.Export != "" {
		out.Export = over.Export
	}
	if over.Endpoint != "" {
		out.Endpoint = over.Endpoint
	}
	if over.Command != "" {
		out.Command = over.Command
	}
	if len(over.Tools) > 0 {
		out.Tools = over.Tools
	}
	if len(over.Graph.Nodes) > 0 {
		out.Graph = over.Graph
	}
	if len(over.Companies) > 0 {
		out.Companies = over.Companies
	}
	if over.Objective != "" {
		out.Objective = over.Objective
	}
	return &out
}

func toolNames(cfg Config) []string {
	spec := hydrateSpec(cfg)
	if spec == nil || len(spec.Tools) == 0 {
		if agent.IsDropIn(cfg.Agent, spec) {
			spec = overlaySpec(agent.TicketLangGraphSpec(), spec)
		} else {
			var out []string
			for _, t := range agent.CRMTools() {
				out = append(out, t.Name)
			}
			return out
		}
	}
	out := make([]string, 0, len(spec.Tools))
	for _, t := range spec.Tools {
		out = append(out, t.Name)
	}
	return out
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
