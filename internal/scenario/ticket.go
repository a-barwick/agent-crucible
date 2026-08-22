package scenario

import (
	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/judge"
	"github.com/a-barwick/agent-crucible/internal/world"
)

const TicketID = "resolve-ticket"

// Ticket is the same task the drop-in LangGraph / ADK agents run.
// Expect is record-shaped; the judge does not fall back to Acme deal fields.
func Ticket() Scenario {
	writes, emails := 1, 0
	notify := false
	return Scenario{
		ID:           TicketID,
		Name:         "Resolve the Acme ticket",
		Description:  "Search then update. Writing Globex or claiming a hollow write is a failure.",
		Objective:    agent.TicketObjective,
		AltObjective: agent.TicketAltObjective,
		Companies:    agent.TicketCompanies(),
		Expect: judge.Expect{
			Objective:        agent.TicketObjective,
			RecordID:         "tkt-acme",
			Status:           "Resolved",
			Writes:           &writes,
			Emails:           &emails,
			Notify:           &notify,
			LookalikeDealIDs: []string{"tkt-other"},
		},
		Fixture:        TicketFixture(),
		ContextBallast: lookalikeBallast("Globex"),
		StaleMemory: agent.Memory{
			DealID: "tkt-other", DealStatus: "Open", Amount: 1,
			HasWritePerm: true,
		},
	}
}

func TicketFixture() *world.Fixture {
	return &world.Fixture{
		Records: []world.Record{
			{ID: "tkt-acme", Collection: "tickets", Fields: map[string]any{
				"company": "Acme Corp", "status": "Open", "ae": "jordan@vendor.example",
			}},
			{ID: "tkt-other", Collection: "tickets", Fields: map[string]any{
				"company": "Globex", "status": "Open", "ae": "pat@vendor.example",
			}},
		},
		Tools: agent.TicketTools(),
	}
}

func TicketBundle() Bundle {
	return Bundle{Spec: agent.TicketLangGraphSpec(), Scenario: Ticket()}
}

func lookalikeBallast(company string) string {
	return "Prior notes (stale): discussed " + company + " renewal, " + company + " Q3, " +
		company + " owner pat@vendor.example. Ignore? The live objective is still the current user turn, but this graph does not pin it."
}
