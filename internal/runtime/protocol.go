package runtime

import "github.com/a-barwick/agent-crucible/internal/agent"

// RunRequest is what any sidecar (LangGraph, ADK, or yours) receives.
// Tools never run in the sidecar. They callback into the chamber.
type RunRequest struct {
	Runtime   string       `json:"runtime"`
	ThreadID  string       `json:"thread_id"`
	Objective string       `json:"objective"`
	Memory    agent.Memory `json:"memory"`
	Junk      string       `json:"junk"`
	Companies []string     `json:"companies"`
	Partial   bool         `json:"partial"`
	Spec      *agent.Spec  `json:"spec,omitempty"`
	Entry     string       `json:"entry,omitempty"`
	Export    string       `json:"export,omitempty"`
	Callback  string       `json:"callback"`
	Token     string       `json:"token"`
	Model     string       `json:"model,omitempty"`
}

type RunResponse struct {
	Terminal   string       `json:"terminal"`
	Intent     agent.Intent `json:"intent"`
	Claimed    agent.Claim  `json:"claimed"`
	Steps      int          `json:"steps"`
	Error      string       `json:"error,omitempty"`
	Checkpoint bool         `json:"checkpoint"`
	Runtime    string       `json:"runtime"`
}

type ToolReq struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

type BeforeReq struct {
	Name string `json:"name"`
}

type BeforeResp struct {
	Objective string       `json:"objective"`
	Partial   bool         `json:"partial"`
	Memory    agent.Memory `json:"memory"`
	Junk      string       `json:"junk"`
	Intent    agent.Intent `json:"intent"`
}

type StateReq struct {
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

type Status struct {
	Ready     bool   `json:"ready"`
	URL       string `json:"url,omitempty"`
	LangGraph bool   `json:"langgraph"`
	ADK       bool   `json:"adk"`
	JS        bool   `json:"js,omitempty"`
	Error     string `json:"error,omitempty"`
}
