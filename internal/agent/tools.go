package agent

import "github.com/a-barwick/agent-crucible/internal/schema"

// CRMTools is the contract the sample LangGraph agent advertises.
func CRMTools() []schema.Tool {
	return []schema.Tool{
		{
			Name:        "lookup_contact",
			Description: "Find a CRM contact by exact company name.",
			Required:    []string{"company"},
			Returns: []schema.Field{
				{Name: "id", Type: "string", Required: true},
				{Name: "company", Type: "string", Required: true},
				{Name: "ae", Type: "string", Required: true},
			},
		},
		{
			Name:        "get_deal",
			Description: "Fetch the open deal for a contact.",
			Required:    []string{"contact_id"},
			Returns: []schema.Field{
				{Name: "id", Type: "string", Required: true},
				{Name: "status", Type: "string", Required: true},
				{Name: "amount", Type: "int", Required: true},
				{Name: "owner_id", Type: "string", Required: true},
				{Name: "close_date", Type: "string", Required: true},
			},
		},
		{
			Name:        "check_permission",
			Description: "Return whether the actor may perform a CRM permission.",
			Required:    []string{"perm"},
			Returns:     []schema.Field{{Name: "allowed", Type: "bool", Required: true}},
		},
		{
			Name:        "write_deal",
			Description: "Patch deal fields. Callers must send a complete Closed-Won record.",
			Required:    []string{"id", "status"},
			Returns: []schema.Field{
				{Name: "id", Type: "string", Required: true},
				{Name: "status", Type: "string", Required: true},
				{Name: "amount", Type: "int", Required: true},
				{Name: "owner_id", Type: "string", Required: true},
			},
		},
		{
			Name:        "send_email",
			Description: "Send mail to the account executive.",
			Required:    []string{"to", "subject"},
			Returns:     []schema.Field{{Name: "id", Type: "string", Required: true}},
		},
	}
}

func CRMGraphSpec() GraphSpec {
	nodes := []string{"plan", "lookup", "fetch", "enrich", "authorize", "write", "notify", "abort", "end"}
	return GraphSpec{
		Start: "plan",
		Nodes: nodes,
		Edges: []Edge{
			{From: "plan", To: "lookup"},
			{From: "lookup", To: "fetch"},
			{From: "lookup", To: "abort"},
			{From: "fetch", To: "enrich"},
			{From: "fetch", To: "abort"},
			{From: "enrich", To: "authorize"},
			{From: "authorize", To: "write"},
			{From: "authorize", To: "abort"},
			{From: "write", To: "notify"},
			{From: "write", To: "abort"},
			{From: "notify", To: "end"},
			{From: "notify", To: "abort"},
		},
	}
}

func CRMBugs() []Bug {
	return []Bug{
		{
			ID:    "transport-as-semantic",
			Node:  "write",
			Title: "Transport success is treated as a valid write",
			Why:   "write_deal is done when the envelope is not a timeout. Missing fields never gate the notify node.",
		},
		{
			ID:    "stale-memory-wins",
			Node:  "enrich",
			Title: "Checkpoint memory overwrites a fresh fetch",
			Why:   "enrich copies Memory over tool results whenever Memory is populated.",
		},
		{
			ID:    "skip-auth",
			Node:  "authorize",
			Title: "Permission check is skipped when memory says so",
			Why:   "HasWritePerm short-circuits check_permission.",
		},
		{
			ID:    "no-idempotency",
			Node:  "write",
			Title: "Side-effect nodes are not idempotent",
			Why:   "A duplicated write_deal or send_email is applied twice.",
		},
		{
			ID:    "no-replan",
			Node:  "plan",
			Title: "Objective is parsed once",
			Why:   "A mid-run objective change never returns to plan.",
		},
	}
}
