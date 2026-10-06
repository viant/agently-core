package clienttool

import (
	"context"
	"encoding/json"
	"fmt"
)

type ResultAdapter string

const AgentRunResultV1 ResultAdapter = "agently.agents.run.v1"

// Dependency is a causal waiting edge. ParentCall is the existing backend tool
// call to complete after the child turn succeeds; it is never a frontend tool.
type Dependency struct {
	ID                  string        `json:"id"`
	ParentCall          PendingCall   `json:"parentCall"`
	ChildConversationID string        `json:"childConversationId"`
	ChildTurnID         string        `json:"childTurnId"`
	ChildAgentID        string        `json:"childAgentId,omitempty"`
	ExecutionMode       string        `json:"executionMode,omitempty"`
	ResultAdapter       ResultAdapter `json:"resultAdapter"`
	Waiting             bool          `json:"waiting"`
}

type executingCallKey struct{}

func WithExecutingCall(ctx context.Context, call PendingCall) context.Context {
	return context.WithValue(ctx, executingCallKey{}, call)
}
func ExecutingCallFromContext(ctx context.Context) (PendingCall, bool) {
	call, ok := ctx.Value(executingCallKey{}).(PendingCall)
	return call, ok
}
func turnKey(conversationID, turnID string) string { return conversationID + "\x00" + turnID }

func (s *Session) RegisterDependency(dependency Dependency) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dependency.ID == "" || dependency.ParentCall.ID == "" || dependency.ParentCall.ToolMessageID == "" || dependency.ParentCall.ConversationID == "" || dependency.ParentCall.TurnID == "" || dependency.ChildConversationID == "" || dependency.ChildTurnID == "" {
		return fmt.Errorf("dependency requires original parent call and child turn identity")
	}
	if dependency.ResultAdapter != AgentRunResultV1 {
		return fmt.Errorf("unknown dependency result adapter: %s", dependency.ResultAdapter)
	}
	if s.dependencies == nil {
		s.dependencies = map[string]Dependency{}
	}
	parentKey := turnKey(dependency.ParentCall.ConversationID, dependency.ParentCall.TurnID)
	childKey := turnKey(dependency.ChildConversationID, dependency.ChildTurnID)
	for _, edge := range s.dependencies {
		if turnKey(edge.ChildConversationID, edge.ChildTurnID) == childKey && (edge.ID != dependency.ID || edge.ParentCall.ID != dependency.ParentCall.ID) {
			return fmt.Errorf("child turn already belongs to another dependency")
		}
	}
	if parentKey == childKey || s.reachableLocked(childKey, parentKey) {
		return fmt.Errorf("cyclic client tool dependency")
	}
	copied, err := clone(dependency)
	if err != nil {
		return err
	}
	if previous, exists := s.dependencies[dependency.ID]; exists {
		prior, _ := json.Marshal(previous.ParentCall)
		next, _ := json.Marshal(dependency.ParentCall)
		if string(prior) != string(next) || previous.ChildTurnID != dependency.ChildTurnID || previous.ChildConversationID != dependency.ChildConversationID {
			return fmt.Errorf("conflicting dependency identity")
		}
		return nil
	}
	s.dependencies[dependency.ID] = copied
	s.dependencyOrder = append(s.dependencyOrder, dependency.ID)
	return nil
}
func (s *Session) reachableLocked(from, to string, waitingOnly ...bool) bool {
	visited := map[string]bool{}
	var walk func(string) bool
	walk = func(current string) bool {
		if current == to {
			return true
		}
		if visited[current] {
			return false
		}
		visited[current] = true
		for _, edge := range s.dependencies {
			if (len(waitingOnly) == 0 || !waitingOnly[0] || edge.Waiting) && turnKey(edge.ParentCall.ConversationID, edge.ParentCall.TurnID) == current && walk(turnKey(edge.ChildConversationID, edge.ChildTurnID)) {
				return true
			}
		}
		return false
	}
	return walk(from)
}
func (s *Session) MarkDependencyWaiting(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	edge, exists := s.dependencies[id]
	if !exists {
		return fmt.Errorf("unknown dependency %s", id)
	}
	edge.Waiting = true
	s.dependencies[id] = edge
	return nil
}
func (s *Session) PendingForSubtree(conversationID, turnID string) []PendingCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := turnKey(conversationID, turnID)
	var result []PendingCall
	for _, pending := range s.pending {
		if s.reachableLocked(root, turnKey(pending.ConversationID, pending.TurnID), true) {
			copy, _ := clone(pending)
			result = append(result, copy)
		}
	}
	return result
}
func (s *Session) WaitingDependencies(conversationID, turnID string) []Dependency {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := turnKey(conversationID, turnID)
	var result []Dependency
	for _, id := range s.dependencyOrder {
		edge := s.dependencies[id]
		if edge.Waiting && s.reachableLocked(root, turnKey(edge.ParentCall.ConversationID, edge.ParentCall.TurnID), true) {
			copy, _ := clone(edge)
			result = append(result, copy)
		}
	}
	return result
}
func (s *Session) WaitingOnCall(conversationID, turnID, callID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, edge := range s.dependencies {
		if edge.Waiting && edge.ParentCall.ConversationID == conversationID && edge.ParentCall.TurnID == turnID && edge.ParentCall.ID == callID {
			return true
		}
	}
	return false
}

