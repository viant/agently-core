package agui

import "strings"

// ToolStatusPending identifies native tool states that have not produced an
// execution result, even when a queue presentation payload is already stored.
func ToolStatusPending(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "pending", "waiting", "waiting_for_user", "blocked":
		return true
	}
	return false
}
