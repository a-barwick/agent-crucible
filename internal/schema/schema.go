// Package schema describes tool contracts the agent claims to speak.
package schema

// Tool is a JSON-schema-ish contract. The runner uses it to know which
// fields are required so it can strip them for malformed-result faults.
type Tool struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Required    []string `json:"required"`
	Returns     []Field  `json:"returns"`
}

// Field is one value in a tool result.
type Field struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

// Result is the envelope every tool returns. Agents that only inspect
// Error / HTTP-ish success treat a hollow payload as transport success.
type Result struct {
	OK    bool           `json:"ok"`
	Error string         `json:"error,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
}

func (r Result) IsTimeout() bool { return r.Error == "timeout" }

func (r Result) IsTransportError() bool {
	switch r.Error {
	case "timeout", "cost_ceiling", "unavailable":
		return true
	default:
		return false
	}
}

// StringField reads a string, returning "" when the key is missing or mistyped.
// Sample agents use this instead of validating required fields.
func StringField(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	v, _ := data[key].(string)
	return v
}

func IntField(data map[string]any, key string) int {
	if data == nil {
		return 0
	}
	switch n := data[key].(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func BoolField(data map[string]any, key string) bool {
	if data == nil {
		return false
	}
	v, _ := data[key].(bool)
	return v
}
