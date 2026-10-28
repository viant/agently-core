package agent

import (
	"context"
	"encoding/json"
	"errors"

	"sort"
	"strings"
	"time"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/internal/logx"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/capability"
	resourcesvc "github.com/viant/agently-core/service/resource"
	"golang.org/x/sync/errgroup"
)

type intakeCapability struct {
	Kind            string             `json:"kind"`
	Name            string             `json:"name"`
	Namespace       string             `json:"namespace"`
	Description     string             `json:"description,omitempty"`
	URI             string             `json:"uri,omitempty"`
	Definition      llm.ToolDefinition `json:"-"`
	LookupNames     []string           `json:"-"`
	AlwaysInclude   bool               `json:"-"`
	CacheScope      string             `json:"-"`
	ConnectionAlias string             `json:"-"`
}

// Intake sees metadata for the same tool surface available to execution. It
// receives no tool schemas, skill bodies, results, or extra execution privileges.
func (s *Service) intakeCapabilitySummary(ctx context.Context, input *QueryInput) (string, error) {
	summary, _, err := s.intakeCapabilities(ctx, input)
	return summary, err
}

// Tool names and prompt metadata come from one authorized catalog read. A nil
// names slice means no tool catalog was available; an empty slice is authoritative.
func (s *Service) intakeCapabilities(ctx context.Context, input *QueryInput) (string, []string, error) {
	summary, names, _, err := s.intakeCapabilitySnapshot(ctx, input)
	return summary, names, err
}

func (s *Service) intakeCapabilitySnapshot(ctx context.Context, input *QueryInput) (string, []string, []string, error) {
	var toolItems, skillItems []intakeCapability
	var toolNames []string
	if s == nil || input == nil || input.Agent == nil {
		return "", nil, nil, nil
	}
	ctx = s.ensureNamespaceSnapshot(ctx)
	snapshot := ctx.Value(namespaceSnapshotKey{}).(*namespaceSnapshot)
	snapshot.load(ctx)
	namespaces := snapshot.items
	// Wait cancels workerCtx; post-discovery validation must keep using ctx.
	group, workerCtx := errgroup.WithContext(ctx)
	if s.registry != nil {
		group.Go(func() error {
			started := time.Now()
			tools, err := s.discoverableToolDefinitions(workerCtx, input)
			debugtrace.Write("agent", "intake_capability_stage", map[string]any{"conversationID": input.ConversationID, "turnID": input.MessageID, "stage": "tools", "elapsedMs": float64(time.Since(started)) / float64(time.Millisecond), "count": len(tools)})
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					code, status := diagnosticRPCFields(err)
					logx.OperationalWarnf("conversation", "intake.capabilities.failed source=tools convo=%q turn=%q errorType=%T rpcCode=%d rpcStatus=%d", input.ConversationID, input.MessageID, err, code, status)
				}
				return err
			}
			scopes := map[string]string{}
			if provider, ok := s.registry.(interface {
				CapabilityCacheScopes(context.Context, []llm.ToolDefinition) map[string]string
			}); ok {
				scopes = provider.CapabilityCacheScopes(workerCtx, tools)
			}
			toolNames = make([]string, 0, len(tools))
			for _, tool := range tools {
				toolNames = append(toolNames, tool.Name)
				toolItems = append(toolItems, intakeCapability{Kind: "tool", Name: tool.Name, Description: tool.Description, CacheScope: scopes[tool.Name], Namespace: capability.ToolNamespace(tool.DiscoveryNamespace, tool.SourceResourceURI, tool.SourceProviderIdentity), Definition: tool})
			}
			return nil
		})
	}
	if s.skillSvc != nil {
		group.Go(func() error {
			started := time.Now()
			skills, err := s.skillSvc.ListVisible(workerCtx, input.Agent)
			debugtrace.Write("agent", "intake_capability_stage", map[string]any{"conversationID": input.ConversationID, "turnID": input.MessageID, "stage": "skills", "elapsedMs": float64(time.Since(started)) / float64(time.Millisecond), "count": len(skills)})
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					code, status := diagnosticRPCFields(err)
					logx.OperationalWarnf("conversation", "intake.capabilities.failed source=skills convo=%q turn=%q errorType=%T rpcCode=%d rpcStatus=%d", input.ConversationID, input.MessageID, err, code, status)
				}
				return err
			}
			for _, skill := range skills {
				name := skill.QualifiedName
				if strings.TrimSpace(name) == "" {
					name = skill.Name
				}
				skillItems = append(skillItems, intakeCapability{Kind: "skill", Name: name, Description: skill.Description, Namespace: capability.SkillNamespace(skill.Namespace, skill.URI), URI: skill.URI, CacheScope: skill.DiscoveryCacheScope, AlwaysInclude: skill.AlwaysInclude, LookupNames: []string{skill.Name, skill.QualifiedName}, ConnectionAlias: skill.ServerID})
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return "", nil, nil, err
	}
	items := append(toolItems, skillItems...)
	if len(items) == 0 {
		return "", toolNames, nil, nil
	}
	sort.Strings(toolNames)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].URI < items[j].URI
	})
	items, ranked, err := s.rankIntakeCapabilities(ctx, input, items, namespaces)
	if err != nil {
		return "", nil, nil, err
	}
	if ranked {
		toolNames = make([]string, 0)
		for _, item := range items {
			if item.Kind == "tool" {
				toolNames = append(toolNames, item.Name)
			}
		}
		sort.Strings(toolNames)
		encoded, hints := s.rankedIntakeMetadata(ctx, input, items, namespaces)
		raw, err := json.Marshal(encoded)
		return string(raw), toolNames, hints, err
	}
	var namespaceNames []string
	var encodedItems []interface{}
	if len(namespaces) > 0 {
		retained := s.retainedNamespaceDiscovery(ctx, input)
		cards := map[string]resourcesvc.DiscoveredNamespace{}
		for _, item := range items {
			namespace := ""
			if item.Kind == "tool" && retained["mcp"] && !retainedToolDefinition(item.Definition) {
				namespace = item.Definition.DiscoveryNamespace
			}
			if item.Kind == "skill" && retained["skill"] && !item.AlwaysInclude {
				uri, err := identity.ParseResourceURI(item.URI)
				if err == nil {
					namespace = describedSkillNamespace(item.URI, item.ConnectionAlias, snapshot.byName[uri.Namespace])
				}
			}
			var descriptor *resourcesvc.DiscoveredNamespace
			if namespace != "" {
				descriptor = describedNamespaceDescriptor(namespace, snapshot.byName[namespace])
			}
			if descriptor != nil {
				cards[namespace] = *descriptor
			} else {
				encodedItems = append(encodedItems, item)
			}
		}
		for namespace := range cards {
			namespaceNames = append(namespaceNames, namespace)
		}
		sort.Strings(namespaceNames)
		for _, namespace := range namespaceNames {
			descriptor := cards[namespace]
			encodedItems = append(encodedItems, namespaceCapability{Kind: "namespace", Name: namespace, Description: descriptor.Description, UseCases: descriptor.UseCases, SupportedKinds: descriptor.Kinds})
		}
	} else {
		for _, item := range items {
			encodedItems = append(encodedItems, item)
		}
	}
	sort.Strings(namespaceNames)
	raw, err := json.Marshal(encodedItems)
	return string(raw), toolNames, namespaceNames, err
}
