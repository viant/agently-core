package agent

import (
	"context"
	"encoding/json"
	"github.com/viant/agently-core/internal/debugtrace"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	intakesvc "github.com/viant/agently-core/service/intake"
	resourcesvc "github.com/viant/agently-core/service/resource"
)

// WithNamespaceProvider reuses the host's authorized namespace catalog. It is
// optional: missing/unavailable metadata retains ordinary flat discovery.
func WithNamespaceProvider(provider func(context.Context) ([]resourcesvc.DiscoveredNamespace, error)) Option {
	return func(s *Service) { s.namespaceProvider = provider }
}

type namespaceSnapshotKey struct{}
type namespaceSnapshot struct {
	scope    namespaceScope
	once     sync.Once
	provider func(context.Context) ([]resourcesvc.DiscoveredNamespace, error)
	items    []resourcesvc.DiscoveredNamespace
	byName   map[string][]resourcesvc.DiscoveredNamespace
}

func (snapshot *namespaceSnapshot) load(ctx context.Context) {
	snapshot.once.Do(func() {
		started := time.Now()
		failed := false
		if snapshot.provider != nil {
			var err error
			snapshot.items, err = snapshot.provider(ctx)
			if err != nil {
				failed = true
				snapshot.items = nil
			}
		}
		snapshot.byName = map[string][]resourcesvc.DiscoveredNamespace{}
		for _, item := range snapshot.items {
			snapshot.byName[item.Namespace] = append(snapshot.byName[item.Namespace], item)
		}
		debugtrace.Write("agent", "namespace_metadata_stage", map[string]interface{}{"conversationID": snapshot.scope.conversation, "turnID": snapshot.scope.turn, "elapsedMs": float64(time.Since(started)) / float64(time.Millisecond), "namespaceCount": len(snapshot.items), "failed": failed})
	})
}

type namespaceScope struct{ user, canonical, provider, conversation, turn string }

func namespaceScopeFrom(ctx context.Context) namespaceScope {
	turn, _ := requestctx.TurnMetaFromContext(ctx)
	return namespaceScope{authctx.EffectiveUserID(ctx), authctx.CanonicalUserID(ctx), authctx.Provider(ctx), requestctx.ConversationIDFromContext(ctx), turn.TurnID}
}

// A request/turn holds the gateway's metadata snapshot on its existing context;
// the gateway remains the sole catalog/cache owner. A new Query masks inherited
// snapshots so another actor/turn never reuses a parent's metadata.
func (s *Service) withNamespaceSnapshot(ctx context.Context) context.Context {
	return context.WithValue(ctx, namespaceSnapshotKey{}, &namespaceSnapshot{scope: namespaceScopeFrom(ctx), provider: s.namespaceProvider})
}

func (s *Service) ensureNamespaceSnapshot(ctx context.Context) context.Context {
	if snapshot, ok := ctx.Value(namespaceSnapshotKey{}).(*namespaceSnapshot); ok && snapshot.scope == namespaceScopeFrom(ctx) {
		return ctx
	}
	return s.withNamespaceSnapshot(ctx)
}

func namespaceSupports(item resourcesvc.DiscoveredNamespace, kind string) bool {
	if strings.TrimSpace(item.Description) == "" {
		return false
	}
	for _, supported := range item.Kinds {
		if supported.Kind == kind {
			return true
		}
	}
	return false
}

func namespaceOwner(item resourcesvc.DiscoveredNamespace) string {
	if item.ProviderIdentity == "" || item.ProviderIdentity != item.Connection.ProviderIdentity {
		return ""
	}
	return item.ProviderIdentity
}

func unambiguousNamespaceOwner(namespace string, namespaces []resourcesvc.DiscoveredNamespace) string {
	owner := ""
	for _, item := range namespaces {
		if item.Namespace != namespace || strings.TrimSpace(item.Description) == "" || namespaceOwner(item) == "" {
			continue
		}
		if owner != "" && owner != namespaceOwner(item) {
			return ""
		}
		owner = namespaceOwner(item)
	}
	return owner
}

// A card must be the same validated owner's descriptor that justified hiding
// individual metadata. Conflicting descriptions/kinds for the same owner are
// ambiguous, so those resources stay flat rather than picking an arbitrary row.
func describedNamespaceDescriptor(namespace string, namespaces []resourcesvc.DiscoveredNamespace) *resourcesvc.DiscoveredNamespace {
	owner := unambiguousNamespaceOwner(namespace, namespaces)
	if owner == "" {
		return nil
	}
	var selected *resourcesvc.DiscoveredNamespace
	for _, item := range namespaces {
		if item.Namespace != namespace || namespaceOwner(item) != owner || strings.TrimSpace(item.Description) == "" {
			continue
		}
		if selected == nil {
			copy := item
			selected = &copy
			continue
		}
		if !reflect.DeepEqual(item.NamespaceMetadata, selected.NamespaceMetadata) || !reflect.DeepEqual(item.Kinds, selected.Kinds) {
			return nil
		}
	}
	return selected
}

