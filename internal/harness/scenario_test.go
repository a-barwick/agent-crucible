package harness

import (
	"context"
	"testing"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/ai"
	"github.com/a-barwick/agent-crucible/internal/fault"
	"github.com/a-barwick/agent-crucible/internal/judge"
	"github.com/a-barwick/agent-crucible/internal/runtime"
	"github.com/a-barwick/agent-crucible/internal/scenario"
	"github.com/a-barwick/agent-crucible/internal/schema"
	"github.com/a-barwick/agent-crucible/internal/world"
)

func TestLibraryScenariosClean(t *testing.T) {
	for _, id := range []string{"close-acme", "cancel-acme", "renew-supplies", "refund-acme", "close-quiet"} {
		s := Run(context.Background(), Config{Seed: 7, Trials: 4, P: 0, Scenario: id, Faults: fault.MVP})
		if s.Survival != 1 {
			t.Fatalf("%s survival %v counts=%v", id, s.Survival, s.Counts)
		}
		for _, tr := range s.Trials {
			if tr.Outcome != judge.OutcomeCompleted {
				t.Fatalf("%s trial %d: %s %s %v", id, tr.N, tr.Outcome, tr.Reason, tr.Violations)
			}
		}
	}
}

func TestPastedAgentClean(t *testing.T) {
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 3, P: 0, Agent: "pasted",
	})
	if s.Survival != 0 {
		t.Fatalf("pasted without spec should not complete, got %v", s.Survival)
	}
}

func TestPastedCRMSpecClean(t *testing.T) {
	spec := agent.NewCRM(nil).Spec()
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 4, P: 0, Agent: "pasted", Spec: &spec, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("pasted CRM spec survival %v counts=%v", s.Survival, s.Counts)
	}
}

func TestPastedCustomTools(t *testing.T) {
	writes, emails := 1, 0
	notify := false
	bundle := ticketBundle(writes, emails, notify)
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 4, P: 0, Agent: "pasted", Bundle: &bundle, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("custom ticket agent survival %v counts=%v critique=%s", s.Survival, s.Counts, s.Critique.Headline)
		for _, tr := range s.Trials {
			t.Logf("trial %d %s %s %v", tr.N, tr.Outcome, tr.Reason, tr.Violations)
		}
	}
}

func TestGeneratedTicketScenarioRuns(t *testing.T) {
	tools := []schema.Tool{
		{Name: "search_ticket", Required: []string{"query"}},
		{Name: "update_ticket", Required: []string{"id", "status"}},
	}
	drafts := ai.Generate(context.Background(), 3, tools, 3, nil)
	if len(drafts) == 0 {
		t.Fatal("no drafts")
	}
	d := drafts[0]
	spec := agent.Spec{
		Name:      "ticket-bot",
		Framework: "generic",
		Tools:     tools,
		Companies: []string{"Acme Corp", "Globex"},
		Objective: d.Objective,
	}
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 3, P: 0, Agent: "pasted",
		Bundle: &scenario.Bundle{Spec: spec, Scenario: d.Scenario},
		Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("generated draft survival %v counts=%v", s.Survival, s.Counts)
		for _, tr := range s.Trials {
			t.Logf("trial %d %s %s %v", tr.N, tr.Outcome, tr.Reason, tr.Violations)
		}
	}
}

func TestUnknownScenarioIDUsesExtra(t *testing.T) {
	tools := []schema.Tool{
		{Name: "search_ticket", Required: []string{"query"}},
		{Name: "update_ticket", Required: []string{"id", "status"}},
	}
	drafts := ai.Generate(context.Background(), 3, tools, 2, nil)
	d := drafts[0]
	spec := agent.Spec{Name: "ticket-bot", Tools: tools, Companies: []string{"Acme Corp", "Globex"}}
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 3, P: 0, Agent: "pasted",
		Scenario: d.ID,
		Extra:    []scenario.Scenario{d.Scenario},
		Bundle:   &scenario.Bundle{Spec: spec},
		Faults:   fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("extra scenario %s survival %v counts=%v critique=%s", d.ID, s.Survival, s.Counts, s.Critique.Headline)
	}
}

func TestLangGraphPastedTicket(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	writes, emails := 1, 0
	notify := false
	bundle := ticketBundle(writes, emails, notify)
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDCloserLangGraph,
		Bundle: &bundle, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("langgraph pasted ticket survival %v counts=%v", s.Survival, s.Counts)
		for _, tr := range s.Trials {
			t.Logf("trial %d %s %s %v", tr.N, tr.Outcome, tr.Reason, tr.Violations)
		}
	}
}

func TestADKPastedTicket(t *testing.T) {
	if !runtime.HaveLangGraph() {
		t.Skip("langgraph not installed")
	}
	writes, emails := 1, 0
	notify := false
	bundle := ticketBundle(writes, emails, notify)
	s := Run(context.Background(), Config{
		Seed: 3, Trials: 2, P: 0, Agent: agent.IDCloserADK,
		Bundle: &bundle, Faults: fault.MVP,
	})
	if s.Survival != 1 {
		t.Fatalf("adk pasted ticket survival %v counts=%v", s.Survival, s.Counts)
	}
}

func TestPastedCustomToolsMalformed(t *testing.T) {
	writes, emails := 1, 0
	notify := false
	bundle := ticketBundle(writes, emails, notify)
	s := Run(context.Background(), Config{
		Seed: 1, Trials: 12, P: 1, Agent: "pasted", Bundle: &bundle,
		Faults: []fault.Type{fault.Malformed},
	})
	if s.Survival > 0.25 {
		t.Fatalf("p=1 malformed custom write should collapse, got %v counts=%v", s.Survival, s.Counts)
	}
}

func ticketBundle(writes, emails int, notify bool) scenario.Bundle {
	return scenario.Bundle{
		Spec: agent.Spec{
			Name:      "ticket-bot",
			Framework: "generic",
			Tools: []schema.Tool{
				{Name: "search_ticket", Required: []string{"query"}, Returns: []schema.Field{{Name: "id", Required: true}, {Name: "status", Required: true}}},
				{Name: "update_ticket", Required: []string{"id", "status"}, Returns: []schema.Field{{Name: "id", Required: true}, {Name: "status", Required: true}}},
			},
			Companies: []string{"Acme Corp", "Globex"},
			Objective: "Resolve the Acme Corp ticket.",
		},
		Scenario: scenario.Scenario{
			ID:        "resolve-tkt",
			Objective: "Resolve the Acme Corp ticket.",
			Companies: []string{"Acme Corp", "Globex"},
			Expect: judge.Expect{
				RecordID:         "tkt-acme",
				Status:           "Resolved",
				Writes:           &writes,
				Emails:           &emails,
				Notify:           &notify,
				LookalikeDealIDs: []string{"tkt-other"},
			},
			Fixture: &world.Fixture{
				Records: []world.Record{
					{ID: "tkt-acme", Collection: "tickets", Fields: map[string]any{"company": "Acme Corp", "status": "Open"}},
					{ID: "tkt-other", Collection: "tickets", Fields: map[string]any{"company": "Globex", "status": "Open"}},
				},
			},
		},
	}
}
