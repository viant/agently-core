package config

import (
	"fmt"
	"strings"
	"unicode/utf8"

	mcpnames "github.com/viant/agently-core/pkg/mcpname"
	"gopkg.in/yaml.v3"
)

const ToolExecutionProtectionModeAtMostOnce = "atMostOnce"

// ToolExecutionProtectionDefaults configures the default-disabled durable
// pre-dispatch guard for exact tool identities.
type ToolExecutionProtectionDefaults struct {
	Enabled bool                          `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Rules   []ToolExecutionProtectionRule `yaml:"rules,omitempty" json:"rules,omitempty"`
}

// ToolExecutionProtectionRule protects one exact canonical tool identity.
type ToolExecutionProtectionRule struct {
	ID                string                          `yaml:"id" json:"id"`
	Tool              string                          `yaml:"tool" json:"tool"`
	Mode              string                          `yaml:"mode" json:"mode"`
	SemanticArguments *ToolExecutionSemanticArguments `yaml:"semanticArguments,omitempty" json:"semanticArguments,omitempty"`
}

// ToolExecutionSemanticArguments selects present top-level provider arguments.
// A nil Fields slice means all arguments; a non-nil empty slice is invalid.
type ToolExecutionSemanticArguments struct {
	Fields        []string `yaml:"fields,omitempty" json:"fields,omitempty"`
	ExcludeFields []string `yaml:"excludeFields,omitempty" json:"excludeFields,omitempty"`
}

func (c *ToolExecutionProtectionDefaults) UnmarshalYAML(value *yaml.Node) error {
	type raw ToolExecutionProtectionDefaults
	var decoded raw
	if err := decodeKnownMapping(value, map[string]bool{"enabled": true, "rules": true}, &decoded); err != nil {
		return fmt.Errorf("toolExecutionProtection: %w", err)
	}
	*c = ToolExecutionProtectionDefaults(decoded)
	return nil
}

func (r *ToolExecutionProtectionRule) UnmarshalYAML(value *yaml.Node) error {
	type raw ToolExecutionProtectionRule
	var decoded raw
	if err := decodeKnownMapping(value, map[string]bool{
		"id": true, "tool": true, "mode": true, "semanticArguments": true,
	}, &decoded); err != nil {
		return err
	}
	*r = ToolExecutionProtectionRule(decoded)
	return nil
}

func (s *ToolExecutionSemanticArguments) UnmarshalYAML(value *yaml.Node) error {
	type raw ToolExecutionSemanticArguments
	var decoded raw
	if err := decodeKnownMapping(value, map[string]bool{"fields": true, "excludeFields": true}, &decoded); err != nil {
		return err
	}
	*s = ToolExecutionSemanticArguments(decoded)
	return nil
}

func decodeKnownMapping(value *yaml.Node, allowed map[string]bool, out interface{}) error {
	if value == nil || value.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping")
	}
	for i := 0; i+1 < len(value.Content); i += 2 {
		key := value.Content[i].Value
		if !allowed[key] {
			return fmt.Errorf("unknown field %q", key)
		}
	}
	return value.Decode(out)
}

// Validate enforces the enabled protection contract without touching a DAO.
func (c ToolExecutionProtectionDefaults) Validate() error {
	if !c.Enabled {
		return nil
	}
	if len(c.Rules) == 0 {
		return fmt.Errorf("toolExecutionProtection: enabled protection requires at least one rule")
	}
	ids := make(map[string]struct{}, len(c.Rules))
	tools := make(map[string]string, len(c.Rules))
	for i := range c.Rules {
		rule := &c.Rules[i]
		rule.ID = strings.TrimSpace(rule.ID)
		if rule.ID == "" || !utf8.ValidString(rule.ID) {
			return fmt.Errorf("toolExecutionProtection.rules[%d]: id is required", i)
		}
		if _, ok := ids[rule.ID]; ok {
			return fmt.Errorf("toolExecutionProtection.rules[%d]: duplicate id %q", i, rule.ID)
		}
		ids[rule.ID] = struct{}{}
		if rule.Mode != ToolExecutionProtectionModeAtMostOnce {
			return fmt.Errorf("toolExecutionProtection.rules[%d]: unsupported mode %q", i, rule.Mode)
		}
		canonical, err := CanonicalProtectedToolName(rule.Tool)
		if err != nil {
			return fmt.Errorf("toolExecutionProtection.rules[%d]: %w", i, err)
		}
		if prior, ok := tools[canonical]; ok {
			return fmt.Errorf("toolExecutionProtection.rules[%d]: tool %q is already protected by rule %q", i, canonical, prior)
		}
		tools[canonical] = rule.ID
		if err := validateSemanticArguments(i, rule.SemanticArguments); err != nil {
			return err
		}
	}
	return nil
}

// CanonicalProtectedToolName applies canonicalization without accepting
// selectors or wildcard tool identities.
func CanonicalProtectedToolName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || !utf8.ValidString(name) {
		return "", fmt.Errorf("tool is required")
	}
	if strings.ContainsAny(name, "*?") {
		return "", fmt.Errorf("tool %q must be exact", raw)
	}
	if strings.Contains(name, "|") {
		return "", fmt.Errorf("tool %q must not contain a selector", raw)
	}
	canonical := strings.TrimSpace(mcpnames.Canonical(name))
	parsed := mcpnames.Name(canonical)
	if strings.TrimSpace(parsed.Service()) == "" || strings.TrimSpace(parsed.Method()) == "" {
		return "", fmt.Errorf("tool %q has invalid canonical identity", raw)
	}
	return mcpnames.Display(canonical), nil
}

func validateSemanticArguments(ruleIndex int, semantic *ToolExecutionSemanticArguments) error {
	if semantic == nil {
		return nil
	}
	if semantic.Fields != nil && len(semantic.Fields) == 0 {
		return fmt.Errorf("toolExecutionProtection.rules[%d].semanticArguments.fields: explicit list must not be empty", ruleIndex)
	}
	fields, hasWildcard, err := validateArgumentNames(semantic.Fields, true)
	if err != nil {
		return fmt.Errorf("toolExecutionProtection.rules[%d].semanticArguments.fields: %w", ruleIndex, err)
	}
	if hasWildcard && len(semantic.Fields) != 1 {
		return fmt.Errorf("toolExecutionProtection.rules[%d].semanticArguments.fields: * cannot be combined with other fields", ruleIndex)
	}
	excludes, excludeWildcard, err := validateArgumentNames(semantic.ExcludeFields, false)
	if err != nil {
		return fmt.Errorf("toolExecutionProtection.rules[%d].semanticArguments.excludeFields: %w", ruleIndex, err)
	}
	if excludeWildcard {
		return fmt.Errorf("toolExecutionProtection.rules[%d].semanticArguments.excludeFields: * is not allowed", ruleIndex)
	}
	if semantic.Fields != nil && !hasWildcard {
		remaining := len(fields)
		for name := range excludes {
			if _, ok := fields[name]; !ok {
				return fmt.Errorf("toolExecutionProtection.rules[%d].semanticArguments.excludeFields: %q is not selected by fields", ruleIndex, name)
			}
			remaining--
		}
		if remaining == 0 {
			return fmt.Errorf("toolExecutionProtection.rules[%d].semanticArguments: selection must not be empty", ruleIndex)
		}
	}
	return nil
}

func validateArgumentNames(values []string, allowWildcard bool) (map[string]struct{}, bool, error) {
	seen := make(map[string]struct{}, len(values))
	hasWildcard := false
	for _, name := range values {
		if name == "*" {
			hasWildcard = true
			if !allowWildcard {
				return nil, true, nil
			}
			continue
		}
		if name == "" || strings.TrimSpace(name) != name || !utf8.ValidString(name) {
			return nil, false, fmt.Errorf("invalid top-level field name %q", name)
		}
		for _, char := range name {
			if char < 0x20 {
				return nil, false, fmt.Errorf("invalid top-level field name %q", name)
			}
		}
		if _, ok := seen[name]; ok {
			return nil, false, fmt.Errorf("duplicate field %q", name)
		}
		seen[name] = struct{}{}
	}
	return seen, hasWildcard, nil
}