func describedToolNamespace(def llm.ToolDefinition, namespaces []resourcesvc.DiscoveredNamespace) string {
	uri, err := identity.ParseResourceURI(def.SourceResourceURI)
	if err != nil || uri.Kind != "mcp" || def.SourceProviderIdentity == "" {
		return ""
	}
	descriptor := describedNamespaceDescriptor(uri.Namespace, namespaces)
	if descriptor == nil || namespaceOwner(*descriptor) != def.SourceProviderIdentity || !namespaceSupports(*descriptor, "mcp") {
		return ""
	}
	return uri.Namespace
}

func describedSkillNamespace(uriText, connectionAlias string, namespaces []resourcesvc.DiscoveredNamespace) string {
	uri, err := identity.ParseResourceURI(uriText)
	if err != nil || uri.Kind != "skill" || connectionAlias == "" {
		return ""
	}
	descriptor := describedNamespaceDescriptor(uri.Namespace, namespaces)
	if descriptor == nil || !namespaceSupports(*descriptor, "skill") {
		return ""
	}
	// ServerID is a connection alias. It must match the actual namespace source,
	// not merely another provider serving the same canonical skill URI.
	for _, item := range namespaces {
		if item.Namespace == uri.Namespace && namespaceOwner(item) == namespaceOwner(*descriptor) && item.Connection.Name == connectionAlias && namespaceSupports(item, "skill") {
			return uri.Namespace
		}
	}
	return ""
}

func (s *Service) namespaceDefinitions(ctx context.Context, definitions []llm.ToolDefinition) []llm.ToolDefinition {
	if len(definitions) == 0 {
		return definitions
	}
	// Only scalar host metadata changes; own the slice before assigning it.
	// Nested immutable schemas remain shared with the authorized registry.
	definitions = append([]llm.ToolDefinition(nil), definitions...)
	ctx = s.ensureNamespaceSnapshot(ctx)
	snapshot := ctx.Value(namespaceSnapshotKey{}).(*namespaceSnapshot)
	snapshot.load(ctx)
	for i := range definitions {
		definitions[i].DiscoveryNamespace = ""
		uri, err := identity.ParseResourceURI(definitions[i].SourceResourceURI)
		if err == nil {
			definitions[i].DiscoveryNamespace = describedToolNamespace(definitions[i], snapshot.byName[uri.Namespace])
		}
	}
	return definitions
}

func (s *Service) retainedNamespaceDiscovery(ctx context.Context, input *QueryInput) map[string]bool {
	definitions, err := s.resolveAlwaysIncludedTools(ctx, input)
	if err != nil {
		return nil
	}
	have := map[string]bool{}
	for _, def := range definitions {
		if retainedToolDefinition(def) {
			have[mcpname.Canonical(def.Name)] = true
		}
	}
	return map[string]bool{
		"mcp":   have[mcpname.Canonical("llm/tools:list")],
		"skill": have[mcpname.Canonical("llm/skills:list")] && have[mcpname.Canonical("llm/skills:get")] && have[mcpname.Canonical("llm/skills:activate")],
	}

}

// Hints are presentation data, not a discovery or execution boundary. Later
// discovery/activation remains free to use any authorized namespace or tool.
func appendDiscoveryNamespaceHints(input *QueryInput, b *binding.Binding) {
	if input == nil || b == nil {
		return
	}
	intake := intakesvc.FromContext(input.Context)
	if intake == nil || len(intake.Prompting.DiscoveryNamespaces) == 0 {
		return
	}
	names := append([]string(nil), intake.Prompting.DiscoveryNamespaces...)
	sort.Strings(names)
	raw, _ := json.Marshal(names)
	b.SystemDocuments.Items = append(b.SystemDocuments.Items, &binding.Document{
		Title: "Initial discovery hints", SourceURI: "internal://intake/discovery-namespaces", MimeType: "text/plain",
		PageContent: "Intake suggests these initial discovery namespaces (optional hints, not permission or a restriction): " + string(raw) + ". Use retained llm/tools:list, or llm/skills:list for advertised skill kinds, with namespace filters to inspect capabilities. Broaden discovery outside these hints whenever useful; stale/empty results do not prohibit any authorized tool, skill or profile. Use authorized prompt:list for playbook discovery; it currently has no namespace filter.",
	})
}

// Namespace cards carry the existing producer contract, not inferred domains.
type namespaceCapability struct {
	Kind           string                  `json:"kind"`
	Name           string                  `json:"name"`
	Description    string                  `json:"description"`
	UseCases       []string                `json:"useCases,omitempty"`
	SupportedKinds []primitive.KindSupport `json:"supportedKinds,omitempty"`
}
