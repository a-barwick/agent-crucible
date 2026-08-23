// Package fault is the deterministic injection layer.
//
// The runner, not a model, decides whether a fault fires. Each decision site
// draws from its own sub-stream keyed by (site, target, visit count), so the
// (u, kind) pair a site sees depends only on the seed and on how many times
// that site has been reached — never on how many draws happened elsewhere.
// Raising the failure probability on a fixed seed therefore only adds faults:
// a site that fired at p fires at every p' > p it is still reached at, and the
// ensemble does not reshuffle when a fault changes the agent's path. The one
// way a fault disappears is behind an earlier one — a run that now aborts at
// the first tool never reaches the second — which is a change in the path, not
// in the dice. TestDecisionsAreStableAsPRises holds both halves of that.
package fault

import (
	"math/rand"
	"sort"

	"github.com/a-barwick/agent-crucible/internal/rng"
	"github.com/a-barwick/agent-crucible/internal/schema"
)

// Type is a controlled failure the chamber can inject.
type Type string

const (
	Timeout         Type = "timeout"
	Malformed       Type = "malformed"
	Duplicate       Type = "duplicate"
	StaleMemory     Type = "stale_memory"
	Permission      Type = "permission"
	PartialModel    Type = "partial_model"
	ContextPressure Type = "context_pressure"
	CostCeiling     Type = "cost_ceiling"
	ObjectiveChange Type = "objective_change"
)

// All is the full catalog. The weekend MVP demo enables the first five.
var All = []Type{
	Timeout, Malformed, Duplicate, StaleMemory, Permission,
	PartialModel, ContextPressure, CostCeiling, ObjectiveChange,
}

// MVP is the five faults the demo slider turns up.
var MVP = []Type{Timeout, Malformed, Duplicate, StaleMemory, Permission}

// Site is where a fault is allowed to fire.
type Site string

const (
	SitePreflight Site = "preflight"
	SiteTool      Site = "tool"
	SiteNode      Site = "node"
)

func (t Type) Site() Site {
	switch t {
	case StaleMemory, ContextPressure, CostCeiling:
		return SitePreflight
	case Timeout, Malformed, Duplicate, Permission:
		return SiteTool
	case PartialModel, ObjectiveChange:
		return SiteNode
	default:
		return SiteTool
	}
}

func (t Type) Label() string {
	switch t {
	case Timeout:
		return "Tool timeout"
	case Malformed:
		return "Malformed tool result"
	case Duplicate:
		return "Duplicate event"
	case StaleMemory:
		return "Stale memory"
	case Permission:
		return "Missing permission"
	case PartialModel:
		return "Partial model response"
	case ContextPressure:
		return "Context-window pressure"
	case CostCeiling:
		return "Cost ceiling"
	case ObjectiveChange:
		return "Objective change"
	default:
		return string(t)
	}
}

func (t Type) Blurb() string {
	switch t {
	case Timeout:
		return "The tool never returns. A serious graph retries; this one mostly stalls."
	case Malformed:
		return "A 200-shaped payload with required fields stripped. Transport success, semantic failure."
	case Duplicate:
		return "The same side effect is delivered twice. No idempotency key, no dedup."
	case StaleMemory:
		return "Checkpoint memory overwrites a fresh fetch with last week's deal."
	case Permission:
		return "The write tool comes back 403. The write node treats any response as done."
	case PartialModel:
		return "The planner emits truncated intent — close the deal, forget the email."
	case ContextPressure:
		return "The state is flooded; lookup latches onto a lookalike company name."
	case CostCeiling:
		return "Budget trips halfway through. Later tools refuse to run."
	case ObjectiveChange:
		return "The user cancels the close after fetch. The graph does not re-plan."
	default:
		return ""
	}
}

// Decision is one injection at one site. Empty Type means the site was clean.
//
// Site, Target and Visit together identify the sub-stream the draw came from,
// which is what makes a suite auditable: the same three values on the same seed
// always yield the same U and Type, whatever else the run did.
type Decision struct {
	Type   Type    `json:"type,omitempty"`
	U      float64 `json:"u"`
	Fired  bool    `json:"fired"`
	Site   Site    `json:"site"`
	Target string  `json:"target,omitempty"`
	Visit  int     `json:"visit"`
}

// DefaultCostBudget is how many tool calls a trial gets once the cost
// ceiling is armed. Everything after this refuses to run.
const DefaultCostBudget = 3

// Injector is bound to one trial. It derives a sub-stream per decision site
// rather than consuming one shared stream, so p stays a pure gate.
type Injector struct {
	base    int64
	p       float64
	enabled map[Type]bool
	order   []Type
	visits  map[string]int
	log     []Decision

	costArmed  bool
	toolsSeen  int
	costBudget int
}

// New derives the injector's sub-stream seed from r. r is not read again, so
// the caller's stream stays untouched no matter how many sites are visited.
func New(r *rand.Rand, p float64, enabled []Type) *Injector {
	var base int64
	if r != nil {
		base = r.Int63()
	}
	return NewWithSeed(base, p, enabled)
}

