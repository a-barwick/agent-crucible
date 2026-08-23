package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/a-barwick/agent-crucible/internal/agent"
	"github.com/a-barwick/agent-crucible/internal/trace"
)

// Wrap runs a foreign process that does not speak POST /v1/run.
// python3 -m crucible_rt.boot <script> <req.json> installs HTTP/SDK
// intercept and execs the user file as __main__.
type Wrap struct {
	SpecIn *agent.Spec
}

func NewWrap(_ context.Context, spec *agent.Spec) (*Wrap, error) {
	if spec == nil || (spec.Command == "" && spec.Entry == "") {
		return nil, fmt.Errorf("wrap agent needs spec.command or spec.entry")
	}
	return &Wrap{SpecIn: spec}, nil
}

func (w *Wrap) Spec() agent.Spec {
	if w.SpecIn != nil {
		return *w.SpecIn
	}
	return agent.ForeignHTTPSpec()
}

func (w *Wrap) Run(ctx context.Context, st agent.State, bus agent.Bus, rec *trace.Recorder, hook agent.Hook) (agent.Result, error) {
	cb, err := Shared()
	if err != nil {
		return agent.Result{}, err
	}
	tok, err := newToken()
	if err != nil {
		return agent.Result{}, err
	}
	cb.Register(tok, &Session{Ctx: ctx, Bus: bus, Hook: hook, Rec: rec, St: &st})
	defer cb.Unregister(tok)

	dir := FindDir()
	if dir == "" {
		return agent.Result{}, fmt.Errorf("python runtime not found (set CRUCIBLE_RUNTIME)")
	}
	script, err := wrapScript(w.SpecIn)
	if err != nil {
		return agent.Result{}, err
	}

	tmp, err := os.MkdirTemp("", "crucible-wrap-*")
	if err != nil {
		return agent.Result{}, err
	}
	defer os.RemoveAll(tmp)
	reqPath := filepath.Join(tmp, "req.json")
	outPath := filepath.Join(tmp, "out.json")
	req := RunRequest{
		Runtime:   "wrap",
		ThreadID:  st.ThreadID,
		Objective: st.Objective,
		Memory:    st.Memory,
		Junk:      st.Junk,
		Companies: st.Companies,
		Partial:   st.Partial,
		Spec:      w.SpecIn,
		Entry:     script,
		Callback:  cb.URL(),
		Token:     tok,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return agent.Result{}, err
	}
	if err := os.WriteFile(reqPath, raw, 0o600); err != nil {
		return agent.Result{}, err
	}

	cmd := exec.CommandContext(ctx, "python3", "-m", "crucible_rt.boot", script, reqPath)
	cmd.Dir = filepath.Dir(dir)
	cmd.Env = append(os.Environ(),
		"PYTHONPATH="+dir,
		"CRUCIBLE_CALLBACK="+cb.URL(),
		"CRUCIBLE_TOKEN="+tok,
		"CRUCIBLE_REQ="+reqPath,
		"CRUCIBLE_RESULT="+outPath,
		"OBJECTIVE="+st.Objective,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return agent.Result{}, fmt.Errorf("wrap %v: %s", err, out)
	}

	body, err := os.ReadFile(outPath)
	if err != nil || len(body) == 0 {
		body = out
	}
	var parsed RunResponse
	if uerr := json.Unmarshal(body, &parsed); uerr != nil {
		// stdout may have boot chatter before the JSON object.
		if i := indexJSON(body); i >= 0 {
			uerr = json.Unmarshal(body[i:], &parsed)
		}
		if uerr != nil {
			return agent.Result{}, fmt.Errorf("wrap result: %v (%s)", uerr, body)
		}
	}
	if parsed.Error != "" && parsed.Claimed.Error == "" {
		parsed.Claimed.Error = parsed.Error
	}
	if parsed.Checkpoint {
		rec.State("wrap process finished", map[string]any{"runtime": "wrap"})
	}
	return agent.Result{
		Terminal: parsed.Terminal,
		Intent:   parsed.Intent,
		Claimed:  parsed.Claimed,
		Steps:    parsed.Steps,
	}, nil
}

// wrapScript picks the Python file out of spec.command. The command is not
// handed to a shell, and the file has to resolve inside the runtime's roots:
// spec.command arrives over HTTP, and this ends in python3 executing it.
func wrapScript(spec *agent.Spec) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("empty wrap spec")
	}
	cand := spec.Command
	if cand == "" {
		cand = spec.Entry
	}
	for _, f := range strings.Fields(cand) {
		if strings.HasSuffix(f, ".py") {
			if p, err := ResolveEntry(f); err == nil {
				return p, nil
			}
		}
	}
	if p, err := ResolveEntry(cand); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("wrap script not found under the working tree or examples/: %s", cand)
}

func indexJSON(b []byte) int {
	for i, c := range b {
		if c == '{' {
			return i
		}
	}
	return -1
}
