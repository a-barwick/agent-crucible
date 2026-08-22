package runtime

import (
	"os"
	"path/filepath"
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

func TestResolveEntryRefusesFilesOutsideTheTree(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "evil.py")
	if err := os.WriteFile(outside, []byte("import os\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{outside, "/etc/passwd", "../../../../etc/passwd"} {
		if got, err := ResolveEntry(entry); err == nil {
			t.Fatalf("ResolveEntry(%q) should have refused, got %q", entry, got)
		}
		// FindEntry hands the raw string back so the sidecar fails to import
		// it, rather than resolving to some other agent file.
		if got := FindEntry(entry); got != entry {
			t.Fatalf("FindEntry(%q) = %q, want the input back", entry, got)
		}
	}
}

func TestResolveEntryRefusesControlCharacters(t *testing.T) {
	if _, err := ResolveEntry("examples/native_ticket.py\x00"); err == nil {
		t.Fatal("expected a refusal for a NUL in the path")
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
