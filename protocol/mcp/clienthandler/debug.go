package clienthandler

import "github.com/viant/agently-core/internal/logx"

func debugf(format string, args ...any) {
	logx.Debugf("mcp-clienthandler", format, args...)
}
