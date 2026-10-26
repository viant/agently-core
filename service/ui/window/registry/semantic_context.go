package registry

import "github.com/viant/forge/backend/uicontext"

// CurrentSemanticContext adapts the already scoped/verified host snapshot to
// Forge's reusable UI observation contract. It supplies no authority itself.
func CurrentSemanticContext(win *WindowSnapshot) *uicontext.Context {
	if win == nil {
		return nil
	}
	return uicontext.Project(&uicontext.Input{SemanticContext: win.SemanticContext, DataSourceRefs: ListDataSourceRefs(win), Metadata: win.Metadata})
}
