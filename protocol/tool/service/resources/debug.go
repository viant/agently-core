package resources

import "github.com/viant/agently-core/internal/logx"

func debugf(format string, args ...any) {
	logx.Debugf("resources", format, args...)
}
