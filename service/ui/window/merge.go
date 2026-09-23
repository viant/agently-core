package window

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/agently-core/workspace"
	wsmeta "github.com/viant/agently-core/workspace/service/meta"
	forgeTypes "github.com/viant/forge/backend/types"
)

// MergeWorkspaceForgeAssets attaches workspace datasources, dialogs, schemas and
// resource models to the supplied window according to the window-owned
// resource assignment. Only explicitly assigned assets and the transitive
// dependencies of attached assets (window-local declarations included) are
// merged; window-owned declarations are never overwritten. A nil or empty
// assignment attaches nothing beyond what the window's own declarations need.
//
// After attaching, the effective window is validated: every reference the
// window makes (YAML dataSourceRef/dialogId/modelRef and action-code literals)
// to a workspace asset must resolve to an attached asset, otherwise the load
// fails with the missing assignments spelled out.
func MergeWorkspaceForgeAssets(ctx context.Context, window *forgeTypes.Window, assignment *ResourceAssignment) error {
	if window == nil {
		return nil
	}
	svc := wsmeta.New(afs.New(), workspace.Root())
	catalog, err := loadWorkspaceCatalog(ctx, svc)
	if err != nil {
		return err
	}
	resolver := newAssignmentResolver(window, catalog)
	resolver.applyAssignment(assignment.Normalized())
	resolver.resolve()
	if err := resolver.attach(); err != nil {
		return err
	}
	if err := enrichWorkspaceWindow(ctx, window); err != nil {
		return err
	}
	// Host extensions (including report builders) add datasource references.
	// Validate the complete window, while keeping missing datasources non-fatal.
	if _, err := validateWindowReferences(window, catalog); err != nil {
		return err
	}
	return forgeTypes.ValidateResourceModels(window)
}

// assignmentResolver computes the transitive closure of assigned assets over
// the workspace catalog. Dependencies follow the Forge reference vocabulary:
//
//	datasource      -> resourceModelRef, parent dataSourceRef
//	dialog          -> dataSourceRef, lookup.dataSource, nested dialogId, modelRef
//	resource model  -> schemaRef, read/write dataSourceRef, field modelRef
//	schema          -> $ref schemas
type assignmentResolver struct {
	window  *forgeTypes.Window
	catalog *workspaceCatalog

	localDataSources map[string]bool
	localDialogs     map[string]bool
	localSchemas     map[string]bool
	localModels      map[string]bool

	dataSources map[string]bool
	dialogs     map[string]bool
	schemas     map[string]bool
	models      map[string]bool
	// explicitDialogs are assigned directly and must be valid.
	explicitDialogs map[string]bool
	// unresolved collects assigned identities the workspace does not provide;
	// they are logged server side and surfaced to the browser console.
	unresolved []string

	queue []dependency
}

type dependency struct {
	kind string
	id   string
}

func newAssignmentResolver(window *forgeTypes.Window, catalog *workspaceCatalog) *assignmentResolver {
	resolver := &assignmentResolver{
		window:           window,
		catalog:          catalog,
		localDataSources: map[string]bool{},
		localDialogs:     map[string]bool{},
		localSchemas:     map[string]bool{},
		localModels:      map[string]bool{},
		dataSources:      map[string]bool{},
		dialogs:          map[string]bool{},
		schemas:          map[string]bool{},
		models:           map[string]bool{},
		explicitDialogs:  map[string]bool{},
	}
	for id, dataSource := range window.DataSource {
		resolver.localDataSources[id] = true
		resolver.enqueueReferences(collectAssetReferences(dataSource))
	}
	for _, dialog := range window.Dialogs {
		if id := strings.TrimSpace(dialog.Id); id != "" {
			resolver.localDialogs[id] = true
		}
		resolver.enqueueReferences(collectAssetReferences(dialog))
	}
	for name, schema := range window.Schemas {
		resolver.localSchemas[name] = true
		resolver.enqueueReferences(collectAssetReferences(schema))
	}
	for name, model := range window.ResourceModels {
		resolver.localModels[name] = true
		resolver.enqueueReferences(collectAssetReferences(model))
	}
	return resolver
}

