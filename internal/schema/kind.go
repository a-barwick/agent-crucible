package schema

import "strings"

// Kind is how the chamber treats a tool when the world has no CRM switch.
type Kind string

const (
	KindRead       Kind = "read"
	KindWrite      Kind = "write"
	KindEmail      Kind = "email"
	KindPermission Kind = "permission"
)

// Classify guesses a tool's kind from its name so pasted schemas can
// dispatch, inject, and judge without a hardcoded CRM switch.
func Classify(name string) Kind {
	n := strings.ToLower(name)
	switch {
	case containsAny(n, "email", "notify", "mail", "send_message"):
		return KindEmail
	case containsAny(n, "permission", "authorize", "acl"):
		return KindPermission
	case containsAny(n, "write", "update", "patch", "create", "delete", "refund", "upsert", "set_"):
		return KindWrite
	default:
		return KindRead
	}
}

func IsWriteLike(name string) bool      { return Classify(name) == KindWrite }
func IsEmailLike(name string) bool      { return Classify(name) == KindEmail }
func IsPermissionLike(name string) bool { return Classify(name) == KindPermission }

// LooksLikeCRM is true when the spec still speaks the sample closer's tool names.
func LooksLikeCRM(tools []Tool) bool {
	for _, t := range tools {
		switch t.Name {
		case "lookup_contact", "get_deal", "write_deal", "send_email", "check_permission":
			return true
		}
	}
	return false
}

// Find returns the named tool from a spec.
func Find(tools []Tool, name string) (Tool, bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
