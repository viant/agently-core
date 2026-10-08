package window

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	wsmeta "github.com/viant/agently-core/workspace/service/meta"
	meta "github.com/viant/forge/backend/service/meta"
	"github.com/viant/forge/backend/types"
)

// These profiles cover the existing web/mobile client contracts. Additional
// profiles are explicit trusted ResourceBinding.Targets, never guessed from a
// requested client path. Each declared profile is materialized independently
// unless its actual source branch precedence is identical to another profile.
func standardWindowTargets() []types.WindowTarget {
	return []types.WindowTarget{{}, {Platform: "web"}, {Platform: "web", FormFactor: "desktop"}, {Platform: "web", FormFactor: "phone"}, {Platform: "web", FormFactor: "tablet"}, {Platform: "ios"}, {Platform: "ios", FormFactor: "phone"}, {Platform: "ios", FormFactor: "tablet"}, {Platform: "android"}, {Platform: "android", FormFactor: "phone"}, {Platform: "android", FormFactor: "tablet"}, {Surface: "app"}, {FormFactor: "phone"}, {FormFactor: "tablet"}}
}

func StandardWindowTargets() []types.WindowTarget { return standardWindowTargets() }
func (s *WorkspaceResourceSource) loadBundle(ctx context.Context, uri identity.ResourceURI) (json.RawMessage, error) {
	return s.LoadBundleWithOptions(ctx, uri, LoaderOptions{})
}

// LoadBundleWithOptions materializes every native target using one confined host filesystem.
func (s *WorkspaceResourceSource) LoadBundleWithOptions(ctx context.Context, uri identity.ResourceURI, options LoaderOptions) (result json.RawMessage, failure error) {
	defer func() {
		if err := options.check(); err != nil {
			result = nil
			failure = err
		}
	}()
	if s == nil || ctx == nil || ctx.Err() != nil || !uri.Valid() || s.windows[uri.String()] == "" {
		return nil, identity.ErrResourceDenied
	}
	profiles := append(standardWindowTargets(), s.targets[uri.String()]...)
	if len(profiles) > 128 {
		return nil, fmt.Errorf("too many window targets")
	}
	// Imports can select target branches below shared components too. Index all
	// actual Forge asset directories, then retain the existing loader's exact
	// ordered precedence. No content is cached across candidate reads.
	dirs := map[string]bool{}
	if options.AssetDirectories != nil {
		var err error
		dirs, err = options.AssetDirectories(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		assetRoot := filepath.Join(s.root, "extension", "forge")
		if err := filepath.WalkDir(assetRoot, func(p string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.IsDir() {
				rel, e := filepath.Rel(assetRoot, p)
				if e != nil {
					return e
				}
				parts := strings.Split(filepath.ToSlash(rel), "/")
				for i := range parts {
					dirs[strings.Join(parts[i:], "/")] = true
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}

	}
	svc := wsmeta.New(options.filesystem(), s.root)
	_, paths, err := loadWorkspaceDataSources(ctx, svc)
	if err != nil {
		return nil, err
	}
	descriptors := map[string]json.RawMessage{}
	envelope := types.WindowResourceEnvelope{SchemaVersion: 2, Format: types.WindowBundleFormat, Variants: map[string]types.WindowResourceVariant{}}
	seen, materialized := map[string]bool{}, map[string]string{}
	for _, profile := range profiles {
		profile, err = profile.Normalize()
		if err != nil || profile.SelectionToken != "" {
			return nil, fmt.Errorf("invalid declared target profile")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		key := profile.ProfileKey()
		if seen[key] {
			return nil, fmt.Errorf("duplicate declared window profile")
		}
		seen[key] = true
		var target *meta.TargetContext
		signature := "default"
		if key != (types.WindowTarget{}).ProfileKey() {
			target = &meta.TargetContext{Platform: profile.Platform, FormFactor: profile.FormFactor, Surface: profile.Surface}
			var branches []string
			for _, b := range meta.TargetBranchCandidates(target) {
				if dirs[b] {
					branches = append(branches, b)
				}
			}
			signature = "target:" + strings.Join(branches, "|")
		}
		variantID := materialized[signature]
		if variantID == "" {
			window, err := LoadWorkspaceWindowWithOptionsAt(ctx, s.root, s.windows[uri.String()], target, s.enrich, options)
			if err != nil {
				return nil, err
			}
			if window == nil {
				return nil, identity.ErrResourceDenied
			}
			window.Resource = nil
			window.ResourceTarget = nil
			window.ResourceDependencies = map[string]string{}
			variant := types.WindowResourceVariant{Window: window, DataSources: map[string]json.RawMessage{}}
			for id, inline := range window.DataSource {
				descriptor := dsproto.DataSource{DataSource: inline, ID: id}
				if path := paths[id]; path != "" {
					base := descriptors[id]
					if base == nil {
						var global dsproto.DataSource
						if err := svc.Load(ctx, path, &global); err != nil {
							return nil, err
						}
						if global.ID == "" {
							global.ID = id
						}
						base, err = json.Marshal(global)
						if err != nil {
							return nil, err
						}
						descriptors[id] = base
					}
					// Decode into a fresh value: decoding into the inline value
					// would reuse and overwrite its shared parameter slices/maps.
					descriptor = dsproto.DataSource{}
					if json.Unmarshal(base, &descriptor) != nil {
						return nil, fmt.Errorf("invalid datasource descriptor")
					}
					// Backend/cache stay from the trusted producer declaration. The
					// selected window owns its effective inline schema, defaults,
					// parameter bindings, selectors and rendering contract.
					descriptor.DataSource = inline
				}
				raw, err := json.Marshal(descriptor)
				if err != nil {
					return nil, err
				}
				raw, err = types.CanonicalWindowDescriptor(raw)
				if err != nil {
					return nil, err
				}
				variant.DataSources[id] = raw
				window.ResourceDependencies[id] = identity.ContentFingerprint(raw)
			}
			variantID, err = types.WindowVariantFingerprint(variant)
			if err != nil {
				return nil, err
			}
			envelope.Variants[variantID] = variant
			materialized[signature] = variantID
		}
		envelope.Targets = append(envelope.Targets, types.WindowTargetBinding{Target: profile, Variant: variantID})
	}
	if err := envelope.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(envelope)
}