// applyAssignment seeds the closure with the explicit whitelist. Assigned
// identities that no workspace asset provides are preserved as declared intent
// and logged as unresolved; they never block the window.
func (r *assignmentResolver) applyAssignment(assignment *ResourceAssignment) {
	identity := windowIdentity(r.window)
	for _, id := range assignment.DataSources {
		if r.localDataSources[id] {
			continue
		}
		if _, ok := r.catalog.dataSources[id]; !ok {
			r.noteUnresolved(identity, fmt.Sprintf("datasource %q", id))
			continue
		}
		r.queue = append(r.queue, dependency{kind: "dataSource", id: id})
	}
	for _, id := range assignment.Dialogs {
		if r.localDialogs[id] {
			continue
		}
		if _, ok := r.catalog.dialogs[id]; !ok {
			r.noteUnresolved(identity, fmt.Sprintf("dialog %q", id))
			continue
		}
		r.explicitDialogs[id] = true
		r.queue = append(r.queue, dependency{kind: "dialog", id: id})
	}
	for _, name := range assignment.Models {
		registry, ok := r.catalog.registries[name]
		if !ok {
			r.noteUnresolved(identity, fmt.Sprintf("resource model registry %q (missing or quarantined)", name))
			continue
		}
		for schema := range registry.Schemas {
			r.queue = append(r.queue, dependency{kind: "schema", id: schema})
		}
		for model := range registry.ResourceModels {
			r.queue = append(r.queue, dependency{kind: "model", id: model})
		}
	}
	for _, name := range assignment.Schemas {
		if r.localSchemas[name] {
			continue
		}
		if _, ok := r.catalog.schemas[name]; !ok {
			r.noteUnresolved(identity, fmt.Sprintf("resource schema %q", name))
			continue
		}
		r.queue = append(r.queue, dependency{kind: "schema", id: name})
	}
	for _, name := range assignment.ResourceModels {
		if r.localModels[name] {
			continue
		}
		if _, ok := r.catalog.models[name]; !ok {
			r.noteUnresolved(identity, fmt.Sprintf("resource model %q", name))
			continue
		}
		r.queue = append(r.queue, dependency{kind: "model", id: name})
	}
}

func (r *assignmentResolver) noteUnresolved(identity, what string) {
	message := fmt.Sprintf("resources assigns %s that the workspace does not provide (unresolved, ignored)", what)
	log.Printf("workspace forge: window %s %s", identity, message)
	r.unresolved = append(r.unresolved, message)
}

func (r *assignmentResolver) enqueueReferences(refs *AssetReferences) {
	if refs == nil {
		return
	}
	for id := range refs.DataSources {
		r.queue = append(r.queue, dependency{kind: "dataSource", id: id})
	}
	for id := range refs.Dialogs {
		r.queue = append(r.queue, dependency{kind: "dialog", id: id})
	}
	for id := range refs.ResourceModels {
		r.queue = append(r.queue, dependency{kind: "model", id: id})
	}
	for id := range refs.Schemas {
		r.queue = append(r.queue, dependency{kind: "schema", id: id})
	}
}

// resolve drains the dependency queue, following references only through
// assets that exist in the workspace catalog.
func (r *assignmentResolver) resolve() {
	for len(r.queue) > 0 {
		next := r.queue[0]
		r.queue = r.queue[1:]
		switch next.kind {
		case "dataSource":
			if r.localDataSources[next.id] || r.dataSources[next.id] {
				continue
			}
			dataSource, ok := r.catalog.dataSources[next.id]
			if !ok {
				continue
			}
			r.dataSources[next.id] = true
			r.enqueueReferences(collectAssetReferences(dataSource))
		case "dialog":
			if r.localDialogs[next.id] || r.dialogs[next.id] {
				continue
			}
			asset, ok := r.catalog.dialogs[next.id]
			if !ok {
				continue
			}
			r.dialogs[next.id] = true
			r.enqueueReferences(collectAssetReferences(asset.dialog))
		case "model":
			if r.localModels[next.id] || r.models[next.id] {
				continue
			}
			entry, ok := r.catalog.models[next.id]
			if !ok {
				continue
			}
			r.models[next.id] = true
			r.enqueueReferences(collectAssetReferences(entry.model))
		case "schema":
			if r.localSchemas[next.id] || r.schemas[next.id] {
				continue
			}
			entry, ok := r.catalog.schemas[next.id]
			if !ok {
				continue
			}
			r.schemas[next.id] = true
			r.enqueueReferences(collectAssetReferences(entry.schema))
		}
	}
}

