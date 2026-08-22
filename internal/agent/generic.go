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
// Custom tool names walk the generic world instead of the CRM switch.
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
		if len(spec.Tools) > 0 && !looksLikeCRM(spec.Tools) {
			spec.Graph = graphFromTools(spec.Tools)
		} else {
			spec.Graph = CRMGraphSpec()
			if len(spec.Tools) == 0 {
				spec.Tools = append([]schema.Tool(nil), CRMTools()...)
			}
		}
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
	if st.Intent.EntityName() == "" && st.Objective != "" {
		st.Intent = ParseIntentWith(st.Objective, st.Companies)
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
		nodes[name] = a.remap(name, a.makeNode(name, bind))
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

func (a *Generic) remap(name string, fn NodeFunc) NodeFunc {
	return func(ctx context.Context, st *State, bus Bus, rec *trace.Recorder) (string, error) {
		next, err := fn(ctx, st, bus, rec)
		if err != nil {
			return next, err
		}
		if next == "" || next == "end" || next == "abort" {
			return next, nil
		}
		if a.hasNode(next) {
			return next, nil
		}
		return nextFrom(a.spec.Graph, name, rec), nil
	}
}

func (a *Generic) hasNode(name string) bool {
	for _, n := range a.spec.Graph.Nodes {
		if n == name {
			return true
		}
	}
	return false
}

func (a *Generic) toolNode(name string, bind NodeBinding) NodeFunc {
	return func(ctx context.Context, st *State, bus Bus, rec *trace.Recorder) (string, error) {
		args := inferArgs(st, bind.Tool, a.spec)
		for arg, path := range bind.ArgsFrom {
			args[arg] = stateValue(st, path)
		}
		hijackReadArgs(st, bind.Tool, args, rec)
		res, err := callRetry(ctx, bus, rec, bind.Tool, args)
		if err != nil {
			return "abort", err
		}
		if res.IsTransportError() {
			st.LastError = res.Error
			return "abort", nil
		}
		applySaves(st, res.Data, bind.Save)
		overwriteFromMemory(st, bind.Tool, rec)
		if schema.IsWriteLike(bind.Tool) {
			// Same bug as the CRM write node: a non-timeout envelope is "done".
			st.Wrote = true
			if s := schema.StringField(res.Data, "status"); s != "" {
				st.Status = s
			} else if s := schema.StringField(args, "status"); s != "" {
				st.Status = s
			}
			if res.Error == "permission_denied" {
				rec.State("write ignored permission_denied", map[string]any{"tool": bind.Tool})
			}
			if res.OK && (res.Data == nil || len(res.Data) == 0) {
				rec.State("write accepted empty success payload", map[string]any{"tool": bind.Tool})
			}
		}
		if schema.IsEmailLike(bind.Tool) {
			st.Notified = res.OK || res.Error == ""
		}
		return nextFrom(a.spec.Graph, name, rec), nil
	}
}

func hijackReadArgs(st *State, tool string, args map[string]any, rec *trace.Recorder) {
	if schema.IsWriteLike(tool) || schema.IsEmailLike(tool) || schema.IsPermissionLike(tool) {
		return
	}
	if st.Junk == "" {
		return
	}
	hijack := lastCompany(st.Junk, st.Companies)
	if hijack == "" || hijack == st.Intent.EntityName() {
		return
	}
	changed := false
	for _, k := range []string{"query", "company", "name", "title"} {
		if _, ok := args[k]; ok {
			args[k] = hijack
			changed = true
		}
	}
	if changed {
		rec.State("lookup hijacked by context ballast", map[string]any{"company": hijack, "tool": tool})
	}
}

// overwriteFromMemory is the stale-memory bug in the generic runner: a populated
// checkpoint beats whatever the read just returned. It only logs when it changed
// something, so a graph with four read nodes reports one stale-memory event
// rather than four identical ones.
func overwriteFromMemory(st *State, tool string, rec *trace.Recorder) {
	if schema.IsWriteLike(tool) || schema.IsEmailLike(tool) || schema.IsPermissionLike(tool) {
		return
	}
	id := st.Memory.TargetID()
	if id == "" {
		return
	}
	changed := st.DealID != id || st.RecordID != id
	st.DealID = id
	st.RecordID = id
	if s := st.Memory.DealStatus; s != "" && st.Status != s {
		st.Status = s
		changed = true
	}
	if a := st.Memory.Amount; a != 0 && st.Amount != a {
		st.Amount = a
		changed = true
	}
	if !changed {
		return
	}
	rec.State("enrich trusted stale memory", map[string]any{"deal_id": st.DealID, "record_id": id, "tool": tool})
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
		if t.Name == name || strings.Contains(name, t.Name) || strings.Contains(t.Name, name) {
			return NodeBinding{Kind: "tool", Tool: t.Name}
		}
	}
	want := schema.Classify(name)
	if want != schema.KindRead || schema.IsWriteLike(name) || schema.IsEmailLike(name) || schema.IsPermissionLike(name) {
		for _, t := range spec.Tools {
			if schema.Classify(t.Name) == want {
				return NodeBinding{Kind: "tool", Tool: t.Name}
			}
		}
	}
	return NodeBinding{Kind: "plan"}
}

