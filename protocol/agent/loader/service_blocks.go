package loader

import (
	"fmt"
	"sort"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/logx"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	yml "github.com/viant/agently-core/workspace/service/meta/yml"
	"gopkg.in/yaml.v3"
)

func (s *Service) parseToolBlock(valueNode *yml.Node, agent *agentmdl.Agent) error {
	if valueNode.Kind != yaml.SequenceNode {
		return fmt.Errorf("tool must be a sequence")
	}
	for _, item := range valueNode.Content {
		if item == nil {
			continue
		}
		switch item.Kind {
		case yaml.ScalarNode:
			v := strings.TrimSpace(item.Value)
			if v != "" {
				agent.Tool.Items = append(agent.Tool.Items, &llm.Tool{Name: v})
			}
		case yaml.MappingNode:
			var t llm.Tool
			var inlineDef llm.ToolDefinition
			var hasInlineDef bool
			if err := (*yml.Node)(item).Pairs(func(k string, v *yml.Node) error {
				lk := strings.ToLower(strings.TrimSpace(k))
				switch lk {
				case "name":
					if v.Kind == yaml.ScalarNode {
						t.Name = strings.TrimSpace(v.Value)
					}
				case "pattern": // backward compat: copy to Name
					if v.Kind == yaml.ScalarNode {
						logx.Warnf("agent-loader", "legacy tool.items.pattern is deprecated; prefer tool.bundles or tool.items.name pattern=%q", strings.TrimSpace(v.Value))
						if t.Name == "" {
							t.Name = strings.TrimSpace(v.Value)
						}
					}
				case "ref": // backward compat: copy to Name
					if v.Kind == yaml.ScalarNode {
						logx.Warnf("agent-loader", "legacy tool.items.ref is deprecated; prefer tool.bundles or tool.items.name ref=%q", strings.TrimSpace(v.Value))
						if t.Name == "" {
							t.Name = strings.TrimSpace(v.Value)
						}
					}
				case "type":
					if v.Kind == yaml.ScalarNode {
						t.Type = strings.TrimSpace(v.Value)
					}
				case "definition":
					if v.Kind == yaml.MappingNode {
						if err := (*yaml.Node)(v).Decode(&t.Definition); err != nil {
							return fmt.Errorf("invalid tool.definition: %w", err)
						}
					}
				case "description":
					if v.Kind == yaml.ScalarNode {
						inlineDef.Description = v.Value
						hasInlineDef = true
					}
				case "parameters":
					if v.Kind == yaml.MappingNode {
						var m map[string]interface{}
						if err := (*yaml.Node)(v).Decode(&m); err != nil {
							return fmt.Errorf("invalid tool.parameters: %w", err)
						}
						inlineDef.Parameters = m
						hasInlineDef = true
					}
				case "required":
					if v.Kind == yaml.SequenceNode {
						var req []string
						if err := (*yaml.Node)(v).Decode(&req); err != nil {
							return fmt.Errorf("invalid tool.required: %w", err)
						}
						inlineDef.Required = req
						hasInlineDef = true
					}
				case "output_schema", "outputschema":
					if v.Kind == yaml.MappingNode {
						var m map[string]interface{}
						if err := (*yaml.Node)(v).Decode(&m); err != nil {
							return fmt.Errorf("invalid tool.output_schema: %w", err)
						}
						inlineDef.OutputSchema = m
						hasInlineDef = true
					}
				case "approvalqueue": // deprecated: approvalQueue on items is no longer supported; use bundle rules
				}
				return nil
			}); err != nil {
				return err
			}
			if hasInlineDef {
				inlineDef.Normalize()
				t.Definition = inlineDef
				if t.Type == "" {
					t.Type = "function"
				}
			}
			if t.Definition.Name != "" || t.Name != "" {
				agent.Tool.Items = append(agent.Tool.Items, &t)
			}
		}
	}
	return nil
}