type ChildResult struct {
	Content        string
	Status         string
	ConversationID string
	TurnID         string
	Error          string
}

// FormatDependencyResult mirrors the public agents:run result shape, never the
// QueryOutput agent/config/auth/render data. Waiting children cannot complete it.
func FormatDependencyResult(dependency Dependency, result ChildResult) (string, string, error) {
	if dependency.ResultAdapter != AgentRunResultV1 {
		return "", "", fmt.Errorf("unknown dependency result adapter: %s", dependency.ResultAdapter)
	}
	if result.ConversationID != dependency.ChildConversationID || result.TurnID != dependency.ChildTurnID {
		return "", "", fmt.Errorf("dependency child result identity mismatch")
	}
	switch result.Status {
	case "succeeded", "completed", "failed", "canceled", "cancelled":
	default:
		return "", "", fmt.Errorf("child result is not terminal: %s", result.Status)
	}
	body := struct {
		Answer         string `json:"answer"`
		Status         string `json:"status,omitempty"`
		Error          string `json:"error,omitempty"`
		ConversationID string `json:"conversationId,omitempty"`
		MessageID      string `json:"messageId,omitempty"`
	}{Answer: result.Content, Status: result.Status, Error: result.Error, ConversationID: result.ConversationID, MessageID: result.TurnID}
	bytes, err := json.Marshal(body)
	return string(bytes), result.Error, err
}

type continuationDependencyKey struct{}

// WithContinuationDependency carries a trusted dependency loaded from durable
// protocol state. HTTP forwarded properties must never directly populate it.
func WithContinuationDependency(ctx context.Context, dependency Dependency) context.Context {
	copy, _ := clone(dependency)
	return context.WithValue(ctx, continuationDependencyKey{}, copy)
}
func ContinuationDependencyFromContext(ctx context.Context) (Dependency, bool) {
	dependency, ok := ctx.Value(continuationDependencyKey{}).(Dependency)
	return dependency, ok
}

// DetachedContinuation is trusted host authority loaded from a principal-scoped
// internal detached protocol admission. A native conversation link alone does
// not prove that a child was detached.
type DetachedContinuation struct {
	ChildConversationID  string
	ChildTurnID          string
	ParentConversationID string
	ParentTurnID         string
	ParentProtocolRunID  string
	ExecutionMode        string
}
type detachedContinuationKey struct{}

func WithDetachedContinuation(ctx context.Context, continuation DetachedContinuation) context.Context {
	return context.WithValue(ctx, detachedContinuationKey{}, continuation)
}
func DetachedContinuationFromContext(ctx context.Context) (DetachedContinuation, bool) {
	continuation, ok := ctx.Value(detachedContinuationKey{}).(DetachedContinuation)
	return continuation, ok
}
