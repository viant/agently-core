package workspace

import (
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
	windowloader "github.com/viant/agently-core/service/ui/window"
)

func validLayoutWindowKey(key string) bool {
	if strings.Contains(key, "://") {
		uri, err := identity.ParseResourceURI(key)
		return err == nil && uri.Kind == "window"
	}
	return key != "" && !strings.ContainsAny(key, "/\\?#") && !strings.HasPrefix(key, windowloader.RemotePrefix)
}
