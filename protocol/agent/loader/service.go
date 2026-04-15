package loader

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/afs/file"
	"github.com/viant/afs/url"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/shared"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/prompt"
	"github.com/viant/agently-core/workspace"
	meta "github.com/viant/agently-core/workspace/service/meta"
	yml "github.com/viant/agently-core/workspace/service/meta/yml"

	"gopkg.in/yaml.v3"
)

// Ensure Service implements interfaces.Loader so that changes are caught by
// the compiler.
var _ agentmdl.Loader = (*Service)(nil)

const (
	defaultExtension = ".yaml"
)

// Service provides agent data access operations
type Service struct {
	metaService *meta.Service
	agents      shared.Map[string, *agentmdl.Agent] //[ url ] -> [ agent]

	defaultExtension string
}

// LoadAgents loads an agents from the specified URL
func (s *Service) LoadAgents(ctx context.Context, URL string) ([]*agentmdl.Agent, error) {
	candidates, err := s.metaService.List(ctx, URL)
	if err != nil {
		return nil, fmt.Errorf("failed to list agent from %s: %w", URL, err)
	}
	var result []*agentmdl.Agent
	for _, candidate := range candidates {
		anAgent, err := s.Load(ctx, candidate)
		if err != nil {
			return nil, fmt.Errorf("failed to load agent from %s: %w", candidate, err)
		}
		result = append(result, anAgent)
	}
	return result, nil

}

func (s *Service) List() []*agentmdl.Agent {
	result := make([]*agentmdl.Agent, 0)
	s.agents.Range(
		func(key string, value *agentmdl.Agent) bool {
			result = append(result, value)
			return true
		})
	return result
}

// Add adds an agent to the service
func (s *Service) Add(name string, agent *agentmdl.Agent) {
	s.agents.Set(name, agent)
}

// Lookup looks up an agent by name
func (s *Service) Lookup(ctx context.Context, name string) (*agentmdl.Agent, error) {
	anAgent, ok := s.agents.Get(name)
	if !ok {
		return nil, fmt.Errorf("agent %q not found", name)
	}
	return anAgent, nil
}

