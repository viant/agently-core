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

func Parse(path, root, source, content string) (*Skill, []Diagnostic, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	front, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, nil, err
	}
	fm := Frontmatter{}
	if err := yaml.Unmarshal([]byte(front), &fm); err != nil {
		return nil, nil, fmt.Errorf("parse skill frontmatter: %w", err)
	}
	fm.Agently = parseAgentlyMetadata(fm)
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
	if rawMode := strings.TrimSpace(fm.Context); rawMode != "" {
		if mode := NormalizeContextMode(rawMode); mode != strings.ToLower(rawMode) {
			diags = append(diags, Diagnostic{Level: "error", Message: "invalid context value", Path: path})
		}
	}
	if temp := fm.TemperatureValue(); temp != nil {
		if *temp < 0 || *temp > 2 {
			diags = append(diags, Diagnostic{Level: "error", Message: "temperature must be between 0 and 2", Path: path})
		}
	}
	if fm.MaxTokens < 0 || fm.MaxTokens >= 200000 {
		diags = append(diags, Diagnostic{Level: "error", Message: "max-tokens must be between 1 and 199999", Path: path})
	}
	if fm.PreprocessEnabled() {
		timeout := fm.PreprocessTimeoutValue()
		if timeout == 0 {
			fm.PreprocessTimeoutSeconds = 10
			s.Frontmatter.PreprocessTimeoutSeconds = 10
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
