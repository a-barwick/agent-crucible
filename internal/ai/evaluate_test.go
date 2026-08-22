package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/judge"
)

type stubClient struct{ reply string }

func (s stubClient) Complete(ctx context.Context, system, user string) (string, error) {
	return s.reply, nil
}

func TestEvaluateLeavesSettledVerdictsAlone(t *testing.T) {
	in := judge.Verdict{Outcome: judge.OutcomeCompleted, Correct: true, Reason: "closed the deal"}
	got := Evaluate(context.Background(), in, agent.Result{}, nil, stubClient{`{"outcome":"failed"}`})
	if got.Outcome != in.Outcome || got.Correct != in.Correct || got.Reason != in.Reason {
		t.Fatalf("a settled verdict was rewritten: %+v", got)
	}
}

// TestEvaluateSaysWhenAModelDecided: this is the one verdict a seed does not
// reproduce, so it has to be visible. Two replays can disagree here — aborted is
// a safe stop, failed is not — and without a marker that reads as a broken
// harness rather than the documented exception.
func TestEvaluateSaysWhenAModelDecided(t *testing.T) {
	in := judge.Verdict{Ambiguous: true, Outcome: judge.OutcomeFailed, Reason: "claimed a write"}
	got := Evaluate(context.Background(), in, agent.Result{},
		nil, stubClient{`{"outcome":"aborted","reason":"stopped before writing"}`})
	if got.Outcome != judge.OutcomeAborted || !got.Correct {
		t.Fatalf("model verdict not applied: %+v", got)
	}
	if !strings.Contains(got.Reason, "model evaluator") {
		t.Fatalf("reason does not say a model settled it: %q", got.Reason)
	}

	// Without a client the same trial is settled locally and reproducibly, and
	// must not claim a model was involved.
	local := Evaluate(context.Background(), in, agent.Result{}, nil, nil)
	if local.Outcome != judge.OutcomeFailed || strings.Contains(local.Reason, "model evaluator") {
		t.Fatalf("local evaluation changed or misattributed: %+v", local)
	}
}

// A model may only choose between aborted and failed: anything else is ignored,
// so it can never talk a survival number upwards.
func TestEvaluateCannotPromoteToCompleted(t *testing.T) {
	in := judge.Verdict{Ambiguous: true, Outcome: judge.OutcomeFailed, Reason: "claimed a write"}
	for _, reply := range []string{
		`{"outcome":"completed","reason":"looks done"}`,
		`{"outcome":"recovered","reason":"recovered fine"}`,
		`not json at all`,
	} {
		got := Evaluate(context.Background(), in, agent.Result{}, nil, stubClient{reply})
		if got.Outcome != judge.OutcomeFailed || got.Correct {
			t.Fatalf("reply %q moved the verdict to %+v", reply, got)
		}
		if strings.Contains(got.Reason, "model evaluator") {
			t.Fatalf("reply %q was refused but credited to the model: %q", reply, got.Reason)
		}
	}
}
