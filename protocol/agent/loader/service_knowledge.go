package loader

import (
	"fmt"
	"strings"

	agentmdl "github.com/viant/agently-core/protocol/agent"
	yml "github.com/viant/agently-core/workspace/service/meta/yml"
	"github.com/viant/embedius/matching/option"
	"gopkg.in/yaml.v3"
)

// parseKnowledge parses a knowledge entry from a YAML node.
func parseKnowledge(node *yml.Node) (*agentmdl.Knowledge, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("knowledge node should be a mapping")
	}
	knowledge := &agentmdl.Knowledge{}
	err := node.Pairs(func(key string, valueNode *yml.Node) error {
		switch strings.ToLower(key) {
		case "description":
			if valueNode.Kind == yaml.ScalarNode {
				knowledge.Description = valueNode.Value
			}
		case "inclusionmode":
			if valueNode.Kind == yaml.ScalarNode {
				knowledge.InclusionMode = valueNode.Value
			}
		case "locations", "url":
			switch valueNode.Kind {
			case yaml.ScalarNode:
				knowledge.URL = valueNode.Value
			case yaml.SequenceNode:
				var locations []string
				for _, locNode := range valueNode.Content {
					if locNode.Kind == yaml.ScalarNode {
						locations = append(locations, locNode.Value)
					}
				}
				if len(locations) > 0 {
					knowledge.URL = locations[0]
				}
			}
		case "filter", "match":
			if valueNode.Kind == yaml.MappingNode {
				opts := &option.Options{}
				_ = valueNode.Pairs(func(optKey string, optValue *yml.Node) error {
					switch strings.ToLower(optKey) {
					case "exclusions":
						opts.Exclusions = asStrings(optValue)
					case "inclusions":
						opts.Inclusions = asStrings(optValue)
					case "maxfilesize":
						switch vv := optValue.Interface().(type) {
						case int:
							opts.MaxFileSize = vv
						case int64:
							opts.MaxFileSize = int(vv)
						case float64:
							opts.MaxFileSize = int(vv)
						case string:
							if n, err := parseInt64(vv); err == nil {
								opts.MaxFileSize = int(n)
							}
						}
					}
					return nil
				})
				knowledge.Filter = opts
			}
		case "maxfiles":
			if valueNode.Kind == yaml.ScalarNode {
				switch vv := valueNode.Interface().(type) {
				case int:
					knowledge.MaxFiles = vv
				case int64:
					knowledge.MaxFiles = int(vv)
				case float64:
					knowledge.MaxFiles = int(vv)
				case string:
					if n, err := parseInt64(vv); err == nil {
						knowledge.MaxFiles = int(n)
					}
				}
			}
		case "minscore":
			if valueNode.Kind == yaml.ScalarNode {
				var f float64
				switch vv := valueNode.Interface().(type) {
				case float64:
					f = vv
				case int:
					f = float64(vv)
				case int64:
					f = float64(vv)
				case string:
					if _, err := fmt.Sscan(strings.TrimSpace(vv), &f); err != nil {
						return nil
					}
				default:
					return nil
				}
				knowledge.MinScore = &f
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return knowledge, nil
}

func parseInt64(s string) (int64, error) {
	s = strings.TrimSpace(s)
	var n int64
	_, err := fmt.Sscan(s, &n)
	return n, err
}

func parseFloat64(s string) (float64, error) {
	s = strings.TrimSpace(s)
	var f float64
	_, err := fmt.Sscan(s, &f)
	return f, err
}

func toBool(s string) bool {
	lv := strings.ToLower(strings.TrimSpace(s))
	return lv == "true" || lv == "yes" || lv == "on" || lv == "1"
}

func asStrings(optValue *yml.Node) []string {
	value := optValue.Interface()
	switch actual := value.(type) {
	case []string:
		return actual
	case []interface{}:
		var result []string
		for _, item := range actual {
			result = append(result, fmt.Sprintf("%v", item))
		}
		return result
	}
	return nil
}

func asStringList(value *yml.Node) []string {
	if value == nil {
		return nil
	}
	switch value.Kind {
	case yaml.ScalarNode:
		v := strings.TrimSpace(value.Value)
		if v == "" {
			return nil
		}
		return []string{v}
	case yaml.SequenceNode:
		out := make([]string, 0, len(value.Content))
		for _, item := range value.Content {
			if item == nil || item.Kind != yaml.ScalarNode {
				continue
			}
			v := strings.TrimSpace(item.Value)
			if v == "" {
				continue
			}
			out = append(out, v)
		}
		if len(out) > 0 {
			return out
		}
	}
	return asStrings(value)
}
