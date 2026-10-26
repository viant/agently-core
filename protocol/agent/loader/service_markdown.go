package loader

import (
	"fmt"
	"strings"

	"github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/workspace/codec"
	"gopkg.in/yaml.v3"
)

func validateAgentKind(node *yaml.Node) error {
	if node == nil || len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	mapping := node.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]
		if strings.EqualFold(key.Value, "kind") && !strings.EqualFold(strings.TrimSpace(value.Value), "agent") {
			return fmt.Errorf("expected agent kind, got %q", value.Value)
		}
		if strings.EqualFold(key.Value, "agent") && value.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(value.Content); j += 2 {
				if strings.EqualFold(value.Content[j].Value, "kind") && !strings.EqualFold(strings.TrimSpace(value.Content[j+1].Value), "agent") {
					return fmt.Errorf("expected agent kind, got %q", value.Content[j+1].Value)
				}
			}
		}
	}
	return nil
}

func applyAgentMarkdown(value *agent.Agent, messages []codec.MarkdownMessage) error {
	for _, message := range messages {
		if message.URI != "" && message.Text != "" {
			return fmt.Errorf("agent message cannot combine a URI with inline body")
		}
		prompt := &binding.Prompt{Text: message.Text, URI: message.URI, Engine: message.Engine}
		inferPromptEngine(prompt)
		field := strings.TrimSpace(message.Field)
		switch message.Role {
		case "system":
			if field == "" {
				field = "instructionPrompt"
			}
			switch field {
			case "instructionPrompt":
				if value.InstructionPrompt != nil || value.Instruction != nil {
					return fmt.Errorf("agent Markdown instruction conflicts with frontmatter or another section")
				}
				value.InstructionPrompt = prompt
			case "systemPrompt":
				if value.SystemPrompt != nil {
					return fmt.Errorf("agent Markdown systemPrompt conflicts with frontmatter or another section")
				}
				value.SystemPrompt = prompt
			default:
				return fmt.Errorf("system message cannot target agent field %q", field)
			}
		case "user":
			if field != "" && field != "prompt" {
				return fmt.Errorf("user message cannot target agent field %q", field)
			}
			if value.Prompt != nil {
				return fmt.Errorf("agent Markdown prompt conflicts with frontmatter or another section")
			}
			value.Prompt = prompt
		case "assistant":
			return fmt.Errorf("agent has no assistant prompt field")
		default:
			return fmt.Errorf("unsupported agent message role %q", message.Role)
		}
	}
	return nil
}
