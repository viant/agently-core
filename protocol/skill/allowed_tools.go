package skill

import "strings"

type AllowedTool struct {
	Raw         string
	ToolPattern string
	BashCommand string
}

func ParseAllowedTools(raw string) []AllowedTool {
	tokens := splitAllowedTools(strings.TrimSpace(raw))
	var out []AllowedTool
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		item := AllowedTool{Raw: token}
		if strings.HasPrefix(token, "Bash(") && strings.HasSuffix(token, ")") {
			spec := strings.TrimSuffix(strings.TrimPrefix(token, "Bash("), ")")
			spec = strings.TrimSpace(spec)
			spec = strings.TrimSuffix(spec, ":*")
			spec = strings.TrimSuffix(spec, "*")
			item.BashCommand = strings.TrimSpace(spec)
		} else {
			item.ToolPattern = token
		}
		out = append(out, item)
	}
	return out
}

func splitAllowedTools(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	var current strings.Builder
	depth := 0
	for _, r := range raw {
		switch r {
		case '(':
			depth++
			current.WriteRune(r)
		case ')':
			if depth > 0 {
				depth--
			}
			current.WriteRune(r)
		case ' ', '\t', '\n', '\r':
			if depth == 0 {
				if current.Len() > 0 {
					out = append(out, current.String())
					current.Reset()
				}
			} else {
				current.WriteRune(r)
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}
