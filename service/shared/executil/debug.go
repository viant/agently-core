package executil

import (
	"context"
	"log"
	"os"

	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/internal/shared"
)

func debugf(ctx context.Context, format string, args ...interface{}) {
	if !shared.DebugAttachmentsEnabled() {
		return
	}
	// Use stdout so CLI/stdout captures include debug lines.
	// Do not rely on global log output configuration.
	log.New(os.Stdout, "", log.LstdFlags).Printf("[executil] "+format, args...)
}

func debugConvEnabled() bool {
	return logx.Enabled()
}

func debugConvf(format string, args ...interface{}) { infoConvf(format, args...) }

func infoConvf(format string, args ...interface{}) {
	if !debugConvEnabled() {
		return
	}
	logx.Infof("conversation", format, args...)
}

func warnConvf(format string, args ...interface{}) {
	if !debugConvEnabled() {
		return
	}
	logx.Warnf("conversation", format, args...)
}

func errorConvf(format string, args ...interface{}) {
	if !debugConvEnabled() {
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
