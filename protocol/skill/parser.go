package skill

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var skillNameRegex = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

var legacyAgentlyFieldTargets = map[string]string{
	"context":               "metadata.agently-context",
	"agent-id":              "metadata.agently-agent-id",
	"model":                 "metadata.agently-model",
	"effort":                "metadata.agently-effort or metadata.model-preferences",
	"temperature":           "metadata.agently-temperature",
	"max-tokens":            "metadata.agently-max-tokens",
	"preprocess":            "metadata.agently-preprocess",
	"preprocess-timeout":    "metadata.agently-preprocess-timeout",
	"async-narrator-prompt": "metadata.agently-async-narrator-prompt",
}

type frontmatterWire struct {
	Name                string         `yaml:"name"`
	Description         string         `yaml:"description"`
	License             string         `yaml:"license,omitempty"`
	Metadata            map[string]any `yaml:"metadata,omitempty"`
	AllowedTools        string         `yaml:"allowed-tools,omitempty"`
	Context             string         `yaml:"context,omitempty"`
	AgentID             string         `yaml:"agent-id,omitempty"`
	Model               string         `yaml:"model,omitempty"`
	Effort              string         `yaml:"effort,omitempty"`
	Temperature         *float64       `yaml:"temperature,omitempty"`
	MaxTokens           *int           `yaml:"max-tokens,omitempty"`
	Preprocess          *bool          `yaml:"preprocess,omitempty"`
	PreprocessTimeout   *int           `yaml:"preprocess-timeout,omitempty"`
	AsyncNarratorPrompt string         `yaml:"async-narrator-prompt,omitempty"`
}

func Parse(path, root, source, content string) (*Skill, []Diagnostic, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	front, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, nil, err
	}
	wire := frontmatterWire{}
	if err := yaml.Unmarshal([]byte(front), &wire); err != nil {
		return nil, nil, fmt.Errorf("parse skill frontmatter: %w", err)
	}
	fm := Frontmatter{
		Name:         wire.Name,
		Description:  wire.Description,
		License:      wire.License,
		Metadata:     wire.Metadata,
		AllowedTools: wire.AllowedTools,
	}
	fm.Agently = parseAgentlyMetadata(wire.Metadata, &LegacyAgentlyFields{
		Context:              wire.Context,
		AgentID:              wire.AgentID,
		Model:                wire.Model,
		Effort:               wire.Effort,
		Temperature:          wire.Temperature,
		MaxTokens:            wire.MaxTokens,
		Preprocess:           wire.Preprocess,
		PreprocessTimeoutSec: wire.PreprocessTimeout,
		AsyncNarratorPrompt:  wire.AsyncNarratorPrompt,
	})
	frontMap := map[string]any{}
	if err := yaml.Unmarshal([]byte(front), &frontMap); err == nil {
		raw := map[string]any{}
		for k, v := range frontMap {
			raw[k] = v
		}
		delete(raw, "name")
		delete(raw, "description")
		delete(raw, "license")
		delete(raw, "metadata")
		delete(raw, "context")
		delete(raw, "agent-id")
		delete(raw, "allowed-tools")
		delete(raw, "model")
		delete(raw, "effort")
		delete(raw, "temperature")
		delete(raw, "max-tokens")
		delete(raw, "preprocess")
		delete(raw, "preprocess-timeout")
		delete(raw, "async-narrator-prompt")
		fm.Raw = raw
	}
	s := &Skill{
		Frontmatter: fm,
		Body:        strings.TrimSpace(body),
		Root:        strings.TrimSpace(root),
		Path:        strings.TrimSpace(path),
		Source:      strings.TrimSpace(source),
	}
	var diags []Diagnostic
	for key, target := range legacyAgentlyFieldTargets {
		if rawValue, ok := frontMap[key]; ok && rawValue != nil {
			diags = append(diags, Diagnostic{
				Level:   "warn",
				Message: fmt.Sprintf("top-level field %q is deprecated; move it to %s", key, target),
				Path:    path,
			})
		}
	}
	if v := strings.TrimSpace(fm.Name); v == "" {
		diags = append(diags, Diagnostic{Level: "error", Message: "missing required frontmatter field: name", Path: path})
	} else if !skillNameRegex.MatchString(v) {
		diags = append(diags, Diagnostic{Level: "error", Message: "invalid skill name", Path: path})
	}
	desc := strings.TrimSpace(fm.Description)
	if desc == "" {
		diags = append(diags, Diagnostic{Level: "error", Message: "missing required frontmatter field: description", Path: path})
	} else if len(desc) > 1024 {
		diags = append(diags, Diagnostic{Level: "error", Message: "description exceeds 1024 characters", Path: path})
	}
	if effort := strings.ToLower(strings.TrimSpace(fm.EffortValue())); effort != "" {
		switch effort {
		case "low", "medium", "high":
		default:
			diags = append(diags, Diagnostic{Level: "error", Message: "invalid effort value", Path: path})
		}
	}
	if rawMode := strings.TrimSpace(wire.Context); rawMode != "" {
		if mode := NormalizeContextMode(rawMode); mode != strings.ToLower(rawMode) {
			diags = append(diags, Diagnostic{Level: "error", Message: "invalid context value", Path: path})
		}
	}
	if temp := fm.TemperatureValue(); temp != nil {
		if *temp < 0 || *temp > 2 {
			diags = append(diags, Diagnostic{Level: "error", Message: "temperature must be between 0 and 2", Path: path})
		}
	}
	if maxTokens := fm.MaxTokensValue(); maxTokens != 0 {
		if maxTokens < 0 || maxTokens >= 200000 {
			diags = append(diags, Diagnostic{Level: "error", Message: "max-tokens must be between 1 and 199999", Path: path})
		}
	}
	if fm.PreprocessEnabled() {
		timeout := fm.PreprocessTimeoutValue()
		if timeout == 0 {
			if fm.Agently == nil {
				fm.Agently = &AgentlyMetadata{}
			}
			v := 10
			fm.Agently.PreprocessTimeoutSec = &v
			s.Frontmatter.Agently = fm.Agently
		} else if timeout < 1 || timeout > 60 {
			diags = append(diags, Diagnostic{Level: "error", Message: "preprocess-timeout must be between 1 and 60", Path: path})
		}
	}
	return s, diags, nil
}

func splitFrontmatter(content string) (string, string, error) {
	if !strings.HasPrefix(content, "---\n") {
		return "", "", fmt.Errorf("missing YAML frontmatter")
	}
	rest := strings.TrimPrefix(content, "---\n")
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return "", "", fmt.Errorf("unterminated YAML frontmatter")
	}
	return rest[:idx], rest[idx+5:], nil
}
