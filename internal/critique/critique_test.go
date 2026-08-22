package critique

import (
	"strings"
	"testing"

	"github.com/a-barwick/agent-crucible/internal/cluster"
	"github.com/a-barwick/agent-crucible/internal/fault"
)

func TestMalformedHeadline(t *testing.T) {
	c := Write(Input{
		Trials:   40,
		P:        0.3,
		Survival: 0.31,
		Clean:    0.87,
		ByFault: []cluster.Cluster{
			{Fault: fault.Malformed, N: 11, Rate: 0.18},
			{Fault: fault.Timeout, N: 8, Rate: 0.5},
		},
		ByShape: []cluster.Cluster{
			{ID: "malformed → incomplete_write", N: 9, Rate: 0},
		},
	})
	if !strings.Contains(c.Headline, "missing fields") {
		t.Fatalf("headline: %s", c.Headline)
	}
	if !strings.Contains(c.Headline, "validation") {
		t.Fatalf("headline should name the fix: %s", c.Headline)
	}
	if len(c.Fixes) == 0 || c.Fixes[0].Node != "write" {
		t.Fatalf("fixes %+v", c.Fixes)
	}
}

func TestColdCopy(t *testing.T) {
	c := Write(Input{Trials: 40, P: 0, Survival: 1, Clean: 1})
	if !strings.Contains(c.Headline, "100%") {
		t.Fatalf("%s", c.Headline)
	}
}
