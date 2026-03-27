package elicitation

import (
	"sort"

	"github.com/viant/agently-core/internal/logx"
)

// DebugEnabled reports whether conversation debug logging is enabled.
// Enable with AGENTLY_DEBUG=1 (or true/yes/on). Also accepts AGENTLY_SCHEDULER_DEBUG.
// Legacy env (deprecated): AGENTLY_CONVERSATION_DEBUG, AGENTLY_DEBUG_ELICITATION.
func DebugEnabled() bool {
	return logx.Enabled()
}

// DebugConversationEnabled kept for backward compatibility with existing calls.
func DebugConversationEnabled() bool { return DebugEnabled() }

func debugf(format string, args ...any) { infof(format, args...) }

func infof(format string, args ...any) {
	if !DebugEnabled() {
		return
	}
	logx.Infof("conversation", format, args...)
}

func warnf(format string, args ...any) {
	if !DebugEnabled() {
		return
	}
	logx.Warnf("conversation", format, args...)
}

func errorf(format string, args ...any) {
	if !DebugEnabled() {
		return
	}
	logx.Errorf("conversation", format, args...)
}

func headString(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func tailString(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func PayloadKeys(payload map[string]interface{}) []string {
	if len(payload) == 0 {
		return nil
	}
	out := make([]string, 0, len(payload))
	for k := range payload {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