// Load loads an agent from the specified URL
func (s *Service) Load(ctx context.Context, nameOrLocation string) (*agentmdl.Agent, error) {
	// Resolve relative name (e.g. "chat") to a workspace file path.
	// All other workspace kinds store definitions flat as
	//   <kind>/<name>.yaml
	// so we keep the same convention for agents instead of the previous
	// nested  <kind>/<name>/<name>.yaml layout.
	URL := nameOrLocation
	if !strings.Contains(URL, "/") && filepath.Ext(nameOrLocation) == "" {
		URL = filepath.Join(workspace.KindAgent, nameOrLocation)
	}

	if url.IsRelative(URL) {
		ext := ""
		if filepath.Ext(URL) == "" {
			ext = s.defaultExtension
		}
		if ok, _ := s.metaService.Exists(ctx, URL+ext); ok {
			// Keep relative URL; metaService will resolve against its base.
			URL = URL + ext
		} else {
			candidate := path.Join(URL, nameOrLocation)
			if ok, _ = s.metaService.Exists(ctx, candidate+ext); ok {
				URL = candidate + ext
			}
		}
	}

	var node yaml.Node
	if err := s.metaService.Load(ctx, URL, &node); err != nil {
		return nil, fmt.Errorf("failed to load agent from %s: %w", URL, err)
	}

	anAgent := &agentmdl.Agent{Source: &agentmdl.Source{URL: s.metaService.GetURL(URL)}}
	// Parse the YAML into our agent model
	if err := s.parseAgent((*yml.Node)(&node), anAgent); err != nil {
		return nil, fmt.Errorf("failed to parse agent from %s: %w", URL, err)
	}
	// Normalize parsed agent prior to validation
	normalizeAgent(anAgent)
	if err := anAgent.Validate(); err != nil {
		return nil, fmt.Errorf("invalid agent %s: %w", URL, err)
	}

	// Set agent name based on URL if not set
	if anAgent.Name == "" {
		anAgent.Name = getAgentNameFromURL(URL)
	}

	srcURL := anAgent.Source.URL
	for i := range anAgent.Knowledge {
		knowledge := anAgent.Knowledge[i]
		if knowledge.URL == "" {
			return nil, fmt.Errorf("agent %v knowledge URL is empty", anAgent.Name)
		}
		// If URL has no scheme: if it’s absolute (drive/UNC/POSIX), convert to file:// URL.
		// Otherwise resolve relative to agent source URL.
		if url.Scheme(knowledge.URL, "") == "" {
			u := strings.TrimSpace(knowledge.URL)
			if !url.IsRelative(u) { // absolute OS path
				anAgent.Knowledge[i].URL = url.ToFileURL(u)
			} else if url.IsRelative(u) && !url.IsRelative(srcURL) {
				parentURL, _ := url.Split(srcURL, file.Scheme)
				anAgent.Knowledge[i].URL = url.JoinUNC(parentURL, u)
			}
		}
		// Validate that knowledge path exists
		if ok, _ := s.metaService.Exists(ctx, anAgent.Knowledge[i].URL); !ok {
			return nil, fmt.Errorf("agent %v knowledge path does not exist: %s", anAgent.Name, anAgent.Knowledge[i].URL)
		}
	}

	for i := range anAgent.SystemKnowledge {
		knowledge := anAgent.SystemKnowledge[i]
		if knowledge.URL == "" {
			return nil, fmt.Errorf("agent %v system knowledge URL is empty", anAgent.Name)
		}
		if url.Scheme(knowledge.URL, "") == "" {
			u := strings.TrimSpace(knowledge.URL)
			if !url.IsRelative(u) {
				anAgent.SystemKnowledge[i].URL = url.ToFileURL(u)
			} else if url.IsRelative(u) && !url.IsRelative(srcURL) {
				parentURL, _ := url.Split(srcURL, file.Scheme)
				anAgent.SystemKnowledge[i].URL = url.JoinUNC(parentURL, u)
			}
		}
		// Validate that system knowledge path exists
		if ok, _ := s.metaService.Exists(ctx, anAgent.SystemKnowledge[i].URL); !ok {
			return nil, fmt.Errorf("agent %v system knowledge path does not exist: %s", anAgent.Name, anAgent.SystemKnowledge[i].URL)
		}
	}

	// Ensure that knowledge and systemKnowledge locations are also reflected as
	// resources with semantic match enabled so they participate in tools such as
	// resources.match and resources.roots.
	augmentResourcesWithKnowledge(anAgent)

	// Resolve relative prompt URIs against the agent source location
	s.resolvePromptURIs(anAgent)

	// Validate agent
	if err := anAgent.Validate(); err != nil {
		return nil, fmt.Errorf("invalid agent configuration from %s: %w", URL, err)
	}

	s.agents.Set(anAgent.Name, anAgent)
	return anAgent, nil
}

// (drive/UNC detection is handled by afs/url.IsRelative and ToFileURL)

