package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agentmdl "github.com/viant/agently-core/protocol/agent"
	yml "github.com/viant/agently-core/workspace/service/meta/yml"
	"github.com/viant/embedius/matching/option"
	embcfg "github.com/viant/embedius/service"
	"gopkg.in/yaml.v3"
)

// parseResourcesBlock parses agent.resources entries (list of mappings)
func (s *Service) parseResourcesBlock(valueNode *yml.Node, agent *agentmdl.Agent) error {
	switch valueNode.Kind {
	case yaml.SequenceNode:
		for _, it := range valueNode.Content {
			if it == nil || it.Kind != yaml.MappingNode {
				continue
			}
			if entries, handled, err := parseEmbediusResourcesEntry((*yml.Node)(it)); err != nil {
				return err
			} else if handled {
				agent.Resources = append(agent.Resources, entries...)
				continue
			}
			entry, err := parseResourceEntry((*yml.Node)(it))
			if err != nil {
				return err
			}
			if entry != nil {
				agent.Resources = append(agent.Resources, entry)
			}
		}
	case yaml.MappingNode:
		if entries, handled, err := parseEmbediusResourcesEntry((*yml.Node)(valueNode)); err != nil {
			return err
		} else if handled {
			agent.Resources = append(agent.Resources, entries...)
			return nil
		}
		entry, err := parseResourceEntry((*yml.Node)(valueNode))
		if err != nil {
			return err
		}
		if entry != nil {
			agent.Resources = append(agent.Resources, entry)
		}
	default:
		return fmt.Errorf("resources must be a sequence or mapping")
	}
	return nil
}

func parseEmbediusResourcesEntry(node *yml.Node) ([]*agentmdl.Resource, bool, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, false, nil
	}
	var embNode *yml.Node
	_ = node.Pairs(func(key string, v *yml.Node) error {
		if strings.EqualFold(strings.TrimSpace(key), "embedius") {
			embNode = v
		}
		return nil
	})
	if embNode == nil {
		return nil, false, nil
	}
	if embNode.Kind != yaml.MappingNode {
		return nil, true, fmt.Errorf("embedius entry must be a mapping")
	}
	type embSpec struct {
		Config             string
		Roots              []string
		AllowSemanticMatch *bool
		AllowGrep          *bool
		Role               string
	}
	spec := embSpec{Config: "~/embedius/config.yaml", Role: "user"}
	roleExplicit := false
	systemFlagSet := false
	systemRole := "user"
	err := embNode.Pairs(func(key string, v *yml.Node) error {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "config":
			if v.Kind == yaml.ScalarNode {
				spec.Config = strings.TrimSpace(v.Value)
			}
		case "roots":
			spec.Roots = asStringList(v)
		case "role":
			if v.Kind == yaml.ScalarNode {
				spec.Role = strings.ToLower(strings.TrimSpace(v.Value))
				roleExplicit = true
			}
		case "system":
			enabled, handled, err := parseSystemFlag(v)
			if err != nil {
				return err
			}
			if handled {
				systemFlagSet = true
				if enabled {
					systemRole = "system"
				} else {
					systemRole = "user"
				}
			}
		case "allowsemanticmatch":
			if v.Kind == yaml.ScalarNode {
				b := toBool(v.Value)
				spec.AllowSemanticMatch = &b
			}
		case "allowgrep":
			if v.Kind == yaml.ScalarNode {
				b := toBool(v.Value)
				spec.AllowGrep = &b
			}
		}
		return nil
	})
	if err != nil {
		return nil, true, err
	}
	if systemFlagSet {
		if !roleExplicit {
			spec.Role = systemRole
		} else if !strings.EqualFold(strings.TrimSpace(spec.Role), systemRole) {
			return nil, true, fmt.Errorf("embedius entry role %q conflicts with system=%v", spec.Role, systemRole == "system")
		}
	}
	cfgPath := expandUserHome(strings.TrimSpace(spec.Config))
	if cfgPath == "" {
		cfgPath = expandUserHome("~/embedius/config.yaml")
	}
	cfg, err := embcfg.LoadConfig(cfgPath)
	if err != nil {
		return nil, true, err
	}
	rootsFilter := normalizeSelectors(spec.Roots)
	var ids []string
	for id := range cfg.Roots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []*agentmdl.Resource
	for _, id := range ids {
		root := cfg.Roots[id]
		if !matchesSelector(id, root.Path, rootsFilter) {
			continue
		}
		res := &agentmdl.Resource{
			ID:          strings.TrimSpace(id),
			URI:         strings.TrimSpace(root.Path),
			Role:        spec.Role,
			Description: strings.TrimSpace(root.Description),
			DB: func() string {
				return strings.TrimSpace(cfg.Store.DSN)
			}(),
		}
		if len(root.Include) > 0 || len(root.Exclude) > 0 || root.MaxSizeBytes > 0 {
			res.Match = &option.Options{
				Inclusions: append([]string(nil), root.Include...),
				Exclusions: append([]string(nil), root.Exclude...),
				MaxFileSize: func() int {
					if root.MaxSizeBytes <= 0 {
						return 0
					}
					return int(root.MaxSizeBytes)
				}(),
			}
		}
		if spec.AllowSemanticMatch != nil {
			res.AllowSemanticMatch = spec.AllowSemanticMatch
		}
		if spec.AllowGrep != nil {
			res.AllowGrep = spec.AllowGrep
		}
		res.URI = expandUserHome(res.URI)
		res.DB = expandUserHome(res.DB)
		if strings.TrimSpace(res.URI) == "" {
			continue
		}
		out = append(out, res)
	}
	return out, true, nil
}

