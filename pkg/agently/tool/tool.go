package tool

import (
	"reflect"

	"github.com/viant/xdatly/types/core"
	"github.com/viant/xdatly/types/custom/dependency/checksum"
)

func init() {
	core.RegisterType("tool", "Feed", reflect.TypeOf(Feed{}), checksum.GeneratedTime)
	core.RegisterType("tool", "FeedSpec", reflect.TypeOf(FeedSpec{}), checksum.GeneratedTime)
}
