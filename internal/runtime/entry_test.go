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
