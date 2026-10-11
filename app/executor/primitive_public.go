package executor

import (
	"context"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	auth "github.com/viant/agently-core/service/auth"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
	"os"
	"path/filepath"
	"time"
)

// Default public composition applies only to hosts without an
// explicit authority/provider. Local authored definitions grant no data access.
func (b *Builder) configurePublicPrimitiveProviders(out *Runtime) error {
	if out == nil || out.MCPManager == nil || out.PrimitiveProviders != nil || out.LocalPrimitiveProvider != nil || out.ComponentAuthority != nil || b.primitiveActor != nil || b.primitiveVerifier != nil || b.pinnedDatasourceRevalidator != nil || b.pinnedDatasourceResolver != nil || b.pinnedDatasourceAuthorizer != nil {
		return nil
	}
	actor, verify := resources.PublicReadActors()
	if out.AuthConfig != nil && out.AuthConfig.Enabled {
		publicActor, publicVerify := actor, verify
		actor = func(ctx context.Context) (identity.VerifiedActor, error) {
			if auth.EffectiveUserID(ctx) == "" {
				return identity.VerifiedActor{}, identity.ErrResourceDenied
			}
			return publicActor(ctx)
		}
		verify = func(ctx context.Context, held identity.VerifiedActor) error {
			if auth.EffectiveUserID(ctx) == "" {
				return identity.ErrResourceDenied
			}
			return publicVerify(ctx, held)
		}
	}

	root := workspace.Root()
	if out.Store != nil {
		root = out.Store.Root()
	}
	declared := []resources.ExtensionDirectory{resources.ToolfeedDirectory("workspace", "feeds"), resources.DatasourceDirectory("workspace", workspace.KindForgeDataSource)}
	var directories []resources.ExtensionDirectory
	var assets []string
	for _, directory := range declared {
		_, err := os.Stat(filepath.Join(root, directory.Directory))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		directories = append(directories, directory)
		assets = append(assets, directory.Directory)
	}
	if len(directories) != 0 {
		defs, err := resources.SnapshotExtensionDefinitions(context.Background(), root, directories)
		if err != nil {
			return err
		}
		if len(defs) != 0 {
			snapshot, err := resources.NewNativeAssetSnapshot(context.Background(), root, defs, resources.NativeSnapshotOptions{AssetPaths: assets, ImmutableUntilRestart: true})
			if err != nil {
				return err
			}
			bindings, err := snapshot.Bindings(resources.InternalProviderIdentity, func(ctx context.Context, a identity.VerifiedActor, uri identity.ResourceURI, action string) (identity.ResourceRevisionPolicy, error) {
				if err := publicDefinitionRead(ctx, a, uri, action); err != nil {
					return nil, err
				}
				return publicDefinitionRevision{actor: a, uri: uri.String()}, nil
			})
			if err != nil {
				return err
			}
			out.LocalPrimitiveProvider, err = resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: resources.InternalProviderIdentity, Actor: actor, Verify: verify, Authorize: publicDefinitionRead, Bindings: bindings, Validators: map[string]resources.LocalResourceValidator{primitive.ToolfeedKind: resources.ValidateToolfeedDefinition, "datasource": resources.ValidateDatasourceDefinition}})
			if err != nil {
				return err
			}
		}
	}

	out.PrimitiveProviders = resources.NewGateway(out.MCPManager, actor, verify, b.primitiveGatewayIdentity)
	return nil
}

func publicDefinitionRead(ctx context.Context, a identity.VerifiedActor, uri identity.ResourceURI, action string) error {
	if ctx == nil || ctx.Err() != nil || !a.PublicRead || !a.ReadValid(time.Now()) || uri.Namespace != "workspace" {
		return identity.ErrResourceDenied
	}
	switch action {
	case "namespace.list", "namespace.get":
		if uri.Kind == "resource" && uri.Name == "catalog" {
			return nil
		}
	case "resource.list", "resource.get":
		if uri.Kind == primitive.ToolfeedKind || uri.Kind == "datasource" {
			return nil
		}
	}
	return identity.ErrResourceDenied
}

type publicDefinitionRevision struct {
	actor identity.VerifiedActor
	uri   string
}

func (p publicDefinitionRevision) SelectRevision(ctx context.Context, ref identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	uri, err := identity.ParseResourceURI(ref.URI)
	if err != nil {
		return identity.ResourceDecision{}, err
	}
	if err := publicDefinitionRead(ctx, p.actor, uri, "resource.get"); err != nil {
		return identity.ResourceDecision{}, err
	}
	if ref.URI != p.uri || ref.Revision != "" && ref.Revision != identity.WorkingCandidate || len(candidates) != 1 || candidates[0].Kind != identity.WorkingCandidate {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: "public-local-definition", ValidUntil: p.actor.ValidUntil}, nil
}
