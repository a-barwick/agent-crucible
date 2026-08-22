// Package scenario is the task library. The chamber is not one Acme close.
package scenario

import (
	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/judge"
	"github.com/a-barwick/agent-crucible/internal/world"
)

const CloseAcmeID = "close-acme"

type Scenario struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Objective      string         `json:"objective"`
	AltObjective   string         `json:"alt_objective,omitempty"`
	Companies      []string       `json:"companies"`
	Expect         judge.Expect   `json:"expect"`
	ContextBallast string         `json:"context_ballast,omitempty"`
	StaleMemory    agent.Memory   `json:"stale_memory"`
	Fixture        *world.Fixture `json:"fixtures,omitempty"`
}

type Bundle struct {
	Spec     agent.Spec `json:"spec"`
	Scenario Scenario   `json:"scenario"`
}

type Info struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Objective   string `json:"objective"`
}

func (s Scenario) World() *world.World {
	if s.Fixture != nil {
		return world.SeedFixture(*s.Fixture)
	}
	return world.SeedCloseAcme()
}

func DefaultBallast() string {
	return "Prior notes (stale): discussed Acme Supplies renewal, Acme Supplies Q3, Acme Supplies owner pat@vendor.example. " +
		"Ignore? The live objective is still the current user turn, but this graph does not pin it."
}

func DefaultStale() agent.Memory {
	return agent.Memory{
		DealID: "deal-acme-1", DealStatus: "Qualified", Amount: 1,
		OwnerID: "", HasWritePerm: true,
	}
}

func Library() []Scenario {
	notifyFalse := false
	notifyTrue := true
	acme := []string{"Acme Corp", "Acme Supplies"}
	return []Scenario{
		{
			ID:             CloseAcmeID,
			Name:           "Close Acme and email the AE",
			Description:    "The weekend-MVP fixture. Production-shaped closer, one lookalike company.",
			Objective:      agent.DefaultObjective,
			AltObjective:   agent.AltObjective,
			Companies:      acme,
			Expect:         judge.DefaultExpect(),
			ContextBallast: DefaultBallast(),
			StaleMemory:    DefaultStale(),
		},
		{
			ID:           "cancel-acme",
			Name:         "Hold Acme, do not email",
			Description:  "User already cancelled. The graph must write On-Hold and stay quiet.",
			Objective:    agent.AltObjective,
			AltObjective: agent.AltObjective,
			Companies:    acme,
			Expect: judge.Expect{
				Objective:        agent.AltObjective,
				DealID:           judge.AcmeDealID,
				AE:               judge.AcmeAE,
				Amount:           judge.AcmeAmount,
				OwnerID:          judge.AcmeOwner,
				DealAction:       "on_hold",
				Notify:           &notifyFalse,
				LookalikeDealIDs: []string{"deal-supplies-1"},
			},
			ContextBallast: DefaultBallast(),
			StaleMemory:    DefaultStale(),
		},
		{
			ID:           "renew-supplies",
			Name:         "Close Acme Supplies",
			Description:  "Same tools, other company. Context ballast names Acme Corp last.",
			Objective:    "Update the Acme Supplies deal to Closed-Won and email the account executive.",
			AltObjective: "Stop. Mark the Acme Supplies deal On-Hold and do not email anyone.",
			Companies:    acme,
			Expect: judge.Expect{
				Objective:        "Update the Acme Supplies deal to Closed-Won and email the account executive.",
				DealID:           "deal-supplies-1",
				AE:               "pat@vendor.example",
				Amount:           1200,
				OwnerID:          "owner-pat",
				DealAction:       "close_won",
				Notify:           &notifyTrue,
				LookalikeDealIDs: []string{"deal-acme-1"},
			},
			ContextBallast: "Prior notes (stale): discussed Acme Corp renewal, Acme Corp Q3, Acme Corp owner jordan@vendor.example.",
			StaleMemory: agent.Memory{
				DealID: "deal-supplies-1", DealStatus: "Qualified", Amount: 1,
				OwnerID: "", HasWritePerm: true,
			},
		},
		{
			ID:           "refund-acme",
			Name:         "Refund Acme, no email",
			Description:  "Write Refunded. A closer that always emails will fail.",
			Objective:    "Refund the Acme Corp deal. Do not email anyone.",
			AltObjective: agent.AltObjective,
			Companies:    acme,
			Expect: judge.Expect{
				Objective:        "Refund the Acme Corp deal. Do not email anyone.",
				DealID:           judge.AcmeDealID,
				AE:               judge.AcmeAE,
				Amount:           judge.AcmeAmount,
				OwnerID:          judge.AcmeOwner,
				DealAction:       "refund",
				Notify:           &notifyFalse,
				LookalikeDealIDs: []string{"deal-supplies-1"},
			},
			ContextBallast: DefaultBallast(),
			StaleMemory:    DefaultStale(),
		},
		{
			ID:           "close-quiet",
			Name:         "Close Acme, do not email",
			Description:  "Closed-Won without a notify. Graphs that always walk notify fail.",
			Objective:    "Update the Acme Corp deal to Closed-Won. Do not email anyone.",
			AltObjective: agent.AltObjective,
			Companies:    acme,
			Expect: judge.Expect{
				Objective:        "Update the Acme Corp deal to Closed-Won. Do not email anyone.",
				DealID:           judge.AcmeDealID,
				AE:               judge.AcmeAE,
				Amount:           judge.AcmeAmount,
				OwnerID:          judge.AcmeOwner,
				DealAction:       "close_won",
				Notify:           &notifyFalse,
				LookalikeDealIDs: []string{"deal-supplies-1"},
			},
			ContextBallast: DefaultBallast(),
			StaleMemory:    DefaultStale(),
		},
		Ticket(),
	}
}

func Get(id string) Scenario {
	if s, ok := Lookup(id); ok {
		return s
	}
	return Library()[0]
}

// Lookup finds a built-in scenario. Unknown ids are not silently rewritten
// to close-acme — callers with generated extras must use those instead.
func Lookup(id string) (Scenario, bool) {
	if id == "" {
		return Scenario{}, false
	}
	for _, s := range Library() {
		if s.ID == id {
			return s, true
		}
	}
	return Scenario{}, false
}

func Summaries() []Info {
	lib := Library()
	out := make([]Info, len(lib))
	for i, s := range lib {
		out[i] = Info{ID: s.ID, Name: s.Name, Description: s.Description, Objective: s.Objective}
	}
	return out
}
