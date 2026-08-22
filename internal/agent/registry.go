package agent

// Built-in agent ids the chamber can resolve.
const (
	IDCloser          = "aether-closer"
	IDCloserLangGraph = "aether-closer-langgraph"
	IDCloserADK       = "aether-closer-adk"
	IDPasted          = "pasted"
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
			Description: "You give it tool schemas and a graph. The chamber stays on this side of the tools.",
		},
	}
}

func NeedsPython(id string, spec *Spec) bool {
	if spec != nil {
		switch spec.Runtime {
		case "langgraph", "adk":
			return true
		}
	}
	return id == IDCloserLangGraph || id == IDCloserADK
}
