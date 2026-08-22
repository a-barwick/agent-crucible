package agent

import "github.com/a-barwick/agent-crucible/internal/schema"

const (
	TicketObjective    = "Resolve the Acme Corp ticket."
	TicketAltObjective = "Stop. Mark the Acme Corp ticket On-Hold and do not email anyone."
)

// TicketTools is the contract the drop-in ticket agents advertise.
func TicketTools() []schema.Tool {
	return []schema.Tool{
		{
			Name:        "search_ticket",
			Description: "Find a ticket by company or query.",
			Required:    []string{"query"},
			Returns: []schema.Field{
				{Name: "id", Type: "string", Required: true},
				{Name: "status", Type: "string", Required: true},
				{Name: "company", Type: "string", Required: true},
			},
		},
		{
			Name:        "update_ticket",
			Description: "Patch ticket fields. Callers must send a complete status write.",
			Required:    []string{"id", "status"},
			Returns: []schema.Field{
				{Name: "id", Type: "string", Required: true},
				{Name: "status", Type: "string", Required: true},
			},
		},
	}
}

func TicketGraphSpec() GraphSpec {
	return GraphSpec{
		Start: "plan",
		Nodes: []string{"plan", "search_ticket", "update_ticket", "end", "abort"},
		Edges: []Edge{
			{From: "plan", To: "search_ticket"},
			{From: "search_ticket", To: "update_ticket"},
			{From: "update_ticket", To: "end"},
		},
	}
}

func TicketCompanies() []string {
	return []string{"Acme Corp", "Globex"}
}

func ticketBase(runtime, entry, framework, desc string) Spec {
	return Spec{
		Name:        "ticket-bot",
		Framework:   framework,
		Runtime:     runtime,
		Description: desc,
		Entry:       entry,
		Tools:       TicketTools(),
		Graph:       TicketGraphSpec(),
		Companies:   TicketCompanies(),
		Objective:   TicketObjective,
		Bugs: []Bug{
			{
				ID:    "transport-as-semantic",
				Node:  "update_ticket",
				Title: "Transport success is treated as a valid write",
				Why:   "update_ticket is done when the envelope is not a timeout. Missing fields never gate the next node.",
			},
			{
				ID:    "stale-memory-wins",
				Node:  "search_ticket",
				Title: "Checkpoint memory overwrites a fresh search",
				Why:   "A populated memory.deal_id replaces the id the search just returned.",
			},
			{
				ID:    "no-replan",
				Node:  "plan",
				Title: "Objective is parsed once",
				Why:   "A mid-run objective change never returns to plan.",
			},
		},
	}
}

func TicketLangGraphSpec() Spec {
	return ticketBase(
		"langgraph",
		"examples/ticket_graph.py",
		"langgraph",
		"User-written LangGraph. Search then update. Tools callback into the chamber.",
	)
}

func TicketADKSpec() Spec {
	s := ticketBase(
		"adk",
		"examples/ticket_adk.py",
		"adk",
		"User-written ADK agent. Same ticket tools, ADK-shaped loop.",
	)
	s.Export = "run"
	return s
}
