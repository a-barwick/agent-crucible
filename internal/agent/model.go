package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Model is the planner the graph actually invokes. Scripted by default so the
// runner stays deterministic; swap in HTTPModel for a live LLM.
type Model interface {
	Complete(ctx context.Context, req ModelReq) (ModelResp, error)
}

type ModelReq struct {
	Objective string
	Companies []string
	Partial   bool
	History   []string
}

type ModelResp struct {
	Text     string `json:"text"`
	Provider string `json:"provider"`
}

// ScriptedModel is a real planner interface with a frozen policy.
// Partial omits notify — the graph must schema-check, and this one does not.
type ScriptedModel struct{}

func (ScriptedModel) Complete(_ context.Context, req ModelReq) (ModelResp, error) {
	in := ParseIntentWith(req.Objective, req.Companies)
	if req.Partial {
		in.Notify = false
	}
	b, err := json.Marshal(in)
	if err != nil {
		return ModelResp{}, err
	}
	return ModelResp{Text: string(b), Provider: "scripted"}, nil
}

// HTTPModel calls an OpenAI-compatible chat API. Optional; the chamber
// defaults to ScriptedModel so suites replay.
type HTTPModel struct {
	BaseURL string
	APIKey  string
	Model   string
	Client  *http.Client
}

func ModelFromEnv() Model {
	if strings.TrimSpace(os.Getenv("CRUCIBLE_AGENT_MODEL")) == "" &&
		strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		return ScriptedModel{}
	}
	if os.Getenv("CRUCIBLE_AGENT_MODEL") == "scripted" {
		return ScriptedModel{}
	}
	key := firstEnv("CRUCIBLE_AI_API_KEY", "OPENAI_API_KEY")
	if key == "" {
		return ScriptedModel{}
	}
	return HTTPModel{
		BaseURL: firstEnv("CRUCIBLE_AI_BASE_URL", "https://api.openai.com/v1"),
		APIKey:  key,
		Model:   firstEnv("CRUCIBLE_AI_MODEL", "gpt-4o-mini"),
	}
}

func (m HTTPModel) Complete(ctx context.Context, req ModelReq) (ModelResp, error) {
	body := map[string]any{
		"model": m.Model,
		"messages": []map[string]string{
			{"role": "system", "content": "Return only JSON {company, entity, deal_action, action, notify}. entity aliases company. action aliases deal_action (close_won, on_hold, refund, resolve, or none)."},
			{"role": "user", "content": req.Objective},
		},
		"temperature": 0,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return ModelResp{}, err
	}
	url := strings.TrimRight(m.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return ModelResp{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.APIKey)
	cli := m.Client
	if cli == nil {
		cli = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := cli.Do(httpReq)
	if err != nil {
		return ModelResp{}, err
	}
	defer res.Body.Close()
	slurp, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return ModelResp{}, fmt.Errorf("model http %d: %s", res.StatusCode, truncate(string(slurp), 200))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(slurp, &parsed); err != nil {
		return ModelResp{}, err
	}
	if len(parsed.Choices) == 0 {
		return ModelResp{}, fmt.Errorf("empty model response")
	}
	text := parsed.Choices[0].Message.Content
	if req.Partial {
		in := ParseModelIntent(text, req.Objective, req.Companies)
		in.Notify = false
		b, _ := json.Marshal(in)
		text = string(b)
	}
	return ModelResp{Text: text, Provider: "openai:" + m.Model}, nil
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
