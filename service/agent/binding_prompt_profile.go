package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/protocol/binding"
	intent "github.com/viant/agently-core/protocol/intent"
	resources "github.com/viant/agently-core/protocol/tool/service/resources"
)

func (s *Service) applySelectedPromptProfile(ctx context.Context, input *QueryInput, b *binding.Binding) error {
	if s == nil || input == nil || b == nil || s.promptRepo == nil {
		return nil
	}
	profile, err := s.selectedPromptProfileAny(ctx, input)
	if err != nil {
		return err
	}
	if profile == nil {
		return nil
	}
	if err := s.appendKnowledgeMatches(ctx, input, b, profile.Knowledge, profile.ID); err != nil {
		return err
	}
	applyProfileExecutionRestrictions(b, profile)
	if input.Agent != nil && !input.Agent.Prompts.AllowsSelectedProfileInjection() {
		return nil
	}
	profileID := input.EffectiveIntentProfileID()
	msgs, err := profile.Render(ctx, s.mcpMgr, nil)
	if err != nil {
		return fmt.Errorf("render intent profile %q: %w", profileID, err)
	}
	for i, msg := range msgs {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "system") {
			continue
		}
		content := strings.TrimSpace(msg.Text)
		if content == "" {
			continue
		}
		uri := fmt.Sprintf("intent://%s/message/%d", profileID, i)
		if hasDocumentURI(b.SystemDocuments.Items, uri) {
			continue
		}
		b.SystemDocuments.Items = append(b.SystemDocuments.Items, &binding.Document{
			Title:       strings.TrimSpace(profile.Name),
			PageContent: content,
			SourceURI:   uri,
			MimeType:    "text/markdown",
			Metadata:    map[string]string{"kind": "intent_profile", "profile": profileID},
		})
	}
	return nil
}

func applyProfileExecutionRestrictions(b *binding.Binding, profile *intent.Profile) {
	if b == nil || profile == nil || profile.Execution == nil || !profile.Execution.DisableDelegation {
		return
	}
	filtered := b.Tools.Signatures[:0]
	for _, definition := range b.Tools.Signatures {
		if definition == nil || isProfileDelegationTool(definition.Name) {
			continue
		}
		filtered = append(filtered, definition)
	}
	b.Tools.Signatures = filtered
}

func isProfileDelegationTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(name, "llm/agents:") || strings.HasPrefix(name, "llm_agents-") ||
		strings.HasPrefix(name, "orchestration/plan:") || strings.HasPrefix(name, "orchestration_plan-")
}

// applyProfileKnowledge interprets KnowledgeMatch only as a declarative,
// typed resource selection. Query planning and document-source interpretation
// are deliberately outside the generic agent: no query splitting, catalogs,
// source file reads, frontmatter parsing, URL rewriting, or phrase heuristics.
func (s *Service) appendKnowledgeMatches(ctx context.Context, input *QueryInput, b *binding.Binding, specs []intent.KnowledgeMatch, source string) error {
	if s == nil || input == nil || b == nil || len(specs) == 0 {
		return nil
	}
	if s.registry == nil {
		return fmt.Errorf("knowledge matching requires the resources service")
	}
	for index, spec := range specs {
		if len(spec.RootIDs) == 0 {
			if spec.Required {
				return fmt.Errorf("knowledge[%d] requires at least one rootId", index)
			}
			continue
		}
		args := map[string]any{
			"query":                   strings.TrimSpace(input.Query),
			"rootIds":                 append([]string(nil), spec.RootIDs...),
			"maxDocuments":            knowledgeMatchDepth(spec),
			"limitBytes":              spec.LimitBytes,
			"exclude":                 append([]string(nil), spec.Exclude...),
			"neighborFragmentsBefore": spec.NeighborFragmentsBefore,
			"neighborFragmentsAfter":  spec.NeighborFragmentsAfter,
		}
		if spec.Path != "" {
			args["path"] = spec.Path
		}
		raw, err := s.registry.Execute(ctx, "resources:match", args)
		if err != nil {
			if spec.Required {
				return fmt.Errorf("knowledge[%d] match failed: %w", index, err)
			}
			continue
		}
		var output resources.MatchOutput
		if err = json.Unmarshal([]byte(raw), &output); err != nil {
			if spec.Required {
				return fmt.Errorf("knowledge[%d] returned invalid match output: %w", index, err)
			}
			continue
		}
		items := resources.BindingDocuments(output.Documents, resources.DocumentSelection{
			MinScore:      spec.MinScore,
			MaxDocuments:  spec.MaxDocuments,
			MaxTotalBytes: spec.MaxTotalBytes,
		})
		for _, item := range items {
			if item == nil || hasDocumentURI(b.SystemDocuments.Items, item.SourceURI) {
				continue
			}
			if item.Metadata == nil {
				item.Metadata = map[string]string{}
			}
			item.Metadata["intent.profile"] = strings.TrimSpace(source)
			b.SystemDocuments.Items = append(b.SystemDocuments.Items, item)
		}
	}
	return nil
}

func knowledgeMatchDepth(spec intent.KnowledgeMatch) int {
	if spec.MaxFragments > 0 {
		return spec.MaxFragments
	}
	return spec.MaxDocuments
}
