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
	workspacewindow "github.com/viant/agently-core/service/ui/window"
	"github.com/viant/agently-core/workspace"
	wscfg "github.com/viant/agently-core/workspace/config"
	"github.com/viant/forge/backend/reporting/registry"
	forgetypes "github.com/viant/forge/backend/types"
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
	// Existing canonical window consumers retain their configured owning-server
	// path. Other default primitives still publish independently below.
	captureWorkspaceWindows := out.UIBridge == nil || !out.UIBridge.UsesWindowResourceResolution()
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
	for _, directory := range declared {
		_, err := os.Stat(filepath.Join(root, directory.Directory))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		directories = append(directories, directory)
	}

	var windowBindings []workspacewindow.ResourceBinding
	var err error
	if captureWorkspaceWindows {
		windowBindings, err = resources.NativeWorkspaceWindowBindings(context.Background(), root, "workspace")
		if err != nil {
			return err
		}
	}
	var windowDefs []resources.NativeSnapshotDefinition
	if len(windowBindings) > 0 {
		windowDefs, err = resources.SnapshotWindowDefinitions(context.Background(), root, windowBindings, nil)
	}
	if err != nil {
		return err
	}

	workspaceKeys := map[string]bool{}
	for _, binding := range windowBindings {
		workspaceKeys[binding.WindowKey] = true
	}

	var defs []resources.NativeSnapshotDefinition
	if len(directories) > 0 {
		defs, err = resources.SnapshotExtensionDefinitions(context.Background(), root, directories)
		if err != nil {
			return err
		}
	}
	reportRoot := ""
	config, err := wscfg.Load(root)
	if err != nil {
		return err
	}
	if config != nil {
		reportRoot = config.ForgeReportingRoot()
	}
	reportDefs, reportOptions, err := resources.NativeWorkspaceReportDefinitions(context.Background(), root, "workspace", reportRoot, windowDefs)
	if err != nil {
		return err
	}
	titles := map[string]string{}
	for _, def := range windowDefs {
		titles[def.URI] = def.Title
	}
	var reportRegistry *registry.Registry
	enrich := resources.StaticWorkspaceWindowEnricher(func(_ context.Context, w *forgetypes.Window) error {
		if reportRegistry == nil {
			return nil
		}
		return reportRegistry.EnrichWindow(w)
	})
	if len(windowBindings) > 0 {
		windowDefs, err = resources.SnapshotWindowDefinitions(context.Background(), root, windowBindings, enrich)
	}
	if err != nil {
		return err
	}
	for i := range windowDefs {
		windowDefs[i].Title = titles[windowDefs[i].URI]
	}
	b.nativeWindowAliases = make(map[string]string, len(b.nativeWindowDefaults))
	for _, entry := range b.nativeWindowDefaults {
		if workspaceKeys[entry.Resource.WindowKey] && entry.Resource.URI == "window://workspace/"+entry.Resource.WindowKey {
			continue
		}
		if !workspaceKeys[entry.Resource.WindowKey] {
			b.nativeWindowAliases[entry.Resource.WindowKey] = entry.Resource.URI
		}
		windowDefs = append(windowDefs, resources.NativeSnapshotDefinition{URI: entry.Resource.URI, Title: entry.Resource.WindowKey, FormatVersion: 2, File: "embedded/window", Load: resources.NativeComposedWindowLoader(entry.Resource, entry.Load, enrich)})
	}
	defs = append(defs, windowDefs...)
	defs = append(defs, reportDefs...)
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
	_, layoutErr := os.Stat(layoutPath)
	if layoutErr == nil {
		definition, err := resources.SnapshotLayoutMenuDefinition(layoutRef, menuURI)
		if err != nil {
			return err
		}
		definition.Load = resources.NativeLayoutMenuLoaderWithCapture(capture)
		defs = append(defs, definition)
	} else if !os.IsNotExist(layoutErr) || explicit {
		return layoutErr
	} else if len(b.nativeLayoutDefault) != 0 {
		definition, err := resources.SnapshotEmbeddedLayoutMenuDefinition(b.nativeLayoutDefault, menuURI, capture)
		if err != nil {
			return err
		}
		layoutRef = "embedded"
		defs = append(defs, definition)
	}
	checkLayoutSource := func(ctx context.Context) error {
		if ctx == nil || ctx.Err() != nil {
			return identity.ErrResourceDenied
		}
		return nil
	}
	declaredNamespaces := map[string]bool{"workspace": true}
	for _, def := range defs {
		uri, e := identity.ParseResourceURI(def.URI)
		if e != nil {
			return e
		}
		declaredNamespaces[uri.Namespace] = true
	}
	var nativeSnapshot *resources.NativeAssetSnapshot
	projection := layoutgate.MenuProjection(permittedview.NewRuntime(out.PermittedResolver), out.AuthorizationPolicy)
	authorize := func(ctx context.Context, a identity.VerifiedActor, uri identity.ResourceURI, action string) error {
		checked := uri
		if declaredNamespaces[uri.Namespace] {
			checked.Namespace = "workspace"
		}
		if err := publicDefinitionRead(ctx, a, checked, action); err != nil {
			return err
		}
		if uri.Kind == "menu" {
			return checkLayoutSource(ctx)
		}
		if uri.Kind == "window" && out.NativeLayoutSource != nil {
			menu, err := identity.ParseResourceURI(menuURI)
			if err != nil {
				return err
			}
			candidates, err := nativeSnapshot.Candidates(ctx, menu)
			if err != nil || len(candidates) != 1 {
				return identity.ErrResourceDenied
			}
			original, err := nativeSnapshot.ReadCandidate(ctx, menu, candidates[0])
			if err != nil {
				return err
			}
			visible, err := projection(ctx, original)
			if err != nil {
				return err
			}
			allowed, err := layoutgate.WindowVisible(original, visible, uri.Name)
			if err != nil {
				return err
			}
			if !allowed {
				return identity.ErrResourceDenied
			}
		}
		return nil
	}
	if len(defs) != 0 {
		snapshot, err := resources.NewNativeAssetSnapshot(context.Background(), root, defs, resources.NativeSnapshotOptions{BeforeCompile: func(ctx context.Context, r *resources.ExtensionReader) error {
			confined, check, err := resources.ConfinedReportingOptions(ctx, r, reportOptions)
			if err != nil {
				return err
			}
			reportRegistry, err = registry.Discover(ctx, confined)
			if err != nil {
				return err
			}
			if err = check(); err != nil {
				return err
			}
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
		nativeSnapshot = snapshot
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
			if err := authorize(ctx, a, uri, action); err != nil {
				return nil, err
			}
			return publicDefinitionRevision{actor: a, uri: uri.String(), read: authorize}, nil
		})
		if err != nil {
			return err
		}
		out.LocalPrimitiveProvider, err = resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: resources.InternalProviderIdentity, Actor: actor, Verify: verify, Authorize: authorize, Bindings: bindings, WindowIndex: snapshot.WindowIndex, MenuVisibilityProjection: func(ctx context.Context, _ identity.VerifiedActor, _ identity.ResourceURI, raw json.RawMessage) (json.RawMessage, error) {
			return projection(ctx, raw)
		}, Validators: map[string]resources.LocalResourceValidator{primitive.ToolfeedKind: resources.ValidateToolfeedDefinition, "datasource": resources.ValidateDatasourceDefinition, "template": resources.ValidateTemplateDefinition, "intent": resources.ValidateIntentDefinition, "menu": resources.ValidateMenu, "window": resources.ValidateWindowBundle, "report": resources.ValidateReportEnvelope}})
		if err != nil {
			return err
		}
	}

	out.publicPrimitiveDefault = true
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
		if uri.Kind == primitive.ToolfeedKind || uri.Kind == "datasource" || uri.Kind == "template" || uri.Kind == "intent" || uri.Kind == "menu" || uri.Kind == "window" || uri.Kind == "report" {
			return nil
		}
	}
	return identity.ErrResourceDenied
}

type publicDefinitionRevision struct {
	actor identity.VerifiedActor
	uri   string
	read  resources.LocalResourceAuthorizer
}

func (p publicDefinitionRevision) SelectRevision(ctx context.Context, ref identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	uri, err := identity.ParseResourceURI(ref.URI)
	if err != nil {
		return identity.ResourceDecision{}, err
	}
	read := p.read
	if read == nil {
		read = publicDefinitionRead
	}
	if err := read(ctx, p.actor, uri, "resource.get"); err != nil {
		return identity.ResourceDecision{}, err
	}
	if ref.URI != p.uri || ref.Revision != "" && ref.Revision != identity.WorkingCandidate || len(candidates) != 1 || candidates[0].Kind != identity.WorkingCandidate {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: "public-local-definition", ValidUntil: p.actor.ValidUntil}, nil
}
