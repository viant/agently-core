package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/viant/agently-core/protocol/binding"
	promptdef "github.com/viant/agently-core/protocol/prompt"
)

func (s *Service) applySelectedPromptProfile(ctx context.Context, input *QueryInput, b *binding.Binding) error {
	if s == nil || input == nil || b == nil || s.promptRepo == nil {
		return nil
	}
	profileID := strings.TrimSpace(input.PromptProfileId)
	if profileID == "" {
		return nil
	}
	profile, err := s.promptRepo.Load(ctx, profileID)
	if err != nil {
		return fmt.Errorf("load prompt profile %q: %w", profileID, err)
	}
	if profile == nil {
		return fmt.Errorf("prompt profile %q not found", profileID)
	}
	msgs, err := profile.Render(ctx, s.mcpMgr, nil)
	if err != nil {
		return fmt.Errorf("render prompt profile %q: %w", profileID, err)
	}
	for i, msg := range msgs {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "system") {
			continue
		}
		content := strings.TrimSpace(msg.Text)
		if content == "" {
			continue
		}
		uri := fmt.Sprintf("prompt://%s/message/%d", profileID, i)
		if hasDocumentURI(b.SystemDocuments.Items, uri) {
			continue
		}
		b.SystemDocuments.Items = append(b.SystemDocuments.Items, &binding.Document{
			Title:       strings.TrimSpace(profile.Name),
			PageContent: content,
			SourceURI:   uri,
			MimeType:    "text/markdown",
			Metadata: map[string]string{
				"kind":    "prompt_profile",
				"profile": profileID,
			},
		})
	}
	if len(profile.ToolBundles) > 0 {
		input.ToolBundles = append(input.ToolBundles, profile.ToolBundles...)
	}
	return nil
}

var _ = promptdef.Profile{}
