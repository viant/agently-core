package resource

import (
	"context"
	workspacewindow "github.com/viant/agently-core/service/ui/window"
	"github.com/viant/forge/backend/types"
)

// NativeFrameworkWindowBinding is trusted startup producer input, never wire data.
type NativeFrameworkWindowBinding struct {
	Resource workspacewindow.ResourceBinding
	Load     func(context.Context, *types.WindowTarget) (*types.Window, error)
}
