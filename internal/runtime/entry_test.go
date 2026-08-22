package runtime

import (
	"strings"
	"testing"
)

func TestFindEntryTicketGraph(t *testing.T) {
	got := FindEntry("examples/ticket_graph.py")
	if got == "" || !strings.HasSuffix(got, "ticket_graph.py") {
		t.Fatalf("FindEntry: %q", got)
	}
	if !fileExists(got) {
		t.Fatalf("missing %s", got)
	}
}

func TestFindEntryNativeAgents(t *testing.T) {
	for _, name := range []string{
		"examples/native_ticket.py", "examples/native_adk.py", "examples/native_openai.py",
		"examples/native_ticket.mjs", "examples/native_react.py", "examples/http_closure.py",
		"examples/foreign_task.py",
	} {
		got := FindEntry(name)
		if got == "" || !fileExists(got) {
			t.Fatalf("FindEntry %s: %q", name, got)
		}
	}
}
