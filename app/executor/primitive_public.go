package executor

import (
	"context"
	"encoding/json"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	auth "github.com/viant/agently-core/service/auth"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/service/ui/layoutgate"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/agently-core/workspace"
	wscfg "github.com/viant/agently-core/workspace/config"
	"os"
	"path/filepath"
	"strings"
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
	authored := resources.NewAuthoredCompilation(workspace.KindTemplate)
	intents := resources.NewIntentCompilation(workspace.KindIntent, workspace.KindToolBundle)
	legacyCompiler := resources.NewIntentCompilation(workspace.KindPrompt, workspace.KindToolBundle)
	defer intents.Release()
	defer legacyCompiler.Release()
	legacy := legacyCompiler.IntentDirectory("workspace")
	legacy.FallbackDirectory = workspace.KindIntent
	defer authored.Release()
	declared := []resources.ExtensionDirectory{resources.ToolfeedDirectory("workspace", "feeds"), resources.DatasourceDirectory("workspace", workspace.KindForgeDataSource), authored.TemplateDirectory("workspace"), intents.IntentDirectory("workspace"), legacy}
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

	defs, err := resources.SnapshotExtensionDefinitions(context.Background(), root, directories)
	if err != nil {
		return err
	}
	layoutRef, explicit := "ui/layout.yaml", false
	cfg, err := wscfg.Load(root)
	if err != nil {
		return err
	}
	if cfg != nil {
		if ui, ok := cfg.Raw["ui"].(map[string]any); ok {
			if layout, ok := ui["layout"].(map[string]any); ok {
				if value, ok := layout["ref"].(string); ok && strings.TrimSpace(value) != "" {
					layoutRef, explicit = value, true
				}
			}
		}
	}
	layoutPath, err := workspace.ResolveChildPath(root, layoutRef)
	if err != nil {
		return err
	}
	const menuURI = "menu://workspace/layout"
	capture := func(raw []byte) error {
		out.NativeLayoutSource = &resources.CapturedLayoutSource{Root: root, Source: layoutRef, Raw: append([]byte(nil), raw...)}
		return nil
	}
	embeddedLayout := false
	_, layoutErr := os.Stat(layoutPath)
	if layoutErr == nil {
		definition, err := resources.SnapshotLayoutMenuDefinition(layoutRef, menuURI)
		if err != nil {
			return err
		}
		definition.Load = resources.NativeLayoutMenuLoaderWithCapture(capture)
		defs = append(defs, definition)
		assets = append(assets, layoutRef)
	} else if !os.IsNotExist(layoutErr) || explicit {
		return layoutErr
	} else if len(b.nativeLayoutDefault) != 0 {
		definition, err := resources.SnapshotEmbeddedLayoutMenuDefinition(b.nativeLayoutDefault, menuURI, capture)
		if err != nil {
			return err
		}
		embeddedLayout = true
		layoutRef = "embedded"
		defs = append(defs, definition)
	}
	checkLayoutSource := func(ctx context.Context) error {
		if ctx == nil || ctx.Err() != nil {
			return identity.ErrResourceDenied
		}
		if embeddedLayout {
			if _, err := os.Lstat(layoutPath); !os.IsNotExist(err) {
				return identity.ErrResourceStale
			}
		}
		return nil
	}
	authorize := func(ctx context.Context, a identity.VerifiedActor, uri identity.ResourceURI, action string) error {
		if err := publicDefinitionRead(ctx, a, uri, action); err != nil {
			return err
		}
		if uri.Kind == "menu" {
			return checkLayoutSource(ctx)
		}
		return nil
	}
	if len(defs) != 0 {
		if assets == nil {
			assets = []string{}
		}
		snapshot, err := resources.NewNativeAssetSnapshot(context.Background(), root, defs, resources.NativeSnapshotOptions{AssetPaths: assets, ImmutableUntilRestart: true, BeforeCompile: func(ctx context.Context, r *resources.ExtensionReader) error {
			if err := authored.Prepare(ctx, r); err != nil {
				return err
			}
			if err := intents.Prepare(ctx, r); err != nil {
				return err
			}
			return legacyCompiler.Prepare(ctx, r)
		}})
		if err != nil {
			return err
		}
		if out.NativeLayoutSource != nil {
			out.NativeLayoutSource.Check = func(ctx context.Context) error {
				if err := checkLayoutSource(ctx); err != nil {
					return err
				}
				uri, _ := identity.ParseResourceURI(menuURI)
				_, err := snapshot.Candidates(ctx, uri)
				return err
			}
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
		projection := layoutgate.MenuProjection(permittedview.NewRuntime(out.PermittedResolver), out.AuthorizationPolicy)
		out.LocalPrimitiveProvider, err = resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: resources.InternalProviderIdentity, Actor: actor, Verify: verify, Authorize: authorize, Bindings: bindings, MenuVisibilityProjection: func(ctx context.Context, _ identity.VerifiedActor, _ identity.ResourceURI, raw json.RawMessage) (json.RawMessage, error) {
			return projection(ctx, raw)
		}, Validators: map[string]resources.LocalResourceValidator{primitive.ToolfeedKind: resources.ValidateToolfeedDefinition, "datasource": resources.ValidateDatasourceDefinition, "template": resources.ValidateTemplateDefinition, "intent": resources.ValidateIntentDefinition, "menu": resources.ValidateMenu}})
		if err != nil {
			return err
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
		if uri.Kind == primitive.ToolfeedKind || uri.Kind == "datasource" || uri.Kind == "template" || uri.Kind == "intent" || uri.Kind == "menu" {
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
