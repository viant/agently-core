package streaming

import "strings"

// IsInternalMessageMode identifies known internal assistant lanes. A JSON body
// is not evidence of an internal lane; unmarked/task messages remain visible.
func IsInternalMessageMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "router", "chain":
		return true
	default:
		return false
	}
}
