package ai

import (
	"context"
	"reflect"
	"testing"

	"github.com/a-barwick/agent-crucible/internal/scenario"
	"github.com/a-barwick/agent-crucible/internal/schema"
)

func ticketTools() []schema.Tool {
	return []schema.Tool{
		{Name: "search_ticket", Required: []string{"query"}},
		{Name: "update_ticket", Required: []string{"id", "status"}},
	}
}

func TestGenerateCustomToolsCarriesExpect(t *testing.T) {
	drafts := Generate(context.Background(), 3, ticketTools(), 4, nil)
	if len(drafts) == 0 {
		t.Fatal("expected schema drafts")
	}
	for _, d := range drafts {
		if d.Source == "library" || d.ID == "close-acme" {
			t.Fatalf("custom tools should not dump the Acme library: %+v", d)
		}
		if !d.Expect.Specified() {
			t.Fatalf("draft %s missing expect", d.ID)
		}
		if d.Fixture == nil || len(d.Fixture.Records) == 0 {
			t.Fatalf("draft %s missing fixtures", d.ID)
		}
		if d.Expect.RecordID == "" || d.Expect.Status == "" {
			t.Fatalf("draft %s expect incomplete: %+v", d.ID, d.Expect)
		}
	}
}

func TestGenerateCRMStillLibrary(t *testing.T) {
	drafts := Generate(context.Background(), 3, []schema.Tool{{Name: "write_deal"}}, 3, nil)
	if len(drafts) == 0 || drafts[0].Source != "library" {
		t.Fatalf("CRM tools should reprint the library: %+v", drafts)
	}
}

// The seed was drawn and discarded, so every seed returned the same first n
// entries of the library. It has to select, and it has to replay.
func TestGenerateSeedSelectsAndReplays(t *testing.T) {
	ids := func(seed int64) []string {
		var out []string
		for _, d := range Generate(context.Background(), seed, nil, 3, nil) {
			out = append(out, d.ID)
		}
		return out
	}
	if a, b := ids(7), ids(7); !reflect.DeepEqual(a, b) {
		t.Fatalf("same seed gave different scenarios: %v vs %v", a, b)
	}
	differs := false
	for seed := int64(0); seed < 12 && !differs; seed++ {
		if !reflect.DeepEqual(ids(7), ids(seed)) {
			differs = true
		}
	}
	if !differs {
		t.Fatal("no seed in 0..11 produced a different selection; the seed is being ignored")
	}
}

func TestHydrateFillsMissingExpect(t *testing.T) {
	s := hydrate(scenario.Scenario{ID: "gen-x", Objective: "Resolve the Acme Corp ticket."}, ticketTools())
	if !s.Expect.Specified() || s.Fixture == nil || len(s.Fixture.Records) == 0 {
		t.Fatalf("%+v", s)
	}
	if s.Expect.RecordID != "tkt-acme" || s.Expect.Status != "Resolved" {
		t.Fatalf("hydrated expect %+v", s.Expect)
	}
}
