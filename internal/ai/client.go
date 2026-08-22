// Package ai is the optional model in the loop: generate scenarios, score
// ambiguous traces, explain systemic patterns. It never picks faults.
package ai

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

type Config struct {
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"-"`
	Model   string `json:"model,omitempty"`
}

type Status struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	Ready    bool   `json:"ready"`
}

type Client interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

const (
	defaultBaseURL = "https://api.openai.com/v1"
	defaultModel   = "gpt-4o-mini"
)

func FromEnv(cfg Config) Config {
	if cfg.APIKey == "" {
		cfg.APIKey = first("CRUCIBLE_AI_API_KEY", "OPENAI_API_KEY")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = first("CRUCIBLE_AI_BASE_URL")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = first("CRUCIBLE_AI_MODEL")
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	return cfg
}

func ClientFromEnv(cfg Config) Client {
	cfg = FromEnv(cfg)
	if cfg.APIKey == "" {
		return nil
	}
	return &HTTP{Config: cfg}
}

func StatusFromEnv() Status {
	cfg := FromEnv(Config{})
	if cfg.APIKey == "" {
		return Status{Provider: "local", Ready: true}
	}
	// first() takes environment variable *names*; passing the fallback literal
	// as a second name meant looking up a variable called "gpt-4o-mini" and
	// reporting an empty model whenever CRUCIBLE_AI_MODEL was unset.
	return Status{Provider: "openai", Model: cfg.Model, Ready: true}
}

type HTTP struct {
	Config Config
	Doer   *http.Client
}

func (h *HTTP) Complete(ctx context.Context, system, user string) (string, error) {
	body := map[string]any{
		"model": h.Config.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	url := strings.TrimRight(h.Config.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.Config.APIKey)
	cli := h.Doer
	if cli == nil {
		cli = &http.Client{Timeout: 25 * time.Second}
	}
	res, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	slurp, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("ai http %d: %s", res.StatusCode, string(slurp))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(slurp, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("empty ai response")
	}
	return parsed.Choices[0].Message.Content, nil
}

func first(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}
