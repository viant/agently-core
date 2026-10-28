package loader

import (
	"context"
	"strings"

	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/workspace/codec"
	yml "github.com/viant/agently-core/workspace/service/meta/yml"
	"gopkg.in/yaml.v3"
)

// DecodeDocument reuses the ordinary agent codec for a producer-closed native
// document. It performs no filesystem discovery, hydration or mutable imports.
func DecodeDocument(ctx context.Context, uri string, raw []byte) (*agentmdl.Agent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var node yaml.Node
	var messages []codec.MarkdownMessage
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "---") {
		document, err := codec.ParseMarkdown(raw)
		if err != nil {
			return nil, err
		}
		raw, messages = document.Metadata, document.Messages
	}
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return nil, err
	}
	if err := validateAgentKind(&node); err != nil {
		return nil, err
	}
	value := &agentmdl.Agent{Source: &agentmdl.Source{URL: uri}}
	parser := &Service{}
	if err := parser.parseAgent((*yml.Node)(&node), value); err != nil {
		return nil, err
	}
	normalizeAgent(value)
	if err := applyAgentMarkdown(value, messages); err != nil {
		return nil, err
	}
	augmentResourcesWithKnowledge(value)
	if err := value.Validate(); err != nil {
		return nil, err
	}
	return value, nil
}
