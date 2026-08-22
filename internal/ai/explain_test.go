package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/a-barwick/agent-crucible/internal/cluster"
	"github.com/a-barwick/agent-crucible/internal/fault"
)

func TestExplainUsesTraceEvidence(t *testing.T) {
	c := Explain(context.Background(), ExplainInput{
		Trials:   10,
		P:        0.3,
		Survival: 0.2,
		Clean:    1,
		ByFault: []cluster.Cluster{
			{Fault: fault.Malformed, N: 8, Rate: 0.1},
		},
		Samples: []Evidence{
			{N: 1, Faults: []fault.Type{fault.Malformed}, Events: []string{"write accepted empty success payload"}},
			{N: 2, Faults: []fault.Type{fault.Malformed}, Events: []string{"write accepted empty success payload"}},
		},
	})
	joined := strings.Join(c.Paragraphs, " ")
	if !strings.Contains(joined, "accepted empty") {
		t.Fatalf("expected evidence in paragraphs: %+v", c)
	}
	if len(c.Fixes) == 0 || c.Fixes[0].Node != "write" {
		t.Fatalf("fixes %+v", c.Fixes)
	}
}

func TestExplainMalformedHeadline(t *testing.T) {
	c := Explain(context.Background(), ExplainInput{
		Trials: 40, P: 0.3, Survival: 0.31, Clean: 0.87,
		ByFault: []cluster.Cluster{
			{Fault: fault.Malformed, N: 11, Rate: 0.18},
			{Fault: fault.Timeout, N: 8, Rate: 0.5},
		},
	})
	if !strings.Contains(c.Headline, "missing fields") {
		t.Fatalf("headline: %s", c.Headline)
	}
}
