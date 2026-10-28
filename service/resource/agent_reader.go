package resource

import (
	"context"
	"encoding/json"

	agent "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/agent/loader"
	identity "github.com/viant/agently-core/protocol/resource"
	primitive "github.com/viant/forge/backend/provider/contract"
)

type AgentReader struct{ Gateway *Gateway }

func NewAgentReader(g *Gateway) *AgentReader { return &AgentReader{Gateway: g} }
func (r *AgentReader) Load(ctx context.Context, uri string) (*agent.Agent, error) {
	if value, owned, err := loadPublishedAI[*agent.Agent](ctx, r.Gateway, uri, "agent", "agent-document.v1", decodeAgentDocument); owned {
		return value, err
	}
	got, err := ResolveDefinition[*agent.Agent](ctx, r.Gateway, "agent", uri, nil, func(raw json.RawMessage) (*agent.Agent, error) { return decodeAgentDocument(ctx, uri, raw) })
	if err != nil {
		return nil, err
	}
	return got.Definition, nil
}

func decodeAgentDocument(ctx context.Context, uri string, raw json.RawMessage) (*agent.Agent, error) {
	var envelope primitive.AgentDocumentEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if envelope.SchemaVersion != 1 || envelope.Document == "" {
		return nil, identity.ErrResource
	}
	return loader.DecodeDocument(ctx, uri, []byte(envelope.Document))
}
