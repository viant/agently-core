package agent

import (
	"os"
	"strconv"
	"strings"
	"time"

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

func stuckWarnDuration() time.Duration {
	v := strings.TrimSpace(os.Getenv("AGENTLY_CONVERSATION_STUCK_WARN_SEC"))
	if v == "" {
		return 10 * time.Minute
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
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
