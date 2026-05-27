package resource

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	htmpl "html/template"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/viant/afs"
	windowloader "github.com/viant/agently-core/service/ui/window"
	"github.com/viant/agently-core/workspace"
	wsmeta "github.com/viant/agently-core/workspace/service/meta"
	forgeTypes "github.com/viant/forge/backend/types"

	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpuiproto "github.com/viant/mcp-ui/appproto"
	mcpuimeta "github.com/viant/mcp-ui/meta"
	mcpuiresource "github.com/viant/mcp-ui/resource"
)

//go:embed workspace_forge.html.tmpl
var workspaceForgeHTMLTemplate string

var parsedWorkspaceForgeHTMLTemplate = htmpl.Must(htmpl.New("workspace_forge.html.tmpl").Parse(workspaceForgeHTMLTemplate))

// WorkspaceServerScope is the canonical server-scope used to publish
// workspace-backed forge windows as MCP UI resources. It identifies the
// default Agently workspace, follows the reserved `agently.*` module
// prefix, and uses a deterministic suffix so identical effective workspace
// inputs always yield the same URI across process restarts.
const WorkspaceServerScope = "agently.wk_default"

// WorkspaceForgeRendererPath is the canonical same-origin SPA route the MCP UI
// host loads inside the bubble iframe when a workspace view resource opts in to
// richer Forge-backed rendering. The page receives the workspace window key via
// the `windowKey` query parameter and bootstraps the canonical Forge
// `WindowContent` renderer through normal authenticated workspace fetches.
const WorkspaceForgeRendererPath = "/mcp-ui/forge-window"

// WorkspaceForgeSandbox is the per-resource sandbox opt-in published with
// workspace view resources. It narrowly adds `allow-same-origin` to the host
// default so the renderer page can perform authenticated same-origin workspace
// metadata and datasource fetches; no other relaxations are granted.
const WorkspaceForgeSandbox = "allow-scripts allow-same-origin"

// WorkspaceForgeRendererURL returns the canonical same-origin renderer URL
// for the workspace forge window identified by windowKey and resource URI.
// The URL includes the exact `resourceUri` plus a deterministic content-hash
// query argument so embedded bubbles do not reuse a stale renderer page after
// the published workspace UI resource changes.
func WorkspaceForgeRendererURL(windowKey, resourceURI, contentHash string) string {
	key := strings.TrimSpace(windowKey)
	if key == "" {
		return ""
	}
	query := url.Values{}
	query.Set("windowKey", key)
	if strings.TrimSpace(resourceURI) != "" {
		query.Set("resourceUri", strings.TrimSpace(resourceURI))
	}
	if strings.TrimSpace(contentHash) != "" {
		query.Set("v", strings.TrimSpace(contentHash))
	}
	return WorkspaceForgeRendererPath + "?" + query.Encode()
}

// WorkspaceViewURI returns the canonical `ui://...` resource URI for the
// workspace-owned forge window identified by windowKey.
func WorkspaceViewURI(windowKey string) string {
	return "ui://" + WorkspaceServerScope + "/" + mcpuiresource.KindView + "/" + strings.TrimSpace(windowKey)
}

// LoadWorkspaceForgeWindow loads the workspace-owned forge window for the
// supplied key by reusing the same canonical workspace fallback the HTTP
// /window handler uses. It returns (nil, nil) when no window with view
// content exists under the workspace.
func LoadWorkspaceForgeWindow(ctx context.Context, windowKey string) (*forgeTypes.Window, error) {
	return windowloader.LoadWorkspaceWindow(ctx, strings.TrimSpace(windowKey), nil)
}

// ListWorkspaceResources enumerates workspace-backed Forge windows with view
// content and exposes them as generic MCP UI resources keyed by windowKey.
// This stays generic: it publishes what the active workspace declares and does
// not hardcode any business-specific window identities.
func ListWorkspaceResources(ctx context.Context) ([]mcpschema.Resource, error) {
	svc := wsmeta.New(afs.New(), workspace.Root())
	exists, err := svc.Exists(ctx, workspace.KindForgeWindow)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	paths, err := svc.List(ctx, workspace.KindForgeWindow)
	if err != nil {
		return nil, err
	}
	resources := make([]mcpschema.Resource, 0, len(paths))
	for _, rawPath := range paths {
		if strings.ToLower(filepath.Ext(rawPath)) != ".yaml" {
			continue
		}
		windowKey := strings.TrimSpace(strings.TrimSuffix(filepath.Base(rawPath), filepath.Ext(rawPath)))
		if windowKey == "" {
			continue
		}
		window, err := LoadWorkspaceForgeWindow(ctx, windowKey)
		if err != nil {
			return nil, err
		}
		if window == nil || window.View.Content == nil {
			continue
		}
		title := strings.TrimSpace(window.Namespace)
		if title == "" {
			title = windowKey
		}
		desc := fmt.Sprintf("Workspace-backed MCP UI surface for the %s Forge window.", windowKey)
		res, err := mcpuiresource.NewHTMLResource(
			WorkspaceViewURI(windowKey),
			windowKey,
			&desc,
			&title,
			nil,
			mcpuimeta.ResourceUI{
				ProtocolVersion: mcpuiproto.Version,
			},
		)
		if err != nil {
			return nil, err
		}
		resources = append(resources, *res)
	}
	return resources, nil
}

