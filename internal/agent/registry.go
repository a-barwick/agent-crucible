package agent

// Built-in agent ids the chamber can resolve.
const (
	IDCloser          = "aether-closer"
	IDCloserLangGraph = "aether-closer-langgraph"
	IDCloserADK       = "aether-closer-adk"
	IDPasted          = "pasted"
	IDTicketLangGraph = "ticket-langgraph"
	IDTicketADK       = "ticket-adk"
	IDRemote          = "remote"
)

// Info is what the UI lists without constructing a graph.
type Info struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Framework   string `json:"framework"`
	Runtime     string `json:"runtime"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
}

// Catalog is the drop-in list. runtimeReady means the Python sidecar is up.
func Catalog(runtimeReady bool) []Info {
	return []Info{
		{
			ID: IDCloser, Name: "aether-closer", Framework: "langgraph-go",
			Runtime: "in-process", Available: true,
			Description: "In-process twin: StateGraph-shaped nodes, MemorySaver, invoked planner. Fast slider.",
		},
		{
			ID: IDCloserLangGraph, Name: "aether-closer", Framework: "langgraph",
			Runtime: "python", Available: runtimeReady,
			Description: "Real LangGraph StateGraph compiled with InMemorySaver. Plan node invokes a chat model.",
		},
		{
			ID: IDCloserADK, Name: "aether-closer", Framework: "adk",
			Runtime: "python", Available: runtimeReady,
			Description: "ADK adapter: Agent + Runner + SessionService. Tools callback into the chamber.",
		},
		{
			ID: IDPasted, Name: "pasted", Framework: "generic",
			Runtime: "spec", Available: true,
			Description: "Paste tool schemas and an optional graph. Chamber-built walk if you have no file.",
		},
		{
			ID: IDTicketLangGraph, Name: "ticket-bot", Framework: "langgraph",
			Runtime: "python", Available: runtimeReady,
			Description: "A real user-written LangGraph (examples/ticket_graph.py). Tools callback into the chamber.",
		},
		{
			ID: IDTicketADK, Name: "ticket-bot", Framework: "adk",
			Runtime: "python", Available: runtimeReady,
			Description: "A real user-written ADK agent (examples/ticket_adk.py). Same ticket task, ADK loop.",
		},
		{
			ID: IDRemote, Name: "remote", Framework: "http",
			Runtime: "endpoint", Available: true,
			Description: "Any process that speaks POST /v1/run and callbacks for tools. Set spec.endpoint.",
		},
	}
}

func NeedsPython(id string, spec *Spec) bool {
	if spec != nil && spec.Endpoint != "" {
		return false
	}
	if spec != nil {
		if spec.Entry != "" {
			return true
		}
		switch spec.Runtime {
		case "langgraph", "adk":
			return true
		}
	}
	switch id {
	case IDCloserLangGraph, IDCloserADK, IDTicketLangGraph, IDTicketADK:
		return true
	}
	return false
}

// DropIn reports whether this id is a real agent file or process, not a chamber walk.
func DropIn(id string) bool {
	switch id {
	case IDTicketLangGraph, IDTicketADK, IDRemote:
		return true
	}
	return false
}
