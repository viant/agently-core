package scheduler

import (
	"context"
	"log"
	"strings"

	iauth "github.com/viant/agently-core/internal/auth"
	runtimediscovery "github.com/viant/agently-core/runtime/discovery"
)

const authLogPrefix = "[runtime-auth]"

func logAuthf(format string, args ...interface{}) {
	log.Printf(authLogPrefix+" "+format, args...)
}

func logAuthRunf(scheduleID, runID, userID string, format string, args ...interface{}) {
	args = append([]interface{}{strings.TrimSpace(scheduleID), strings.TrimSpace(runID), strings.TrimSpace(userID)}, args...)
	log.Printf(authLogPrefix+" schedule=%q run=%q user=%q "+format, args...)
}

func schedulerAuthMeta(ctx context.Context) (runtimediscovery.Mode, string) {
	mode, _ := runtimediscovery.ModeFromContext(ctx)
	return mode, strings.TrimSpace(iauth.EffectiveUserID(ctx))
}

func userCredRefKind(ref string) string {
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "":
		return ""
	case strings.HasPrefix(ref, "aws://"):
		return "aws"
	case strings.HasPrefix(ref, "file://"), strings.HasPrefix(ref, "/"):
		return "file"
	default:
		if idx := strings.Index(ref, "://"); idx > 0 {
			return ref[:idx]
		}
	}
	return "unknown"
}
