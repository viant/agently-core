package a2a

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	agentsvc "github.com/viant/agently-core/service/agent"
)

// Service manages A2A protocol operations. It is stateless —
// for server-side (exposing agents as A2A), it bridges to agent.Query()
// and the conversation system holds all state. For client-side (consuming
// external A2A agents), see Client.
type Service struct {
	agent       *agentsvc.Service
	agentFinder agentmodel.Finder
}

type agentCatalog interface {
	All() []*agentmodel.Agent
}

// New creates an A2A service.
func New(agent *agentsvc.Service, finder agentmodel.Finder) *Service {
	return &Service{
		agent:       agent,
		agentFinder: finder,
	}
}

// GetAgentCard returns the A2A agent card for the given agent ID.
func (s *Service) GetAgentCard(ctx context.Context, agentID string) (*AgentCard, error) {
	ag, err := s.agentFinder.Find(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("find agent %s: %w", agentID, err)
	}
	card := &AgentCard{
		Name: ag.Name,
	}
	if ag.ID != "" {
		card.Name = ag.ID
	}
	if ag.Profile != nil {
		card.Description = ag.Profile.Description
	}
	card.Capabilities = &AgentCapabilities{}
	if a2aCfg := EffectiveA2A(ag); a2aCfg != nil {
		card.Capabilities.Streaming = a2aCfg.Streaming
		if a2aCfg.Auth != nil && a2aCfg.Auth.Enabled {
			card.Authentication = map[string]interface{}{
				"type":       "bearer",
				"resource":   a2aCfg.Auth.Resource,
				"scopes":     a2aCfg.Auth.Scopes,
				"useIDToken": a2aCfg.Auth.UseIDToken,
			}
		}
	}
	return card, nil
}

// SendMessage sends a message to an A2A agent via agent.Query() and returns
// the result as an A2A task envelope. The conversation system tracks all state.
func (s *Service) SendMessage(ctx context.Context, agentID string, req *SendMessageRequest) (*SendMessageResponse, error) {
	messages := req.EffectiveMessages()
	if len(messages) == 0 {
		return nil, fmt.Errorf("message with at least one part is required")
	}

	query := extractText(messages)
	if query == "" {
		return nil, fmt.Errorf("no text content in message")
	}

	// Build agent query input — contextID maps to conversationID.
	input := &agentsvc.QueryInput{
		AgentID:        agentID,
		Query:          query,
		ConversationID: req.ContextID,
	}

	out := &agentsvc.QueryOutput{}
	if err := s.agent.Query(ctx, input, out); err != nil {
		errMsg := err.Error()
		task := &Task{
			ID:        uuid.New().String(),
			ContextID: req.ContextID,
			Status: TaskStatus{
				State:     TaskStateFailed,
				Error:     &errMsg,
				UpdatedAt: time.Now().UTC(),
			},
		}
		return &SendMessageResponse{Task: *task}, nil
	}

	// Build task envelope from response.
	taskID := "t-" + out.MessageID
	if out.MessageID == "" {
		taskID = uuid.New().String()
	}
	contextID := req.ContextID
	if out.ConversationID != "" {
		contextID = out.ConversationID
	}

	task := &Task{
		ID:        taskID,
		ContextID: contextID,
		Status: TaskStatus{
			State:     TaskStateCompleted,
			UpdatedAt: time.Now().UTC(),
		},
		Artifacts: []Artifact{{
			ID:        uuid.New().String(),
			CreatedAt: time.Now().UTC(),
			Parts: []Part{{
				Type: "text",
				Text: out.Content,
			}},
		}},
	}

	return &SendMessageResponse{Task: *task}, nil
}

// ListA2AAgents returns agent IDs that have A2A serving enabled.
func (s *Service) ListA2AAgents(ctx context.Context, agentIDs []string) ([]string, error) {
	var result []string
	for _, id := range agentIDs {
		ag, err := s.agentFinder.Find(ctx, id)
		if err != nil {
			continue
		}
		if a2aCfg := EffectiveA2A(ag); a2aCfg != nil && a2aCfg.Enabled {
			result = append(result, id)
		}
	}
	return result, nil
}

// EffectiveA2A returns the A2A configuration for an agent, checking both
// modern Serve.A2A and legacy ExposeA2A. Exported for use by the server launcher.
func EffectiveA2A(ag *agentmodel.Agent) *agentmodel.ServeA2A {
	if ag.Serve != nil && ag.Serve.A2A != nil {
		return ag.Serve.A2A
	}
	if ag.ExposeA2A != nil && ag.ExposeA2A.Enabled {
		return &agentmodel.ServeA2A{
			Enabled:   ag.ExposeA2A.Enabled,
			Port:      ag.ExposeA2A.Port,
			Streaming: ag.ExposeA2A.Streaming,
			Auth:      ag.ExposeA2A.Auth,
		}
	}
	return nil
}

func (s *Service) resolveSharedAgent(ctx context.Context, requested string) (*agentmodel.Agent, *agentmodel.ServeA2A, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		ag, err := s.agentFinder.Find(ctx, requested)
		if err != nil {
			return nil, nil, err
		}
		cfg := EffectiveA2A(ag)
		if cfg == nil || !cfg.Enabled {
			return nil, nil, fmt.Errorf("agent %s is not A2A-enabled", requested)
		}
		return ag, cfg, nil
	}
	catalog, ok := s.agentFinder.(agentCatalog)
	if !ok {
		return nil, nil, fmt.Errorf("agentId is required")
	}
	var match *agentmodel.Agent
	for _, ag := range catalog.All() {
		if ag == nil {
			continue
		}
		cfg := EffectiveA2A(ag)
		if cfg == nil || !cfg.Enabled {
			continue
		}
		if match != nil {
			return nil, nil, fmt.Errorf("agentId is required when multiple A2A agents are enabled")
		}
		match = ag
	}
	if match == nil {
		return nil, nil, fmt.Errorf("no A2A-enabled agent found")
	}
	return match, EffectiveA2A(match), nil
}

// extractText concatenates all text parts from messages.
func extractText(messages []Message) string {
	var parts []string
	for _, m := range messages {
		for _, p := range m.Parts {
			if p.Type == "text" || (p.Type == "" && p.Text != "") {
				parts = append(parts, p.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}