// parseAgent parses agent properties from a YAML node
func (s *Service) parseAgent(node *yml.Node, agent *agentmdl.Agent) error {
	rootNode := node
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		rootNode = (*yml.Node)(node.Content[0])
	}

	// Look for the "agent" root node
	var agentNode *yml.Node
	err := rootNode.Pairs(func(key string, valueNode *yml.Node) error {
		if strings.ToLower(key) == "agent" && valueNode.Kind == yaml.MappingNode {
			agentNode = valueNode
			return nil
		}
		return nil
	})

	if err != nil {
		return err
	}
	if agentNode == nil {
		agentNode = rootNode // Use the root node if no "agent" node is found
	}

	// Parse agent properties
	return agentNode.Pairs(func(key string, valueNode *yml.Node) error {
		lowerKey := strings.ToLower(key)
		if handled, err := s.parseAgentBasicScalar(agent, lowerKey, valueNode); err != nil {
			return err
		} else if handled {
			return nil
		}
		switch lowerKey {
		case "capabilities":
			return s.parseCapabilitiesBlock(valueNode, agent)
		case "internal":
			if valueNode.Kind == yaml.ScalarNode {
				v := valueNode.Interface()
				switch actual := v.(type) {
				case bool:
					agent.Internal = actual
				case string:
					s := strings.TrimSpace(actual)
					if s == "" {
						agent.Internal = false
						break
					}
					if strings.EqualFold(s, "true") || s == "1" || strings.EqualFold(s, "yes") {
						agent.Internal = true
					} else if strings.EqualFold(s, "false") || s == "0" || strings.EqualFold(s, "no") {
						agent.Internal = false
					} else {
						return fmt.Errorf("invalid internal value: %q", s)
					}
				default:
					return fmt.Errorf("invalid internal value: %T %v", v, v)
				}
			}
		case "allowedproviders":
			// Accept either a scalar (single provider) or a sequence of scalars.
			switch valueNode.Kind {
			case yaml.ScalarNode:
				v := strings.TrimSpace(valueNode.Value)
				if v != "" {
					agent.AllowedProviders = []string{v}
				}
			case yaml.SequenceNode:
				providers := make([]string, 0, len(valueNode.Content))
				for _, item := range valueNode.Content {
					if item.Kind != yaml.ScalarNode {
						continue
					}
					v := strings.TrimSpace(item.Value)
					if v != "" {
						providers = append(providers, v)
					}
				}
				if len(providers) > 0 {
					agent.AllowedProviders = providers
				}
			}
		case "allowedmodels":
			// Accept either a scalar (single model id) or a sequence of scalars.
			switch valueNode.Kind {
			case yaml.ScalarNode:
				v := strings.TrimSpace(valueNode.Value)
				if v != "" {
					agent.AllowedModels = []string{v}
				}
			case yaml.SequenceNode:
				models := make([]string, 0, len(valueNode.Content))
				for _, item := range valueNode.Content {
					if item.Kind != yaml.ScalarNode {
						continue
					}
					v := strings.TrimSpace(item.Value)
					if v != "" {
						models = append(models, v)
					}
				}
				if len(models) > 0 {
					agent.AllowedModels = models
				}
			}
		case "paralleltoolcalls":
			if valueNode.Kind == yaml.ScalarNode {
				val := valueNode.Interface()
				switch actual := val.(type) {
				case bool:
					boolVal := actual
					agent.ParallelToolCalls = &boolVal
				case string:
					lv := strings.ToLower(strings.TrimSpace(actual))
					boolVal2 := lv == "true"
					agent.ParallelToolCalls = &boolVal2
				}
			}
		case "autosummarize", "autosumarize":
			// Support correct and common misspelling keys. Accept bool or string values.
			if valueNode.Kind == yaml.ScalarNode {
				val := valueNode.Interface()
				switch actual := val.(type) {
				case bool:
					agent.AutoSummarize = &actual
				case string:
					lv := strings.ToLower(strings.TrimSpace(actual))
					v := lv == "true" || lv == "yes" || lv == "on"
					agent.AutoSummarize = &v
				}
			}
		case "showexecutiondetails":
			if valueNode.Kind == yaml.ScalarNode {
				val := strings.ToLower(strings.TrimSpace(valueNode.Value))
				b := val == "true" || val == "yes" || val == "on" || val == "1"
				agent.ShowExecutionDetails = &b
			}
		case "showtoolfeed":
			if valueNode.Kind == yaml.ScalarNode {
				val := strings.ToLower(strings.TrimSpace(valueNode.Value))
				b := val == "true" || val == "yes" || val == "on" || val == "1"
				agent.ShowToolFeed = &b
			}
		case "ringonfinish", "finishring", "notifyonfinish":
			if valueNode.Kind == yaml.ScalarNode {
				val := strings.ToLower(strings.TrimSpace(valueNode.Value))
				agent.RingOnFinish = val == "true" || val == "yes" || val == "on" || val == "1"
			}
		case "delegation":
			switch valueNode.Kind {
			case yaml.ScalarNode:
				v := valueNode.Interface()
				switch actual := v.(type) {
				case bool:
					if agent.Delegation == nil {
						agent.Delegation = &agentmdl.Delegation{}
					}
					agent.Delegation.Enabled = actual
				case string:
					sv := strings.ToLower(strings.TrimSpace(actual))
					if agent.Delegation == nil {
						agent.Delegation = &agentmdl.Delegation{}
					}
					agent.Delegation.Enabled = sv == "true" || sv == "yes" || sv == "on" || sv == "1"
				default:
					return fmt.Errorf("invalid delegation value: %T %v", v, v)
				}
			case yaml.MappingNode:
				if agent.Delegation == nil {
					agent.Delegation = &agentmdl.Delegation{}
				}
				for i := 0; i < len(valueNode.Content); i += 2 {
					k := strings.ToLower(strings.TrimSpace(valueNode.Content[i].Value))
					vn := (*yml.Node)(valueNode.Content[i+1])
					switch k {
					case "enabled":
						if vn.Kind == yaml.ScalarNode {
							v := vn.Interface()
							switch actual := v.(type) {
							case bool:
								agent.Delegation.Enabled = actual
							case string:
								sv := strings.ToLower(strings.TrimSpace(actual))
								agent.Delegation.Enabled = sv == "true" || sv == "yes" || sv == "on" || sv == "1"
							default:
								return fmt.Errorf("invalid delegation.enabled value: %T %v", v, v)
							}
						}
					case "maxdepth":
						if vn.Kind == yaml.ScalarNode {
							v := vn.Interface()
							switch actual := v.(type) {
							case int:
								agent.Delegation.MaxDepth = actual
							case int64:
								agent.Delegation.MaxDepth = int(actual)
							case float64:
								agent.Delegation.MaxDepth = int(actual)
							case string:
								sv := strings.TrimSpace(actual)
								if sv == "" {
									agent.Delegation.MaxDepth = 0
									break
								}
								n, err := strconv.Atoi(sv)
								if err != nil {
									return fmt.Errorf("invalid delegation.maxDepth value: %q", sv)
								}
								agent.Delegation.MaxDepth = n
							default:
								return fmt.Errorf("invalid delegation.maxDepth value: %T %v", v, v)
							}
						}
					}
				}
			}
		case "knowledge":
			if err := s.parseKnowledgeBlock(valueNode, agent); err != nil {
				return err
			}
		case "systemknowledge":
			if err := s.parseSystemKnowledgeBlock(valueNode, agent); err != nil {
				return err
			}
		case "resources":
			if err := s.parseResourcesBlock(valueNode, agent); err != nil {
				return err
			}
		case "prompt":
			if agent.Prompt, err = s.getPrompt(valueNode); err != nil {
				return err
			}

		case "systemprompt":
			if agent.SystemPrompt, err = s.getPrompt(valueNode); err != nil {
				return err
			}
		case "instructionprompt":
			if agent.InstructionPrompt, err = s.getPrompt(valueNode); err != nil {
				return err
			}
		case "instruction":
			if agent.Instruction, err = s.getPrompt(valueNode); err != nil {
				return err
			}

		case "tool":
			switch valueNode.Kind {
			case yaml.SequenceNode:
				// Legacy format: tool: [ ... ]
				if err := s.parseToolBlock(valueNode, agent); err != nil {
					return err
				}
			case yaml.MappingNode:
				// New format: tool: { items: [], toolCallExposure: "..." }
				if err := s.parseToolConfig(valueNode, agent); err != nil {
					return err
				}
			default:
				return fmt.Errorf("invalid tool block; expected sequence or mapping")
			}

		case "template":
			if valueNode.Kind != yaml.MappingNode {
				return fmt.Errorf("template must be a mapping")
			}
			if err := valueNode.Pairs(func(k string, v *yml.Node) error {
				switch strings.ToLower(strings.TrimSpace(k)) {
				case "bundles", "bundle":
					switch v.Kind {
					case yaml.ScalarNode:
						if id := strings.TrimSpace(v.Value); id != "" {
							agent.Template.Bundles = append(agent.Template.Bundles, id)
						}
					case yaml.SequenceNode:
						agent.Template.Bundles = append(agent.Template.Bundles, asStrings(v)...)
					}
				}
				return nil
			}); err != nil {
				return err
			}
			agent.Template.Bundles = normalizeStrings(agent.Template.Bundles)

		case "profile":
			if err := s.parseProfileBlock(valueNode, agent); err != nil {
				return err
			}

		case "startertasks":
			if err := s.parseStarterTasksBlock(valueNode, agent); err != nil {
				return err
			}

		case "serve":
			if err := s.parseServeBlock(valueNode, agent); err != nil {
				return err
			}

		case "exposea2a":
			if err := s.parseExposeA2ABlock(valueNode, agent); err != nil {
				return err
			}
		case "persona":
			if valueNode.Kind == yaml.MappingNode {
				var p prompt.Persona
				if err := (*yaml.Node)(valueNode).Decode(&p); err != nil {
					return fmt.Errorf("invalid persona definition: %w", err)
				}
				agent.Persona = &p
			}
		case "reasoning":
			if valueNode.Kind == yaml.MappingNode {
				var r llm.Reasoning
				if err := (*yaml.Node)(valueNode).Decode(&r); err != nil {
					return fmt.Errorf("invalid reasoning definition: %w", err)
				}
				agent.Reasoning = &r
			}
		case "callexposure", "toolcallexposure":
			// Accept scalar values: turn | conversation | semantic
			if valueNode.Kind == yaml.ScalarNode {
				exp := agentmdl.ToolCallExposure(strings.ToLower(strings.TrimSpace(valueNode.Value)))
				agent.Tool.CallExposure = exp
				agent.ToolCallExposure = exp
			}
		case "attachment":
			if err := s.parseAttachmentBlock(valueNode, agent); err != nil {
				return err
			}
		case "followups":
			if err := s.parseFollowUpsBlock(valueNode, agent); err != nil {
				return err
			}
			// mcpresources: removed; use generic resources instead
		}
		return nil
	})

}

