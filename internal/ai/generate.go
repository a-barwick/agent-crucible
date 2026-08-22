package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/a-barwick/agent-crucible/internal/rng"
	"github.com/a-barwick/agent-crucible/internal/scenario"
	"github.com/a-barwick/agent-crucible/internal/schema"
)

// Draft is a generated scenario the UI can load.
type Draft struct {
	scenario.Scenario
	Source string `json:"source"`
}

func Generate(ctx context.Context, seed int64, tools []schema.Tool, n int, cli Client) []Draft {
	if n <= 0 {
		n = 5
	}
	if n > 12 {
		n = 12
	}
	local := generateLocal(seed, tools, n)
	if cli == nil {
		return local
	}
	raw, err := json.Marshal(map[string]any{"tools": tools, "n": n, "existing": namesOf(local)})
	if err != nil {
		return local
	}
	text, err := cli.Complete(ctx,
		"You generate agent-eval scenarios. Return JSON array of objects with id, name, description, objective. No markdown.",
		string(raw),
	)
	if err != nil {
		return local
	}
	var extras []scenario.Scenario
	if err := json.Unmarshal([]byte(extractJSONArray(text)), &extras); err != nil {
		return local
	}
	out := local
	for i, s := range extras {
		if s.ID == "" {
			s.ID = fmt.Sprintf("gen-%d", i)
		}
		if s.Objective == "" {
			continue
		}
		out = append(out, Draft{Scenario: s, Source: "model"})
	}
	if len(out) > n+len(local) {
		out = out[:n+len(local)]
	}
	return out
}

func generateLocal(seed int64, tools []schema.Tool, n int) []Draft {
	_ = rng.Stream(seed, 0)
	lib := scenario.Library()
	out := make([]Draft, 0, n)
	for i, s := range lib {
		if i >= n {
			break
		}
		out = append(out, Draft{Scenario: s, Source: "library"})
	}
	if hasWriteLike(tools) && len(out) < n {
		s := scenario.Get("close-quiet")
		s.ID = "gen-quiet-write"
		s.Name = "Generated: write without notify"
		s.Description = "Derived from tool schemas: a write-like tool exists, no mail required."
		out = append(out, Draft{Scenario: s, Source: "schema"})
	}
	return out
}

func hasWriteLike(tools []schema.Tool) bool {
	for _, t := range tools {
		n := strings.ToLower(t.Name)
		if strings.Contains(n, "write") || strings.Contains(n, "update") || strings.Contains(n, "patch") {
			return true
		}
	}
	return false
}

func namesOf(in []Draft) []string {
	out := make([]string, len(in))
	for i, d := range in {
		out[i] = d.ID
	}
	return out
}

func extractJSONArray(s string) string {
	s = strings.TrimSpace(s)
	i := strings.Index(s, "[")
	j := strings.LastIndex(s, "]")
	if i >= 0 && j > i {
		return s[i : j+1]
	}
	return s
}