func inferArgs(st *State, tool string, spec Spec) map[string]any {
	args := map[string]any{}
	t, ok := schema.Find(spec.Tools, tool)
	required := t.Required
	if !ok || len(required) == 0 {
		required = defaultArgNames(tool)
	}
	for _, name := range required {
		if v, ok := argFromState(st, name, schema.Classify(tool)); ok {
			args[name] = v
		}
	}
	if schema.IsWriteLike(tool) {
		if s := ActionStatus(st.Intent.ActionName()); s != "" {
			if _, exists := args["status"]; !exists || schema.StringField(args, "status") == "" {
				args["status"] = s
			}
		}
	}
	return args
}

func defaultArgNames(tool string) []string {
	switch schema.Classify(tool) {
	case schema.KindWrite:
		return []string{"id", "status"}
	case schema.KindEmail:
		return []string{"to", "subject", "body"}
	case schema.KindPermission:
		return []string{"perm"}
	default:
		return []string{"query", "company", "id"}
	}
}

func argFromState(st *State, name string, kind schema.Kind) (any, bool) {
	switch name {
	case "company", "query", "name", "title", "entity":
		if st.Intent.EntityName() != "" {
			return st.Intent.EntityName(), true
		}
	case "id", "record_id", "ticket_id", "deal_id":
		if id := st.TargetID(); id != "" {
			return id, true
		}
	case "contact_id":
		if st.ContactID != "" {
			return st.ContactID, true
		}
	case "status":
		if kind == schema.KindWrite {
			if s := ActionStatus(st.Intent.ActionName()); s != "" {
				return s, true
			}
		}
		if st.Status != "" {
			return st.Status, true
		}
	case "to", "email", "recipient":
		if st.AE != "" {
			return st.AE, true
		}
	case "perm", "permission":
		return "crm.write", true
	case "amount":
		return st.Amount, true
	case "owner_id":
		if st.OwnerID != "" {
			return st.OwnerID, true
		}
	case "close_date":
		if st.CloseDate != "" {
			return st.CloseDate, true
		}
	case "subject":
		return "update: " + st.Intent.EntityName(), true
	case "body", "text":
		return "deal=" + st.DealID + " status=" + st.Status, true
	}
	return nil, false
}

func applySaves(st *State, data map[string]any, save map[string]string) {
	if len(save) == 0 {
		save = map[string]string{
			"id": "deal_id", "status": "status", "ae": "ae",
			"email": "ae", "amount": "amount", "owner_id": "owner_id",
			"contact_id": "contact_id",
		}
	}
	for from, to := range save {
		if data == nil {
			continue
		}
		if _, ok := data[from]; !ok {
			continue
		}
		setState(st, to, schema.StringField(data, from))
		if to == "amount" {
			st.Amount = schema.IntField(data, from)
		}
	}
}

func stateValue(st *State, path string) any {
	switch path {
	case "intent.company", "intent.entity", "company", "entity", "query", "name":
		return st.Intent.EntityName()
	case "contact_id":
		return st.ContactID
	case "deal_id", "id", "record_id", "ticket_id":
		return st.TargetID()
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
	case "deal_id", "id", "record_id", "ticket_id":
		st.DealID = val
		st.RecordID = val
	case "status":
		st.Status = val
	case "owner_id":
		st.OwnerID = val
	case "close_date":
		st.CloseDate = val
	}
}

// nextFrom picks the outgoing edge to follow. A pasted graph may declare
// several edges out of one node; without a condition to evaluate, taking the
// first non-abort edge silently drops the rest, so say so in the timeline
// rather than leaving a node the user declared permanently unreachable.
func nextFrom(g GraphSpec, from string, rec *trace.Recorder) string {
	var outs []string
	for _, e := range g.Edges {
		if e.From == from && e.To != "abort" {
			outs = append(outs, e.To)
		}
	}
	if len(outs) == 0 {
		return "end"
	}
	if len(outs) > 1 && rec != nil {
		rec.State("graph has an unconditional branch; took the first edge", map[string]any{
			"node": from, "took": outs[0], "skipped": outs[1:],
		})
	}
	return outs[0]
}

func looksLikeCRM(tools []schema.Tool) bool {
	return schema.LooksLikeCRM(tools)
}

// LooksLikeCRM reports whether the spec is the sample closer's tool set.
func LooksLikeCRM(tools []schema.Tool) bool {
	return schema.LooksLikeCRM(tools)
}

func graphFromTools(tools []schema.Tool) GraphSpec {
	nodes := []string{"plan"}
	edges := []Edge{}
	prev := "plan"
	for _, t := range tools {
		nodes = append(nodes, t.Name)
		edges = append(edges, Edge{From: prev, To: t.Name})
		prev = t.Name
	}
	edges = append(edges, Edge{From: prev, To: "end"})
	nodes = append(nodes, "end", "abort")
	return GraphSpec{Start: "plan", Nodes: nodes, Edges: edges}
}
