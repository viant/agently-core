package skill

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var skillNameRegex = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

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
	raw := map[string]any{}
	if err := yaml.Unmarshal([]byte(front), &raw); err == nil {
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
	if effort := strings.ToLower(strings.TrimSpace(fm.Effort)); effort != "" {
		switch effort {
		case "low", "medium", "high":
		default:
			diags = append(diags, Diagnostic{Level: "error", Message: "invalid effort value", Path: path})
		}
	}
	if mode := NormalizeContextMode(fm.Context); strings.TrimSpace(fm.Context) != "" && mode != strings.ToLower(strings.TrimSpace(fm.Context)) {
		diags = append(diags, Diagnostic{Level: "error", Message: "invalid context value", Path: path})
	} else {
		s.Frontmatter.Context = mode
	}
	if fm.Temperature != nil {
		if *fm.Temperature < 0 || *fm.Temperature > 2 {
			diags = append(diags, Diagnostic{Level: "error", Message: "temperature must be between 0 and 2", Path: path})
		}
	}
	if fm.MaxTokens < 0 || fm.MaxTokens >= 200000 {
		diags = append(diags, Diagnostic{Level: "error", Message: "max-tokens must be between 1 and 199999", Path: path})
	}
	if fm.Preprocess {
		if fm.PreprocessTimeoutSeconds == 0 {
			fm.PreprocessTimeoutSeconds = 10
			s.Frontmatter.PreprocessTimeoutSeconds = 10
		} else if fm.PreprocessTimeoutSeconds < 1 || fm.PreprocessTimeoutSeconds > 60 {
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
