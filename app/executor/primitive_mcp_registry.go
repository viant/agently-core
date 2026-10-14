package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/service/resource/provider"
	"github.com/viant/agently-core/workspace"
	wscfg "github.com/viant/agently-core/workspace/config"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

const configurationMCPName = "configuration"

// MCP configuration is operator-authored metadata, not a user's window or data
// permission. A configuration-only local MCP makes startup possible without
// fabricating a user or bypassing the authenticated mixed primitive provider.
type mcpDefinitionRuntime struct {
	provider *resources.LocalProvider
	gateway  *resources.Gateway
	bound    bool
}

func (b *Builder) bindMCPDefinitions(out *Runtime) error {
	if out == nil || out.mcpDefinitions == nil || out.mcpDefinitions.bound {
		return nil
	}
	root := workspace.Root()
	if out.Store != nil {
		root = out.Store.Root()
	}
	config, err := wscfg.Load(root)
	if err != nil {
		return err
	}
	catalogs, err := config.MCPCatalogs()
	if err != nil {
		return err
	}
	reader, err := resources.NewMCPReferenceReader(out.mcpDefinitions.gateway, resources.MCPDefinitionSource{
		ConnectionName: configurationMCPName, ProviderIdentity: configurationMCPName, Namespace: "workspace",
	}, catalogs)
	if err != nil {
		return err
	}
	_, err = out.MCPManager.BindDefinitionReader(reader)
	if err != nil {
		return err
	}
	out.mcpDefinitions.bound = true
	return nil
}

func (b *Builder) configureMCPDefinitionSource(out *Runtime) error {
	if out == nil || out.MCPManager == nil || out.mcpDefinitions != nil {
		return nil
	}
	root := workspace.Root()
	if out.Store != nil {
		root = out.Store.Root()
	}
	var definitions []resources.NativeSnapshotDefinition
	if _, err := os.Stat(filepath.Join(root, workspace.KindMCP)); err == nil {
		definitions, err = resources.SnapshotExtensionDefinitions(context.Background(), root, []resources.ExtensionDirectory{resources.MCPDirectory("workspace", workspace.KindMCP)})
		if err != nil {
			return fmt.Errorf("capture MCP definitions: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	actor, verify := resources.PublicReadActors()
	authorize := func(ctx context.Context, a identity.VerifiedActor, uri identity.ResourceURI, action string) error {
		if uri.Kind != "mcp" && uri.Kind != "resource" {
			return identity.ErrResourceDenied
		}
		return publicDefinitionRead(ctx, a, uri, action)
	}
	var bindings []resources.LocalResourceBinding
	if len(definitions) > 0 {
		snapshot, err := resources.NewNativeAssetSnapshot(context.Background(), root, definitions)
		if err != nil {
			return err
		}
		bindings, err = snapshot.Bindings(configurationMCPName, func(ctx context.Context, a identity.VerifiedActor, uri identity.ResourceURI, action string) (identity.ResourceRevisionPolicy, error) {
			if err := authorize(ctx, a, uri, action); err != nil {
				return nil, err
			}
			return publicDefinitionRevision{actor: a, uri: uri.String(), read: authorize}, nil
		})
		if err != nil {
			return err
		}
	}
	p, err := resources.NewLocalProvider(resources.LocalConfig{
		ProviderIdentity: configurationMCPName, Actor: actor, Verify: verify, Authorize: authorize,
		Bindings: bindings, Validators: map[string]resources.LocalResourceValidator{"mcp": resources.ValidateMCPDefinition},
		Catalog: provider.EmptyCatalog{{Namespace: "workspace", Kinds: []primitive.KindSupport{{Kind: "mcp", FormatVersions: []int64{1}, Operations: []string{"list", "get"}}}}},
	})
	if err != nil {
		return err
	}
	documents, err := resources.MCPResourceDocuments(p)
	if err != nil {
		return err
	}
	if err = p.ConfigureNativeResources(documents); err != nil {
		return err
	}
	if err = out.MCPManager.RegisterLocal(context.Background(), configurationMCPName, &mcpcfg.MCPClient{
		ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: configurationMCPName,
		ResourceCacheScopes: map[string]string{"workspace": mcpcfg.ResourceCachePublic},
	}, func(context.Context) (mcpclient.Interface, error) { return resources.NewLocalMCPClient(p) }); err != nil {
		return err
	}
	out.mcpDefinitions = &mcpDefinitionRuntime{provider: p, gateway: resources.NewGateway(out.MCPManager, actor, verify, b.primitiveGatewayIdentity)}
	return nil
}
