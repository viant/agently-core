package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// Intake sees metadata for the same tool surface available to execution. It
// receives no tool schemas, skill bodies, results, or extra execution privileges.
func (s *Service) intakeCapabilitySummary(ctx context.Context, input *QueryInput) (string, error) {
	type capability struct {
		Kind        string `json:"kind"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		URI         string `json:"uri,omitempty"`
	}
	var items []capability
	if s == nil || input == nil || input.Agent == nil {
		return "", nil
	}
	if s.registry != nil {
		tools, err := s.resolveTools(ctx, input)
		if err != nil {
			return "", err
		}
		for _, tool := range tools {
			items = append(items, capability{Kind: "tool", Name: tool.Definition.Name, Description: tool.Definition.Description})
		}
	}
	if s.skillSvc != nil {
		skills, err := s.skillSvc.ListVisible(ctx, input.Agent)
		if err != nil {
			return "", err
		}
		for _, skill := range skills {
			name := skill.QualifiedName
			if strings.TrimSpace(name) == "" {
				name = skill.Name
			}
			items = append(items, capability{Kind: "skill", Name: name, Description: skill.Description, URI: skill.URI})
		}
	}
	if len(items) == 0 {
		return "", nil
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].URI < items[j].URI
	})
	raw, err := json.Marshal(items)
	return string(raw), err
}