// parseToolConfig parses the new tool mapping contract.
func (s *Service) parseToolConfig(valueNode *yml.Node, agent *agentmdl.Agent) error {
	if valueNode.Kind != yaml.MappingNode {
		return fmt.Errorf("tool must be a mapping")
	}
	var cfg agentmdl.Tool
	var itemsNode *yml.Node
	if err := valueNode.Pairs(func(k string, v *yml.Node) error {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "items":
			itemsNode = v
		case "bundles", "bundle", "toolsets", "toolset", "connectors", "connector":
			switch v.Kind {
			case yaml.ScalarNode:
				if id := strings.TrimSpace(v.Value); id != "" {
					cfg.Bundles = append(cfg.Bundles, id)
				}
			case yaml.SequenceNode:
				cfg.Bundles = append(cfg.Bundles, asStrings(v)...)
			}
		case "toolcallexposure", "toolexposure", "callexposure":
			if v.Kind == yaml.ScalarNode {
				exp := agentmdl.ToolCallExposure(strings.ToLower(strings.TrimSpace(v.Value)))
				cfg.CallExposure = exp
				agent.Tool.CallExposure = exp
				agent.ToolCallExposure = exp
			}
		case "allowoverflowhelpers":
			if v.Kind != yaml.ScalarNode {
				return fmt.Errorf("tool.allowOverflowHelpers must be a scalar")
			}
			val := toBool(v.Value)
			cfg.AllowOverflowHelpers = &val
		}
		return nil
	}); err != nil {
		return err
	}
	if itemsNode != nil {
		if err := s.parseToolBlock(itemsNode, agent); err != nil {
			return err
		}
		cfg.Items = agent.Tool.Items
	}
	cfg.Bundles = normalizeStrings(cfg.Bundles)
	agent.Tool = cfg
	return nil
}

func (s *Service) parseCapabilitiesBlock(valueNode *yml.Node, agent *agentmdl.Agent) error {
	if valueNode.Kind != yaml.MappingNode {
		return fmt.Errorf("capabilities must be a mapping")
	}
	cfg := &agentmdl.Capabilities{}
	if err := valueNode.Pairs(func(k string, v *yml.Node) error {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "modelartifactgeneration":
			if v.Kind != yaml.ScalarNode {
				return fmt.Errorf("capabilities.modelArtifactGeneration must be a scalar")
			}
			cfg.ModelArtifactGeneration = toBool(v.Value)
		default:
			return fmt.Errorf("unsupported capabilities key: %s", k)
		}
		return nil
	}); err != nil {
		return err
	}
	if !cfg.ModelArtifactGeneration {
		agent.Capabilities = nil
		return nil
	}
	agent.Capabilities = cfg
	return nil
}

func normalizeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func (s *Service) parseProfileBlock(valueNode *yml.Node, agent *agentmdl.Agent) error {
	if valueNode.Kind != yaml.MappingNode {
		return fmt.Errorf("profile must be a mapping")
	}
	prof := &agentmdl.Profile{}
	if err := valueNode.Pairs(func(k string, v *yml.Node) error {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "publish":
			if v.Kind == yaml.ScalarNode {
				prof.Publish = toBool(v.Value)
			}
		case "name":
			if v.Kind == yaml.ScalarNode {
				prof.Name = v.Value
			}
		case "description":
			if v.Kind == yaml.ScalarNode {
				prof.Description = v.Value
			}
		case "tags":
			if v != nil {
				prof.Tags = asStrings(v)
			}
		case "rank":
			if v.Kind == yaml.ScalarNode {
				val := v.Interface()
				switch actual := val.(type) {
				case int:
					prof.Rank = actual
				case int64:
					prof.Rank = int(actual)
				case float64:
					prof.Rank = int(actual)
				case string:
					if n, err := parseInt64(actual); err == nil {
						prof.Rank = int(n)
					}
				}
			}
		case "capabilities":
			if v.Kind == yaml.MappingNode {
				var m map[string]interface{}
				_ = (*yaml.Node)(v).Decode(&m)
				prof.Capabilities = m
			}
		case "responsibilities":
			if v != nil {
				prof.Responsibilities = asStrings(v)
			}
		case "inscope":
			if v != nil {
				prof.InScope = asStrings(v)
			}
		case "conversationscope":
			if v != nil {
				prof.ConversationScope = v.Value
			}
		case "outofscope":
			if v != nil {
				prof.OutOfScope = asStrings(v)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	agent.Profile = prof
	return nil
}

func (s *Service) parseStarterTasksBlock(valueNode *yml.Node, agent *agentmdl.Agent) error {
	if valueNode.Kind != yaml.SequenceNode {
		return fmt.Errorf("starterTasks must be a sequence")
	}
	var tasks []agentmdl.StarterTask
	if err := (*yaml.Node)(valueNode).Decode(&tasks); err != nil {
		return fmt.Errorf("invalid starterTasks definition: %w", err)
	}
	agent.StarterTasks = append([]agentmdl.StarterTask(nil), tasks...)
	return nil
}

func (s *Service) parseServeBlock(valueNode *yml.Node, agent *agentmdl.Agent) error {
	if valueNode.Kind != yaml.MappingNode {
		return fmt.Errorf("serve must be a mapping")
	}
	var srv agentmdl.Serve
	if err := valueNode.Pairs(func(k string, v *yml.Node) error {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "a2a":
			if v.Kind != yaml.MappingNode {
				return fmt.Errorf("serve.a2a must be a mapping")
			}
			a2a := &agentmdl.ServeA2A{}
			if err := v.Pairs(func(ak string, av *yml.Node) error {
				switch strings.ToLower(strings.TrimSpace(ak)) {
				case "enabled":
					if av.Kind == yaml.ScalarNode {
						a2a.Enabled = toBool(av.Value)
					}
				case "port":
					if av.Kind == yaml.ScalarNode {
						val := av.Interface()
						switch actual := val.(type) {
						case int:
							a2a.Port = actual
						case int64:
							a2a.Port = int(actual)
						case float64:
							a2a.Port = int(actual)
						case string:
							if n, err := parseInt64(actual); err == nil {
								a2a.Port = int(n)
							}
						}
					}
				case "streaming":
					if av.Kind == yaml.ScalarNode {
						a2a.Streaming = toBool(av.Value)
					}
				case "usercredurl":
					if av.Kind == yaml.ScalarNode {
						a2a.UserCredURL = strings.TrimSpace(av.Value)
					}
				case "auth":
					if av.Kind != yaml.MappingNode {
						return fmt.Errorf("serve.a2a.auth must be a mapping")
					}
					a := &agentmdl.A2AAuth{}
					_ = av.Pairs(func(k2 string, v2 *yml.Node) error {
						switch strings.ToLower(strings.TrimSpace(k2)) {
						case "enabled":
							if v2.Kind == yaml.ScalarNode {
								a.Enabled = toBool(v2.Value)
							}
						case "resource":
							if v2.Kind == yaml.ScalarNode {
								a.Resource = v2.Value
							}
						case "scopes":
							a.Scopes = asStrings(v2)
						case "useidtoken":
							if v2.Kind == yaml.ScalarNode {
								a.UseIDToken = toBool(v2.Value)
							}
						case "excludeprefix":
							if v2.Kind == yaml.ScalarNode {
								a.ExcludePrefix = v2.Value
							}
						}
						return nil
					})
					a2a.Auth = a
				}
				return nil
			}); err != nil {
				return err
			}
			srv.A2A = a2a
		}
		return nil
	}); err != nil {
		return err
	}
	agent.Serve = &srv
	return nil
}

func (s *Service) parseExposeA2ABlock(valueNode *yml.Node, agent *agentmdl.Agent) error {
	if valueNode.Kind != yaml.MappingNode {
		return fmt.Errorf("exposeA2A must be a mapping")
	}
	exp := &agentmdl.ExposeA2A{}
	if err := valueNode.Pairs(func(k string, v *yml.Node) error {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "enabled":
			if v.Kind == yaml.ScalarNode {
				exp.Enabled = toBool(v.Value)
			}
		case "port":
			if v.Kind == yaml.ScalarNode {
				val := v.Interface()
				switch actual := val.(type) {
				case int:
					exp.Port = actual
				case int64:
					exp.Port = int(actual)
				case float64:
					exp.Port = int(actual)
				case string:
					if n, err := parseInt64(actual); err == nil {
						exp.Port = int(n)
					}
				}
			}
		case "basepath":
			if v.Kind == yaml.ScalarNode {
				exp.BasePath = strings.TrimSpace(v.Value)
			}
		case "streaming":
			if v.Kind == yaml.ScalarNode {
				exp.Streaming = toBool(v.Value)
			}
		case "auth":
			if v.Kind != yaml.MappingNode {
				return fmt.Errorf("exposeA2A.auth must be a mapping")
			}
			a := &agentmdl.A2AAuth{}
			if err := v.Pairs(func(ak string, av *yml.Node) error {
				switch strings.ToLower(strings.TrimSpace(ak)) {
				case "enabled":
					if av.Kind == yaml.ScalarNode {
						a.Enabled = toBool(av.Value)
					}
				case "resource":
					if av.Kind == yaml.ScalarNode {
						a.Resource = av.Value
					}
				case "scopes":
					if av != nil {
						a.Scopes = asStrings(av)
					}
				case "useidtoken":
					if av.Kind == yaml.ScalarNode {
						a.UseIDToken = toBool(av.Value)
					}
				case "excludeprefix":
					if av.Kind == yaml.ScalarNode {
						a.ExcludePrefix = av.Value
					}
				}
				return nil
			}); err != nil {
				return err
			}
			exp.Auth = a
		}
		return nil
	}); err != nil {
		return err
	}
	agent.ExposeA2A = exp
	if agent.Serve == nil {
		agent.Serve = &agentmdl.Serve{}
	}
	agent.Serve.A2A = &agentmdl.ServeA2A{Enabled: exp.Enabled, Port: exp.Port, Streaming: exp.Streaming, Auth: exp.Auth}
	return nil
}

func (s *Service) parseAttachmentBlock(valueNode *yml.Node, agent *agentmdl.Agent) error {
	if valueNode.Kind != yaml.MappingNode {
		return fmt.Errorf("attachment must be a mapping")
	}
	var cfg agentmdl.Attachment
	if err := valueNode.Pairs(func(k string, v *yml.Node) error {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "limit", "limitbytes", "limitbytesmb", "limitmb":
			if v.Kind == yaml.ScalarNode {
				val := v.Interface()
				switch a := val.(type) {
				case int:
					cfg.LimitBytes = int64(a)
					if strings.Contains(strings.ToLower(k), "mb") {
						cfg.LimitBytes *= 1024 * 1024
					}
				case int64:
					cfg.LimitBytes = a
					if strings.Contains(strings.ToLower(k), "mb") {
						cfg.LimitBytes *= 1024 * 1024
					}
				case float64:
					cfg.LimitBytes = int64(a)
					if strings.Contains(strings.ToLower(k), "mb") {
						cfg.LimitBytes *= 1024 * 1024
					}
				case string:
					lv := strings.TrimSpace(strings.ToLower(a))
					mul := int64(1)
					if strings.HasSuffix(lv, "mb") {
						lv = strings.TrimSuffix(lv, "mb")
						mul = 1024 * 1024
					}
					if n, err := parseInt64(lv); err == nil {
						cfg.LimitBytes = n * mul
					}
				}
			}
		case "mode":
			if v.Kind == yaml.ScalarNode {
				m := strings.ToLower(strings.TrimSpace(v.Value))
				if m != "inline" {
					m = "ref"
				}
				cfg.Mode = m
			}
		case "ttlsec":
			if v.Kind == yaml.ScalarNode {
				val := v.Interface()
				switch a := val.(type) {
				case int:
					cfg.TTLSec = int64(a)
				case int64:
					cfg.TTLSec = a
				case float64:
					cfg.TTLSec = int64(a)
				case string:
					if n, err := parseInt64(a); err == nil {
						cfg.TTLSec = n
					}
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	agent.Attachment = &cfg
	return nil
}

func (s *Service) parseFollowUpsBlock(valueNode *yml.Node, agent *agentmdl.Agent) error {
	if valueNode.Kind != yaml.SequenceNode {
		return fmt.Errorf("followUps must be a sequence")
	}
	for _, item := range valueNode.Content {
		if item == nil || item.Kind != yaml.MappingNode {
			return fmt.Errorf("invalid followUp entry; expected mapping")
		}
		var c agentmdl.Chain
		var whenExpr string
		var disabledOverride *bool
		for i := 0; i+1 < len(item.Content); i += 2 {
			k := strings.ToLower(strings.TrimSpace(item.Content[i].Value))
			if k == "when" {
				v := item.Content[i+1]
				if v != nil && v.Kind == yaml.ScalarNode {
					whenExpr = v.Value
					item.Content[i+1] = &yaml.Node{Kind: yaml.MappingNode}
				}
				break
			}
			if k == "disabled" {
				v := item.Content[i+1]
				if v != nil && v.Kind == yaml.ScalarNode {
					b := toBool(v.Value)
					disabledOverride = &b
				}
			}
		}
		if err := (*yaml.Node)(item).Decode(&c); err != nil {
			return fmt.Errorf("invalid followUp definition: %w", err)
		}
		if c.Query == nil {
			for i := 0; i+1 < len(item.Content); i += 2 {
				k := strings.ToLower(strings.TrimSpace(item.Content[i].Value))
				if k == "query" {
					v := item.Content[i+1]
					if v.Kind == yaml.ScalarNode {
						c.Query = &binding.Prompt{Text: v.Value}
					}
					break
				}
			}
		}
		if strings.TrimSpace(whenExpr) != "" {
			if c.When == nil {
				c.When = &agentmdl.WhenSpec{Expr: whenExpr}
			} else if strings.TrimSpace(c.When.Expr) == "" && c.When.Query == nil && c.When.Expect == nil && strings.TrimSpace(c.When.Model) == "" {
				c.When.Expr = whenExpr
			}
		}
		if strings.TrimSpace(c.Conversation) == "" {
			c.Conversation = "link"
		}
		switch strings.ToLower(strings.TrimSpace(c.Conversation)) {
		case "reuse", "link", "":
		default:
			return fmt.Errorf("invalid chain.conversation: %s", c.Conversation)
		}
		if disabledOverride != nil {
			c.Disabled = *disabledOverride
		}
		agent.FollowUps = append(agent.FollowUps, &c)
	}
	return nil
}
