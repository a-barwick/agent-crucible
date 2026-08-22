package agent

import "strings"

// TruncateObjective is the partial-model fault: drop the notify clause
// (and anything after the first sentence). The current objective is the
// input — this is not hardcoded to an Acme close.
func TruncateObjective(obj string) string {
	obj = strings.TrimSpace(obj)
	if obj == "" {
		return obj
	}
	low := strings.ToLower(obj)
	for _, sep := range []string{" and email", " and notify", " and send"} {
		if i := strings.Index(low, sep); i > 0 {
			out := strings.TrimSpace(obj[:i])
			out = strings.TrimRight(out, ",;")
			if !strings.HasSuffix(out, ".") {
				out += "."
			}
			return out
		}
	}
	if i := strings.Index(obj, ". "); i > 0 {
		return strings.TrimSpace(obj[:i+1])
	}
	return obj
}
