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
		eff := fault.ApplyToolSpec(d, tool, schema.Result{}, b.tools())
		if d.Type == fault.Timeout && b.Clock != nil {
			b.Clock.Advance(50)
		}
		b.Rec.ToolResult(tool, eff.Result.OK, eff.Result.Error, eff.Result.Data)
		return eff.Result, nil
	}

	var dealSnap *world.Deal
	var recSnap *world.Record
	if schema.IsWriteLike(tool) {
		if id := writeID(args); id != "" {
			if d0, ok := b.World.Deal(id); ok {
				cp := d0
				dealSnap = &cp
			}
			if r0, ok := b.World.Record(id); ok {
				cp := r0.Clone()
				recSnap = &cp
			}
		}
	}
	raw := b.World.Invoke(tool, args)
	eff := fault.ApplyToolSpec(d, tool, raw, b.tools())
	if d.Fired && d.Type == fault.Malformed && schema.IsWriteLike(tool) && raw.OK {
		// Semantic lie: response says success, roll back the write so the
		// world does not match what the agent believes.
		if n := len(b.World.Writes); n > 0 {
			last := b.World.Writes[n-1]
			b.World.Writes = b.World.Writes[:n-1]
			if dealSnap != nil {
				b.World.Deals[last.DealID] = *dealSnap
			}
			if recSnap != nil && b.World.Records != nil {
				b.World.Records[recSnap.ID] = *recSnap
			}
		}
	}
	if eff.Duplicate {
		// Deliver the side effect twice. Reads just get two identical envelopes.
		_ = b.World.Invoke(tool, args)
		b.Rec.SideEffect("duplicate delivery", map[string]any{"tool": tool})
	}
	b.Rec.ToolResult(tool, eff.Result.OK, eff.Result.Error, eff.Result.Data)
	return eff.Result, nil
}

func (b *FaultBus) tools() []schema.Tool {
	if b.World == nil {
		return nil
	}
	return b.World.Tools
}

func writeID(args map[string]any) string {
	for _, k := range []string{"id", "record_id", "ticket_id", "deal_id"} {
		if s := schema.StringField(args, k); s != "" {
			return s
		}
	}
	return ""
}
