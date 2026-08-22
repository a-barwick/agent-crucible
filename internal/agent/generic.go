package agent

import (
	"context"
	"strings"

	"github.com/a-barwick/agent-crucible/internal/clock"
	"github.com/a-barwick/agent-crucible/internal/schema"
	"github.com/a-barwick/agent-crucible/internal/trace"
)

// Generic runs a pasted Spec: tool schemas + graph (+ optional node bindings).
// Standard closer nodes reuse the CRM patient so the same bugs still fire.
type Generic struct {
	spec  Spec
	clock *clock.Clock
	crm   *CRM
	Model Model
	Saver Checkpointer
}

func NewGeneric(spec Spec, clk *clock.Clock) *Generic {
	if clk == nil {
		clk = clock.New()
	}
	crm := NewCRM(clk)
	return &Generic{spec: spec, clock: clk, crm: crm, Model: crm.Model, Saver: crm.Saver}
}

// NewFromSpec builds an in-process agent from a pasted contract.
func NewFromSpec(spec Spec, clk *clock.Clock) Agent {
	if spec.Name == "" {
		spec.Name = "pasted"
	}
	if spec.Framework == "" {
		spec.Framework = "generic"
	}
	if spec.Graph.Start == "" && len(spec.Graph.Nodes) == 0 {
		spec.Graph = CRMGraphSpec()
		spec.Tools = append([]schema.Tool(nil), CRMTools()...)
	}
	return NewGeneric(spec, clk)
}

func (a *Generic) Spec() Spec { return a.spec }

func (a *Generic) Run(ctx context.Context, st State, bus Bus, rec *trace.Recorder, hook Hook) (Result, error) {
	if a.Saver != nil {
		a.crm.Saver = a.Saver
	}
	if a.Model != nil {
		a.crm.Model = a.Model
	}
	if st.Objective == "" && a.spec.Objective != "" {
		st.Objective = a.spec.Objective
	}
	if len(st.Companies) == 0 && len(a.spec.Companies) > 0 {
		st.Companies = a.spec.Companies
	}
	if st.ThreadID == "" {
		st.ThreadID = "pasted"
	}
	nodes := map[string]NodeFunc{}
	for _, name := range a.spec.Graph.Nodes {
		if name == "end" || name == "abort" {
			continue
		}
		bind := a.spec.NodeTools[name]
		if bind.Kind == "" {
			bind = inferBind(name, a.spec)
		}
		nodes[name] = a.makeNode(name, bind)
	}
	start := a.spec.Graph.Start
	if start == "" {
		start = "plan"
	}
	g := &Graph{
		Name:         a.spec.Name,
		Start:        start,
		Nodes:        nodes,
		MaxSteps:     20,
		Clock:        a.clock,
		Checkpointer: a.crm.Saver,
	}
	return g.Run(ctx, &st, bus, rec, hook)
}

func (a *Generic) makeNode(name string, bind NodeBinding) NodeFunc {
	switch bind.Kind {
	case "plan":
		return a.crm.plan
	case "lookup":
		return a.crm.lookup
	case "fetch":
		return a.crm.fetch
	case "enrich":
		return a.crm.enrich
	case "authorize":
		return a.crm.authorize
	case "write":
		return a.crm.write
	case "notify":
		return a.crm.notify
	case "tool":
		return a.toolNode(name, bind)
	default:
		return a.crm.plan
	}
}

func (a *Generic) toolNode(name string, bind NodeBinding) NodeFunc {
	return func(ctx context.Context, st *State, bus Bus, rec *trace.Recorder) (string, error) {
		args := map[string]any{}
		for arg, path := range bind.ArgsFrom {
			args[arg] = stateValue(st, path)
		}
		res, err := callRetry(ctx, bus, rec, bind.Tool, args)
		if err != nil {
			return "abort", err
		}
		if res.IsTransportError() {
			st.LastError = res.Error
			return "abort", nil
		}
		for from, to := range bind.Save {
			setState(st, to, schema.StringField(res.Data, from))
			if to == "amount" {
				st.Amount = schema.IntField(res.Data, from)
			}
		}
		return nextFrom(a.spec.Graph, name), nil
	}
}

func inferBind(name string, spec Spec) NodeBinding {
	switch name {
	case "plan":
		return NodeBinding{Kind: "plan"}
	case "lookup":
		return NodeBinding{Kind: "lookup", Tool: "lookup_contact"}
	case "fetch":
		return NodeBinding{Kind: "fetch", Tool: "get_deal"}
	case "enrich":
		return NodeBinding{Kind: "enrich"}
	case "authorize":
		return NodeBinding{Kind: "authorize", Tool: "check_permission"}
	case "write":
		return NodeBinding{Kind: "write", Tool: "write_deal"}
	case "notify":
		return NodeBinding{Kind: "notify", Tool: "send_email"}
	}
	for _, t := range spec.Tools {
		if t.Name == name || strings.Contains(name, t.Name) {
			return NodeBinding{Kind: "tool", Tool: t.Name}
		}
	}
	return NodeBinding{Kind: "plan"}
}

func stateValue(st *State, path string) any {
	switch path {
	case "intent.company", "company":
		return st.Intent.Company
	case "contact_id":
		return st.ContactID
	case "deal_id", "id":
		return st.DealID
	case "ae":
		return st.AE
	case "status":
		return st.Status
	case "amount":
		return st.Amount
	case "owner_id":
		return st.OwnerID
	case "close_date":
		return st.CloseDate
	default:
		return path
	}
}

func setState(st *State, field, val string) {
	switch field {
	case "contact_id":
		st.ContactID = val
	case "ae":
		st.AE = val
	case "deal_id", "id":
		st.DealID = val
	case "status":
		st.Status = val
	case "owner_id":
		st.OwnerID = val
	case "close_date":
		st.CloseDate = val
	}
}

func nextFrom(g GraphSpec, from string) string {
	for _, e := range g.Edges {
		if e.From == from && e.To != "abort" {
			return e.To
		}
	}
	return "end"
}