// ReadWorkspaceResource resolves `ui://agently.wk_default/view/<windowKey>`
// URIs against the canonical workspace forge window loader. The HTML
// payload is server-assembled from the loaded forge window so the surface
// flows through MCP UI without inventing a parallel app-specific tool or
// rendering path.
func ReadWorkspaceResource(ctx context.Context, uri string) (*mcpschema.ReadResourceResult, error) {
	parsed, err := mcpuiresource.ValidateUIURI(uri)
	if err != nil {
		return nil, err
	}
	if parsed.ServerScope != WorkspaceServerScope {
		return nil, fmt.Errorf("workspace ui resource: unsupported server-scope %q", parsed.ServerScope)
	}
	if parsed.Kind != mcpuiresource.KindView {
		return nil, fmt.Errorf("workspace ui resource: unsupported kind %q", parsed.Kind)
	}
	return ReadWorkspaceViewResource(ctx, uri, parsed.ResourceID)
}

// ReadWorkspaceViewResource loads the workspace-owned forge window identified
// by windowKey and assembles a workspace-backed MCP UI HTML resource for the
// exact uri supplied by the caller.
//
// This helper is intentionally additive: it does not mutate ui/view behavior
// or rely on hosted-region open semantics. Callers provide the exact `ui://...`
// identity they want to publish, while the backing window truth still comes
// from the canonical workspace forge window loader.
func ReadWorkspaceViewResource(ctx context.Context, uri, windowKey string) (*mcpschema.ReadResourceResult, error) {
	windowKey = strings.TrimSpace(windowKey)
	window, err := LoadWorkspaceForgeWindow(ctx, windowKey)
	if err != nil {
		return nil, err
	}
	if window == nil {
		return nil, fmt.Errorf("workspace ui resource: unknown window %q", windowKey)
	}
	htmlPayload := renderWorkspaceForgeHTML(windowKey, window)
	contentHash := mcpuiresource.ContentHash(htmlPayload)
	contents, err := mcpuiresource.NewReadResultHTMLContents(uri, htmlPayload, mcpuimeta.ResourceUI{
		ContentHash:     contentHash,
		ProtocolVersion: mcpuiproto.Version,
		RendererURL:     WorkspaceForgeRendererURL(windowKey, uri, contentHash),
		Sandbox:         WorkspaceForgeSandbox,
	})
	if err != nil {
		return nil, err
	}
	return &mcpschema.ReadResourceResult{
		Contents: []mcpschema.ReadResourceResultContentsElem{*contents},
	}, nil
}

type workspaceForgeTemplateData struct {
	Title       string
	WindowKey   string
	Datasources []workspaceForgeDatasourceView
	Content     *workspaceForgeContainerView
}

type workspaceForgeDatasourceView struct {
	Name        string
	Cardinality string
}

type workspaceForgeContainerView struct {
	Label         string
	DataSourceRef string
	Items         []workspaceForgeItemView
	Containers    []workspaceForgeContainerView
}

type workspaceForgeItemView struct {
	Label         string
	DataSourceRef string
}

func renderWorkspaceForgeHTML(windowKey string, window *forgeTypes.Window) string {
	namespace := strings.TrimSpace(window.Namespace)
	title := namespace
	if title == "" {
		title = windowKey
	}
	view := workspaceForgeTemplateData{
		Title:       title,
		WindowKey:   windowKey,
		Datasources: workspaceForgeDatasources(window.DataSource),
	}
	if window.View.Content != nil {
		content := workspaceForgeContainer(window.View.Content)
		view.Content = &content
	}
	var buf bytes.Buffer
	if err := parsedWorkspaceForgeHTMLTemplate.Execute(&buf, view); err != nil {
		panic(fmt.Sprintf("render workspace forge html: %v", err))
	}
	return buf.String()
}

func workspaceForgeDatasources(dataSources map[string]forgeTypes.DataSource) []workspaceForgeDatasourceView {
	if len(dataSources) == 0 {
		return nil
	}
	keys := sortedDataSourceKeys(dataSources)
	result := make([]workspaceForgeDatasourceView, 0, len(keys))
	for _, key := range keys {
		result = append(result, workspaceForgeDatasourceView{
			Name:        key,
			Cardinality: strings.TrimSpace(dataSources[key].Cardinality),
		})
	}
	return result
}

func workspaceForgeContainer(container *forgeTypes.Container) workspaceForgeContainerView {
	view := workspaceForgeContainerView{
		Label:         workspaceForgeContainerLabel(container.Title, container.ID),
		DataSourceRef: strings.TrimSpace(container.Binding.DataSourceRef),
	}
	if len(container.Items) > 0 {
		view.Items = make([]workspaceForgeItemView, 0, len(container.Items))
		for _, item := range container.Items {
			view.Items = append(view.Items, workspaceForgeItemView{
				Label:         workspaceForgeItemLabel(item.Label, item.ID),
				DataSourceRef: strings.TrimSpace(item.Binding.DataSourceRef),
			})
		}
	}
	if len(container.Containers) > 0 {
		view.Containers = make([]workspaceForgeContainerView, 0, len(container.Containers))
		for index := range container.Containers {
			view.Containers = append(view.Containers, workspaceForgeContainer(&container.Containers[index]))
		}
	}
	return view
}

func workspaceForgeContainerLabel(primary, fallback string) string {
	label := strings.TrimSpace(primary)
	if label == "" {
		label = strings.TrimSpace(fallback)
	}
	if label == "" {
		label = "container"
	}
	return label
}

func workspaceForgeItemLabel(primary, fallback string) string {
	label := strings.TrimSpace(primary)
	if label == "" {
		label = strings.TrimSpace(fallback)
	}
	if label == "" {
		label = "item"
	}
	return label
}

func sortedDataSourceKeys(dataSources map[string]forgeTypes.DataSource) []string {
	keys := make([]string, 0, len(dataSources))
	for key := range dataSources {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