func (s *Service) parseKnowledgeBlock(valueNode *yml.Node, agent *agentmdl.Agent) error {
	if valueNode.Kind != yaml.SequenceNode {
		return nil
	}
	for _, itemNode := range valueNode.Content {
		knowledge, err := parseKnowledge((*yml.Node)(itemNode))
		if err != nil {
			return err
		}
		agent.Knowledge = append(agent.Knowledge, knowledge)
	}
	return nil
}

func (s *Service) parseSystemKnowledgeBlock(valueNode *yml.Node, agent *agentmdl.Agent) error {
	if valueNode.Kind != yaml.SequenceNode {
		return nil
	}
	for _, itemNode := range valueNode.Content {
		knowledge, err := parseKnowledge((*yml.Node)(itemNode))
		if err != nil {
			return err
		}
		agent.SystemKnowledge = append(agent.SystemKnowledge, knowledge)
	}
	return nil
}

// parseMCPResourcesBlock removed — agent.resources replaces it.

// New creates a new agent service instance
func New(opts ...Option) *Service {
	ret := &Service{
		metaService:      meta.New(afs.New(), ""),
		defaultExtension: defaultExtension,
		agents:           shared.NewMap[string, *agentmdl.Agent](),
	}
	for _, opt := range opts {
		opt(ret)
	}
	return ret
}
