// Package runtime composes operator-owned resource providers, authorization,
// immutable source snapshots and native window delivery for a workspace host.
package runtime

import (
	"context"
	"embed"
	"github.com/viant/agently-core/app/executor"
	forgeservice "github.com/viant/agently-core/service/primitiveprovider"
	forgetypes "github.com/viant/forge/backend/types"
)

// Options supplies trusted host configuration and application-owned artifacts.
// LinkedMetadata is the framework bundle linked by the embedding application.
type Options struct {
	LinkedMetadata                   *embed.FS
	NativeWindowRuntimeBinder        func(*executor.Runtime) error
	InternalResourceProviderIdentity string
	NativeWindowConfigPath           string
	WindowTargetProof                forgetypes.WindowTargetProof
	WindowLegacyTargetSupport        forgeservice.WindowLegacyTargetSupport
	ConfigureBuilder                 func(context.Context, *executor.Builder) error
	AuthorizationProviderFactory     AuthorizationProviderFactory
	ReportResourceProviderFactory    ReportResourceProviderFactory
	ResourceSelectionProviderFactory ResourceSelectionProviderFactory
	AuthzConfigPath                  string
	WorkspacePath                    string
}

// BindNativeWindowRuntime binds delivery to the actual constructed runtime.
func BindNativeWindowRuntime(options Options, runtime *executor.Runtime) error {
	if options.NativeWindowRuntimeBinder == nil {
		return nil
	}
	return options.NativeWindowRuntimeBinder(runtime)
}
