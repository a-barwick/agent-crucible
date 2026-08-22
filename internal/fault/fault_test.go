package fault

import (
	"testing"

	"github.com/a-barwick/agent-crucible/internal/rng"
	"github.com/a-barwick/agent-crucible/internal/schema"
)

func TestDecideConsumesIndependentlyOfP(t *testing.T) {
	seq := func(p float64) []float64 {
		in := New(rng.Stream(7, 0), p, MVP)
		var us []float64
		for i := 0; i < 8; i++ {
			d := in.Decide(SiteTool, "write_deal")
			us = append(us, d.U)
		}
		return us
	}
	a, b := seq(0), seq(0.3)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("u[%d] changed with p: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestDecideMonotonicFire(t *testing.T) {
	low := New(rng.Stream(3, 1), 0.1, MVP)
	high := New(rng.Stream(3, 1), 0.4, MVP)
	for i := 0; i < 20; i++ {
		d0 := low.Decide(SiteTool, "get_deal")
		d1 := high.Decide(SiteTool, "get_deal")
		if d0.Fired && !d1.Fired {
			t.Fatalf("draw %d fired at p=0.1 but not p=0.4 (u=%v)", i, d0.U)
		}
	}
}

func TestStripRequired(t *testing.T) {
	raw := schema.Result{OK: true, Data: map[string]any{
		"id": "deal-acme-1", "status": "Negotiation", "amount": 48000, "owner_id": "x",
	}}
	got := ApplyTool(Decision{Fired: true, Type: Malformed}, "get_deal", raw)
	if !got.Result.OK {
		t.Fatal("malformed should still look like success")
	}
	if _, ok := got.Result.Data["id"]; ok {
		t.Fatal("id should have been stripped")
	}
	if _, ok := got.Result.Data["amount"]; ok {
		t.Fatal("amount should have been stripped")
	}
}

func TestTimeoutSkipsWorld(t *testing.T) {
	got := ApplyTool(Decision{Fired: true, Type: Timeout}, "lookup_contact", schema.Result{OK: true})
	if !got.SkipWorld || got.Result.Error != "timeout" {
		t.Fatalf("%+v", got)
	}
}
