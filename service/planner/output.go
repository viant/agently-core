package planner

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Output struct {
	StrategyFamily      string         `json:"strategyFamily,omitempty"`
	BaseProfiles        []string       `json:"baseProfiles,omitempty"`
	ToolBundles         []string       `json:"toolBundles,omitempty"`
	TemplateID          string         `json:"templateId,omitempty"`
	RequiredEvidence    []string       `json:"requiredEvidence,omitempty"`
	ExecutionOrder      []string       `json:"executionOrder,omitempty"`
	FinalizationGuards  []string       `json:"finalizationGuards,omitempty"`
	NarrationPolicy     map[string]any `json:"narrationPolicy,omitempty"`
	WorkspaceExtensions map[string]any `json:"workspaceExtensions,omitempty"`
	ParallelToolCalls   *bool          `json:"parallelToolCalls,omitempty"`
}

func Parse(raw string) (*Output, error) {
	raw = stripFence(raw)
	var out Output
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		start := strings.Index(raw, "{")
		end := strings.LastIndex(raw, "}")
		if start >= 0 && end > start {
			if err2 := json.Unmarshal([]byte(raw[start:end+1]), &out); err2 == nil {
				return &out, nil
			}
		}
		return nil, fmt.Errorf("planner parse: %w", err)
	}
	return &out, nil
}

func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if strings.HasSuffix(s, "```") {
			s = s[:len(s)-3]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

func JSONSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"strategyFamily": map[string]interface{}{"type": "string"},
			"baseProfiles": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"toolBundles": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"templateId": map[string]interface{}{"type": "string"},
			"requiredEvidence": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"executionOrder": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"finalizationGuards": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"narrationPolicy": map[string]interface{}{
				"type":                 "object",
				"additionalProperties": true,
			},
			"workspaceExtensions": map[string]interface{}{
				"type":                 "object",
				"additionalProperties": true,
			},
			"parallelToolCalls": map[string]interface{}{"type": "boolean"},
		},
		"additionalProperties": false,
	}
}