// attach merges the resolved assets into the window without overwriting any
// window-owned declaration.
func (r *assignmentResolver) attach() error {
	window := r.window
	if len(r.dataSources) > 0 && window.DataSource == nil {
		window.DataSource = map[string]forgeTypes.DataSource{}
	}
	for _, id := range sortedKeys(r.dataSources) {
		if _, exists := window.DataSource[id]; exists {
			continue
		}
		window.DataSource[id] = r.catalog.dataSources[id]
	}
	if len(r.schemas) > 0 && window.Schemas == nil {
		window.Schemas = map[string]forgeTypes.ResourceSchema{}
	}
	for _, name := range sortedKeys(r.schemas) {
		if _, exists := window.Schemas[name]; exists {
			log.Printf("workspace forge: ignoring workspace resource schema %q because window %s owns that name", name, windowIdentity(window))
			continue
		}
		window.Schemas[name] = r.catalog.schemas[name].schema
	}
	if len(r.models) > 0 && window.ResourceModels == nil {
		window.ResourceModels = map[string]forgeTypes.ResourceModel{}
	}
	for _, name := range sortedKeys(r.models) {
		if _, exists := window.ResourceModels[name]; exists {
			log.Printf("workspace forge: ignoring workspace resource model %q because window %s owns that name", name, windowIdentity(window))
			continue
		}
		window.ResourceModels[name] = r.catalog.models[name].model
	}
	for _, id := range sortedKeys(r.dialogs) {
		asset := r.catalog.dialogs[id]
		probe := *window
		probe.View = forgeTypes.View{}
		probe.Dialogs = []forgeTypes.Dialog{asset.dialog}
		if err := forgeTypes.ValidateResourceModels(&probe); err != nil {
			if r.explicitDialogs[id] {
				return fmt.Errorf("assigned workspace dialog %s (%s) is invalid for window %s: %w", id, workspaceAssetPath(asset.path), windowIdentity(window), err)
			}
			log.Printf("workspace forge: skipping invalid workspace dialog %s (%s) required by window %s: %v", id, workspaceAssetPath(asset.path), windowIdentity(window), err)
			continue
		}
		window.Dialogs = append(window.Dialogs, asset.dialog)
	}
	return nil
}

// validateWindowReferences checks the effective window against assigned assets.
// Missing datasource references are warnings so one unavailable source cannot
// prevent the rest of a window from loading. Other known missing resources
// remain assignment errors. Unknown identities and dynamic dialog ids are logged.
func validateWindowReferences(window *forgeTypes.Window, catalog *workspaceCatalog) ([]string, error) {
	refs := CollectWindowReferences(window)
	identity := windowIdentity(window)
	var notes []string
	note := func(format string, args ...interface{}) {
		message := fmt.Sprintf(format, args...)
		log.Printf("workspace forge: window %s %s", identity, message)
		notes = append(notes, message)
	}
	attachedDialogs := map[string]bool{}
	for _, dialog := range window.Dialogs {
		attachedDialogs[strings.TrimSpace(dialog.Id)] = true
	}
	missing := map[string]map[string]bool{"dialogs": {}, "resourceModels": {}, "schemas": {}}
	for id := range refs.YAML.DataSources {
		if _, attached := window.DataSource[id]; attached {
			continue
		}
		if _, known := catalog.dataSources[id]; known {
			note("references datasource %q that is not attached to the window", id)
		} else {
			note("references datasource %q that is not declared in the workspace", id)
		}
	}
	for id := range refs.YAML.Dialogs {
		if attachedDialogs[id] {
			continue
		}
		if _, known := catalog.dialogs[id]; known {
			missing["dialogs"][id] = true
		} else {
			note("references dialog %q that is not declared in the workspace", id)
		}
	}
	for name := range refs.YAML.ResourceModels {
		if _, attached := window.ResourceModels[name]; attached {
			continue
		}
		if _, known := catalog.models[name]; known {
			missing["resourceModels"][name] = true
		}
	}
	for name := range refs.YAML.Schemas {
		if _, attached := window.Schemas[name]; attached {
			continue
		}
		if _, known := catalog.schemas[name]; known {
			missing["schemas"][name] = true
		}
	}
	for literal := range refs.Code.Literals {
		if _, known := catalog.dialogs[literal]; known && !attachedDialogs[literal] {
			missing["dialogs"][literal] = true
		}
		if _, known := catalog.dataSources[literal]; known {
			if _, attached := window.DataSource[literal]; !attached {
				note("action code references datasource %q that is not attached to the window", literal)
			}
		}
	}
	if count := len(refs.Code.DynamicDialogs); count > 0 {
		log.Printf("workspace forge: window %s computes dialog ids dynamically at %d code location(s), e.g. %s; make sure resources.dialogs lists every dialog it can open", identity, count, refs.Code.DynamicDialogs[0])
	}
	var problems []string
	for _, kind := range []string{"dialogs", "resourceModels", "schemas"} {
		if len(missing[kind]) == 0 {
			continue
		}
		problems = append(problems, fmt.Sprintf("resources.%s is missing: %s", kind, strings.Join(sortedKeys(missing[kind]), ", ")))
	}
	if len(problems) == 0 {
		return notes, nil
	}
	return notes, fmt.Errorf("window %s references workspace assets that are not assigned:\n  %s", identity, strings.Join(problems, "\n  "))
}

func windowIdentity(window *forgeTypes.Window) string {
	if window == nil {
		return "<nil>"
	}
	if key := strings.TrimSpace(window.WindowKey); key != "" {
		return key
	}
	if window.View.Content != nil && strings.TrimSpace(window.View.Content.ID) != "" {
		return window.View.Content.ID
	}
	if ns := strings.TrimSpace(window.Namespace); ns != "" {
		return ns
	}
	return "<unnamed>"
}
