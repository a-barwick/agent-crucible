package agent

// Built-in agent ids the chamber can resolve.
const (
	IDCloser          = "aether-closer"
	IDCloserLangGraph = "aether-closer-langgraph"
	IDCloserADK       = "aether-closer-adk"
	IDPasted          = "pasted"
	IDTicketLangGraph = "ticket-langgraph"
	IDTicketADK       = "ticket-adk"
	IDNativeLangGraph = "native-langgraph"
	IDNativeADK       = "native-adk"
	IDNativeOpenAI    = "native-openai"
	IDNativeJS        = "native-js"
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

// Catalog is the drop-in list. pythonReady means the Python sidecar is up.
func Catalog(pythonReady, nodeReady bool) []Info {
	return []Info{
		{
			ID: IDCloser, Name: "aether-closer", Framework: "langgraph-go",
			Runtime: "in-process", Available: true,
			Description: "In-process twin: StateGraph-shaped nodes, MemorySaver, invoked planner. Fast slider.",
		},
		{
			ID: IDCloserLangGraph, Name: "aether-closer", Framework: "langgraph",
			Runtime: "python", Available: pythonReady,
			Description: "Real LangGraph StateGraph compiled with InMemorySaver. Plan node invokes a chat model.",
		},
		{
			ID: IDCloserADK, Name: "aether-closer", Framework: "adk",
			Runtime: "python", Available: pythonReady,
			Description: "ADK adapter: Agent + Runner + SessionService. Tools callback into the chamber.",
		},
		{
			ID: IDPasted, Name: "pasted", Framework: "generic",
			Runtime: "spec", Available: true,
			Description: "Paste tool schemas and an optional graph. Chamber-built walk if you have no file.",
		},
		{
			ID: IDTicketLangGraph, Name: "ticket-bot", Framework: "langgraph",
			Runtime: "python", Available: pythonReady,
			Description: "Unmodified LangGraph: @tool functions the chamber intercepts. No cb.retry_tool.",
		},
		{
			ID: IDTicketADK, Name: "ticket-bot", Framework: "adk",
			Runtime: "python", Available: pythonReady,
			Description: "Unmodified ADK agent: FunctionTool + LlmAgent. Chamber wraps the functions.",
		},
		{
			ID: IDNativeLangGraph, Name: "ticket-bot", Framework: "langgraph",
			Runtime: "python", Available: pythonReady,
			Description: "Same unmodified LangGraph as ticket-langgraph (examples/native_ticket.py).",
		},
		{
			ID: IDNativeADK, Name: "ticket-bot", Framework: "adk",
			Runtime: "python", Available: pythonReady,
			Description: "Same unmodified ADK agent as ticket-adk (examples/native_adk.py).",
		},
		{
			ID: IDNativeOpenAI, Name: "ticket-bot", Framework: "openai",
			Runtime: "python", Available: pythonReady,
			Description: "OpenAI tools loop: chat.completions schemas + DISPATCH. Chamber wraps the callables.",
		},
		{
			ID: IDNativeJS, Name: "ticket-bot", Framework: "javascript",
			Runtime: "node", Available: nodeReady,
			Description: "Unmodified Node agent (examples/native_ticket.mjs). Tools are plain functions.",
		},
		{
			ID: IDRemote, Name: "remote", Framework: "http",
			Runtime: "endpoint", Available: true,
			Description: "Any process that speaks POST /v1/run. The process may wrap tools for an unmodified file.",
		},
	}
}

func NeedsPython(id string, spec *Spec) bool {
	if spec != nil && spec.Endpoint != "" {
		return false
	}
	if spec != nil && (JSRuntime(spec.Runtime) || JSEntry(spec.Entry)) {
		return false
	}
	if id == IDNativeJS {
		return false
	}
	if spec != nil {
		if spec.Entry != "" {
			return true
		}
		switch spec.Runtime {
		case "langgraph", "adk", "openai":
			return true
		}
	}
	switch id {
	case IDCloserLangGraph, IDCloserADK, IDTicketLangGraph, IDTicketADK,
		IDNativeLangGraph, IDNativeADK, IDNativeOpenAI:
		return true
	}
	return false
}

func NeedsNode(id string, spec *Spec) bool {
	if spec != nil && spec.Endpoint != "" {
		return false
	}
	if id == IDNativeJS {
		return true
	}
	if spec != nil && (JSRuntime(spec.Runtime) || JSEntry(spec.Entry)) {
		return true
	}
	return false
}

// DropIn reports whether this id is a real agent file or process, not a chamber walk.
func DropIn(id string) bool {
	switch id {
	case IDTicketLangGraph, IDTicketADK, IDRemote,
		IDNativeLangGraph, IDNativeADK, IDNativeOpenAI, IDNativeJS:
		return true
	}
	return false
}

// IsDropIn is DropIn plus pasted entry/endpoint files that are not the CRM closer.
func IsDropIn(id string, spec *Spec) bool {
	if DropIn(id) {
		return true
	}
	if spec != nil && (spec.Entry != "" || spec.Endpoint != "") {
		return !LooksLikeCRM(spec.Tools)
	}
	return false
}
