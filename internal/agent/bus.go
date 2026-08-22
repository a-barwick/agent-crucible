package agent

import (
	"context"

	"github.com/a-barwick/agent-crucible/internal/clock"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/schema"
	"github.com/a-barwick/agent-crucible/internal/trace"
	"github.com/a-barwick/agent-crucible/internal/world"
)

// FaultBus is the chamber's tool runtime: world + injector + recorder.
// The agent never talks to the world directly.
type FaultBus struct {
	World *world.World
	Inj   *fault.Injector
	Rec   *trace.Recorder
	Clock *clock.Clock
}

func (b *FaultBus) Call(ctx context.Context, tool string, args map[string]any) (schema.Result, error) {
	if err := ctx.Err(); err != nil {
		return schema.Result{}, err
	}
	if b.Clock != nil {
		b.Clock.Advance(1)
	}
	b.Inj.NoteToolCall()
	b.Rec.ToolCall(tool, args)

	if b.Inj.CostExceeded() {
		b.Rec.Fault(fault.CostCeiling, tool, "cost ceiling reached")
		res := schema.Result{OK: false, Error: "cost_ceiling"}
		b.Rec.ToolResult(tool, false, res.Error, nil)
		return res, nil
	}

	d := b.Inj.Decide(fault.SiteTool, tool)
	if d.Fired {
		b.Rec.Fault(d.Type, tool, d.Type.Label()+" on "+tool)
	}

	if d.Fired && (d.Type == fault.Timeout || d.Type == fault.Permission) {
		eff := fault.ApplyTool(d, tool, schema.Result{})
		if d.Type == fault.Timeout && b.Clock != nil {
			b.Clock.Advance(50)
		}
		b.Rec.ToolResult(tool, eff.Result.OK, eff.Result.Error, eff.Result.Data)
		return eff.Result, nil
	}

	var snapshot *world.Deal
	if tool == "write_deal" {
		if id := schema.StringField(args, "id"); id != "" {
			if d0, ok := b.World.Deal(id); ok {
				cp := d0
				snapshot = &cp
			}
		}
	}
	raw := b.dispatch(tool, args)
	eff := fault.ApplyTool(d, tool, raw)
	if d.Fired && d.Type == fault.Malformed && tool == "write_deal" && raw.OK {
		// Semantic lie: response says success, roll back the write so the
		// world does not match what the agent believes.
		if n := len(b.World.Writes); n > 0 {
			last := b.World.Writes[n-1]
			b.World.Writes = b.World.Writes[:n-1]
			if snapshot != nil {
				b.World.Deals[last.DealID] = *snapshot
			}
		}
	}
	if eff.Duplicate {
		// Deliver the side effect twice. Reads just get two identical envelopes.
		_ = b.dispatch(tool, args)
		b.Rec.SideEffect("duplicate delivery", map[string]any{"tool": tool})
	}
	b.Rec.ToolResult(tool, eff.Result.OK, eff.Result.Error, eff.Result.Data)
	return eff.Result, nil
}

func (b *FaultBus) dispatch(tool string, args map[string]any) schema.Result {
	switch tool {
	case "lookup_contact":
		return b.World.LookupContact(schema.StringField(args, "company"))
	case "get_deal":
		return b.World.GetDeal(schema.StringField(args, "contact_id"))
	case "write_deal":
		return b.World.WriteDeal(
			schema.StringField(args, "id"),
			schema.StringField(args, "status"),
			schema.IntField(args, "amount"),
			schema.StringField(args, "close_date"),
			schema.StringField(args, "owner_id"),
		)
	case "send_email":
		return b.World.SendEmail(
			schema.StringField(args, "to"),
			schema.StringField(args, "subject"),
			schema.StringField(args, "body"),
		)
	case "check_permission":
		return b.World.CheckPermission(schema.StringField(args, "perm"))
	default:
		return schema.Result{OK: false, Error: "unknown_tool"}
	}
}
