package agent

import (
	"context"
	"encoding/json"
	"github.com/viant/agently-core/internal/debugtrace"
	"golang.org/x/sync/errgroup"
	"sort"
	"strings"
	"time"
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
	var toolItems, skillItems []capability
	if s == nil || input == nil || input.Agent == nil {
		return "", nil
	}
	group, ctx := errgroup.WithContext(ctx)
	if s.registry != nil {
		group.Go(func() error {
			started := time.Now()
			tools, err := s.discoverableToolDefinitions(ctx, input)
			debugtrace.Write("agent", "intake_capability_stage", map[string]any{"conversationID": input.ConversationID, "turnID": input.MessageID, "stage": "tools", "elapsedMs": float64(time.Since(started)) / float64(time.Millisecond), "count": len(tools)})
			if err != nil {
				return err
			}
			for _, tool := range tools {
				toolItems = append(toolItems, capability{Kind: "tool", Name: tool.Name, Description: tool.Description})
			}
			return nil
		})
	}
	if s.skillSvc != nil {
		group.Go(func() error {
			started := time.Now()
			skills, err := s.skillSvc.ListVisible(ctx, input.Agent)
			debugtrace.Write("agent", "intake_capability_stage", map[string]any{"conversationID": input.ConversationID, "turnID": input.MessageID, "stage": "skills", "elapsedMs": float64(time.Since(started)) / float64(time.Millisecond), "count": len(skills)})
			if err != nil {
				return err
			}
			for _, skill := range skills {
				name := skill.QualifiedName
				if strings.TrimSpace(name) == "" {
					name = skill.Name
				}
				skillItems = append(skillItems, capability{Kind: "skill", Name: name, Description: skill.Description, URI: skill.URI})
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return "", err
	}
	items := append(toolItems, skillItems...)
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
