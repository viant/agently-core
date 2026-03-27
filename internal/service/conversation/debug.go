package conversation

import (
	"github.com/viant/agently-core/internal/logx"
)

// DebugEnabled reports whether conversation debug logging is enabled.
// Enable with AGENTLY_DEBUG=1 (or true/yes/on). Also accepts AGENTLY_SCHEDULER_DEBUG.
// Legacy env (deprecated): AGENTLY_CONVERSATION_DEBUG.
func DebugEnabled() bool {
	return logx.Enabled()
}

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