func normalizeSelectors(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		out = append(out, strings.ToLower(v))
	}
	return out
}

func matchesSelector(id, path string, selectors []string) bool {
	if len(selectors) == 0 {
		return true
	}
	id = strings.ToLower(strings.TrimSpace(id))
	path = strings.ToLower(strings.TrimSpace(path))
	for _, sel := range selectors {
		if sel == "*" || sel == id || sel == path {
			return true
		}
	}
	return false
}

func parseResourceEntry(node *yml.Node) (*agentmdl.Resource, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("resource entry must be a mapping")
	}
	re := &agentmdl.Resource{Role: "user"}
	roleExplicit := false
	systemFlagSet := false
	systemRole := "user"
	hasMCP := false
	hasURI := false
	err := node.Pairs(func(key string, v *yml.Node) error {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "id":
			if v.Kind == yaml.ScalarNode {
				re.ID = strings.TrimSpace(v.Value)
			}
		case "uri":
			if v.Kind == yaml.ScalarNode {
				re.URI = strings.TrimSpace(v.Value)
				if re.URI != "" {
					hasURI = true
				}
			}
		case "mcp":
			if v.Kind == yaml.ScalarNode {
				re.MCP = strings.TrimSpace(v.Value)
				if re.MCP != "" {
					hasMCP = true
				}
			}
		case "roots":
			re.Roots = asStringList(v)
		case "role":
			if v.Kind == yaml.ScalarNode {
				re.Role = strings.ToLower(strings.TrimSpace(v.Value))
				roleExplicit = true
			}
		case "system":
			enabled, handled, err := parseSystemFlag(v)
			if err != nil {
				return err
			}
			if handled {
				systemFlagSet = true
				if enabled {
					systemRole = "system"
				} else {
					systemRole = "user"
				}
			}
		case "binding":
			if v.Kind == yaml.ScalarNode {
				re.Binding = toBool(v.Value)
			}
		case "description":
			if v.Kind == yaml.ScalarNode {
				re.Description = strings.TrimSpace(v.Value)
			}
		case "allowsemanticmatch":
			if v.Kind == yaml.ScalarNode {
				b := toBool(v.Value)
				re.AllowSemanticMatch = &b
			}
		case "allowgrep":
			if v.Kind == yaml.ScalarNode {
				b := toBool(v.Value)
				re.AllowGrep = &b
			}
		case "maxfiles":
			if v.Kind == yaml.ScalarNode {
				val := v.Interface()
				switch actual := val.(type) {
				case int:
					re.MaxFiles = actual
				case int64:
					re.MaxFiles = int(actual)
				case float64:
					re.MaxFiles = int(actual)
				case string:
					if n, err := parseInt64(actual); err == nil {
						re.MaxFiles = int(n)
					}
				}
			}
		case "trimpath":
			if v.Kind == yaml.ScalarNode {
				re.TrimPath = v.Value
			}
		case "match":
			if v.Kind == yaml.MappingNode {
				m := &option.Options{}
				_ = v.Pairs(func(optKey string, optValue *yml.Node) error {
					switch strings.ToLower(optKey) {
					case "exclusions":
						m.Exclusions = asStrings(optValue)
					case "inclusions":
						m.Inclusions = asStrings(optValue)
					case "maxfilesize":
						switch vv := optValue.Interface().(type) {
						case int:
							m.MaxFileSize = vv
						case int64:
							m.MaxFileSize = int(vv)
						case float64:
							m.MaxFileSize = int(vv)
						case string:
							if n, err := parseInt64(vv); err == nil {
								m.MaxFileSize = int(n)
							}
						}
					}
					return nil
				})
				re.Match = m
			}
		case "minscore":
			if v.Kind == yaml.ScalarNode {
				val := v.Interface()
				var f float64
				switch actual := val.(type) {
				case float64:
					f = actual
				case int:
					f = float64(actual)
				case int64:
					f = float64(actual)
				case string:
					var parsed float64
					if _, err := fmt.Sscan(strings.TrimSpace(actual), &parsed); err == nil {
						f = parsed
					}
				}
				re.MinScore = &f
			}
		case "upstreamref":
			if v.Kind == yaml.ScalarNode {
				re.UpstreamRef = strings.TrimSpace(v.Value)
			}
		case "db":
			if v.Kind == yaml.ScalarNode {
				re.DB = strings.TrimSpace(v.Value)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if systemFlagSet {
		if !roleExplicit {
			re.Role = systemRole
		} else if !strings.EqualFold(strings.TrimSpace(re.Role), systemRole) {
			return nil, fmt.Errorf("resource entry role %q conflicts with system=%v", re.Role, systemRole == "system")
		}
	}
	if hasMCP {
		if hasURI {
			return nil, fmt.Errorf("resource entry cannot set both uri and mcp")
		}
		return re, nil
	}
	re.URI = expandUserHome(re.URI)
	re.DB = expandUserHome(re.DB)
	if strings.TrimSpace(re.URI) == "" {
		return nil, fmt.Errorf("resource entry missing uri")
	}
	return re, nil
}

func parseSystemFlag(node *yml.Node) (enabled bool, handled bool, err error) {
	if node == nil {
		return false, false, nil
	}
	val := node.Interface()
	switch actual := val.(type) {
	case bool:
		return actual, true, nil
	case string:
		return toBool(actual), true, nil
	default:
		return false, false, fmt.Errorf("system flag must be a boolean or string")
	}
}

func expandUserHome(u string) string {
	trimmed := strings.TrimSpace(u)
	if trimmed == "" {
		return u
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return u
	}
	if strings.HasPrefix(trimmed, "~/") || trimmed == "~" {
		return filepath.Join(home, strings.TrimPrefix(trimmed, "~"))
	}
	if strings.HasPrefix(trimmed, "file://") {
		prefix := "file://localhost"
		rest := strings.TrimPrefix(trimmed, prefix)
		if rest == trimmed {
			prefix = "file://"
			rest = strings.TrimPrefix(trimmed, prefix)
		}
		if rest == "" {
			return u
		}
		rest = strings.TrimLeft(rest, "/")
		if strings.HasPrefix(rest, "~") {
			rel := strings.TrimPrefix(rest, "~")
			abs := filepath.Join(home, rel)
			return prefix + "/" + filepath.ToSlash(strings.TrimLeft(abs, "/"))
		}
	}
	if strings.HasPrefix(trimmed, "file:") {
		prefix := "file:"
		rest := strings.TrimPrefix(trimmed, prefix)
		if rest == "" {
			return u
		}
		rest = strings.TrimLeft(rest, "/")
		if strings.HasPrefix(rest, "~") {
			rel := strings.TrimPrefix(rest, "~")
			abs := filepath.Join(home, rel)
			absSlash := filepath.ToSlash(abs)
			if !strings.HasPrefix(absSlash, "/") {
				absSlash = "/" + absSlash
			}
			return prefix + absSlash
		}
	}
	return u
}

func augmentResourcesWithKnowledge(agent *agentmdl.Agent) {
	if agent == nil {
		return
	}
	ensure := func(url, role string) {
		u := strings.TrimSpace(url)
		if u == "" {
			return
		}
		norm := strings.TrimRight(u, "/")
		for _, r := range agent.Resources {
			if r == nil || strings.TrimSpace(r.URI) == "" {
				continue
			}
			cur := strings.TrimRight(strings.TrimSpace(r.URI), "/")
			if cur == norm {
				b := true
				r.AllowSemanticMatch = &b
				return
			}
		}
		b := true
		agent.Resources = append(agent.Resources, &agentmdl.Resource{
			URI:                u,
			Role:               role,
			AllowSemanticMatch: &b,
		})
	}
	for _, k := range agent.Knowledge {
		if k != nil && strings.TrimSpace(k.URL) != "" {
			ensure(k.URL, "user")
		}
	}
	for _, k := range agent.SystemKnowledge {
		if k != nil && strings.TrimSpace(k.URL) != "" {
			ensure(k.URL, "system")
		}
	}
}
