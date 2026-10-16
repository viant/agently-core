package runtime

import (
	"github.com/viant/agently-core/app/host/runtime/internal/testmetadata"
	windowloader "github.com/viant/agently-core/service/ui/window"
)

func testEmbeddedWindowSource(bindings []windowloader.ResourceBinding) (*embeddedWindowSource, error) {
	return newEmbeddedWindowSource(bindings, &testmetadata.FS)
}