func NewWithSeed(seed int64, p float64, enabled []Type) *Injector {
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	m := make(map[Type]bool, len(enabled))
	var order []Type
	seen := map[Type]bool{}
	for _, t := range enabled {
		if !seen[t] {
			seen[t] = true
			order = append(order, t)
			m[t] = true
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	return &Injector{
		base: seed, p: p, enabled: m, order: order,
		visits: map[string]int{}, costBudget: DefaultCostBudget,
	}
}

func (in *Injector) Enabled() []Type { return append([]Type(nil), in.order...) }

func (in *Injector) P() float64 { return in.p }

// SetCostBudget overrides how many tool calls survive an armed cost ceiling.
func (in *Injector) SetCostBudget(n int) {
	if n > 0 {
		in.costBudget = n
	}
}

// Decide draws (u, kind) from this site's own sub-stream even when nothing
// fires, so p only gates application and never shifts another site's draw.
func (in *Injector) Decide(site Site, target string, extra ...Type) Decision {
	applicable := in.applicable(site, extra...)
	if len(applicable) == 0 {
		return Decision{Site: site, Target: target}
	}
	key := string(site) + "|" + target
	n := in.visits[key]
	in.visits[key] = n + 1
	r := rng.Sub(in.base, key, n)
	u := r.Float64()
	kind := applicable[r.Intn(len(applicable))]
	d := Decision{Type: kind, U: u, Site: site, Target: target, Visit: n, Fired: u < in.p}
	if d.Fired && kind == CostCeiling {
		in.costArmed = true
	}
	in.log = append(in.log, d)
	return d
}

// Decisions is every draw this injector made, in order. Sites that had no
// applicable fault are not recorded because they never drew.
func (in *Injector) Decisions() []Decision {
	return append([]Decision(nil), in.log...)
}

func (in *Injector) applicable(site Site, extra ...Type) []Type {
	var out []Type
	for _, t := range in.order {
		if t.Site() != site {
			continue
		}
		if len(extra) > 0 && !contains(extra, t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func contains(ts []Type, t Type) bool {
	for _, x := range ts {
		if x == t {
			return true
		}
	}
	return false
}

// NoteToolCall tracks the cost-ceiling budget. Call it for every tool
// attempt, including ones that the injector itself fails.
func (in *Injector) NoteToolCall() {
	in.toolsSeen++
}

func (in *Injector) CostArmed() bool { return in.costArmed }

func (in *Injector) CostExceeded() bool {
	return in.costArmed && in.toolsSeen > in.costBudget
}

// ApplyTool mutates a successful result (or replaces the call) according to d.
// The world mutation is the caller's problem; this only shapes the envelope
// the agent sees, plus flags for duplicate / skip-world.
type ToolEffect struct {
	SkipWorld bool
	Duplicate bool
	Result    schema.Result
}

func ApplyTool(d Decision, tool string, raw schema.Result) ToolEffect {
	return ApplyToolSpec(d, tool, raw, nil)
}

// ApplyToolSpec is ApplyTool with the pasted schema so unknown tools
// still get 403s, hollow successes, and stripped required fields.
func ApplyToolSpec(d Decision, tool string, raw schema.Result, spec []schema.Tool) ToolEffect {
	if !d.Fired {
		return ToolEffect{Result: raw}
	}
	switch d.Type {
	case Timeout:
		return ToolEffect{SkipWorld: true, Result: schema.Result{OK: false, Error: "timeout"}}
	case Permission:
		if schema.IsPermissionLike(tool) {
			perm := "crm.write"
			if raw.Data != nil {
				if p := schema.StringField(raw.Data, "perm"); p != "" {
					perm = p
				}
			}
			return ToolEffect{SkipWorld: true, Result: schema.Result{OK: true, Data: map[string]any{
				"perm": perm, "allowed": false,
			}}}
		}
		// Every other tool — reads included — comes back 403. A permission
		// fault that silently returned the real payload would be recorded as
		// fired while changing nothing the agent could react to.
		return ToolEffect{SkipWorld: true, Result: schema.Result{OK: false, Error: "permission_denied"}}
	case Malformed:
		return ToolEffect{Result: stripRequired(tool, raw, spec)}
	case Duplicate:
		return ToolEffect{Duplicate: true, Result: raw}
	default:
		return ToolEffect{Result: raw}
	}
}

func stripRequired(tool string, raw schema.Result, spec []schema.Tool) schema.Result {
	if schema.IsWriteLike(tool) {
		// Transport success, no confirmation of what was written.
		return schema.Result{OK: true, Data: map[string]any{}}
	}
	if !raw.OK || raw.Data == nil {
		// A failed call that we still mark OK with an empty body — the
		// classic "CRM returned success with missing fields" lie.
		return schema.Result{OK: true, Data: map[string]any{}}
	}
	data := make(map[string]any, len(raw.Data))
	for k, v := range raw.Data {
		data[k] = v
	}
	if t, ok := schema.Find(spec, tool); ok {
		for _, f := range t.Returns {
			if f.Required {
				delete(data, f.Name)
			}
		}
		if _, ok := data["id"]; ok {
			delete(data, "id")
		}
		return schema.Result{OK: true, Data: data}
	}
	switch tool {
	case "lookup_contact":
		delete(data, "id")
		delete(data, "ae")
	case "get_deal":
		delete(data, "id")
		delete(data, "amount")
		delete(data, "owner_id")
		delete(data, "status")
	case "write_deal":
		return schema.Result{OK: true, Data: map[string]any{}}
	case "send_email":
		delete(data, "id")
		delete(data, "to")
	case "check_permission":
		delete(data, "allowed")
	default:
		delete(data, "id")
	}
	return schema.Result{OK: true, Data: data}
}

// Catalog is the UI/API description of every fault.
func Catalog() []Info {
	out := make([]Info, 0, len(All))
	for _, t := range All {
		mvp := false
		for _, m := range MVP {
			if m == t {
				mvp = true
				break
			}
		}
		out = append(out, Info{
			Type: t, Label: t.Label(), Blurb: t.Blurb(),
			Site: t.Site(), MVP: mvp,
		})
	}
	return out
}

type Info struct {
	Type  Type   `json:"type"`
	Label string `json:"label"`
	Blurb string `json:"blurb"`
	Site  Site   `json:"site"`
	MVP   bool   `json:"mvp"`
}
