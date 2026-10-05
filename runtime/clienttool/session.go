// Package clienttool carries request-scoped frontend tools without modifying a
// backend registry. The protocol layer persists definitions and pending calls.
package clienttool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/mcpname"
)

type PendingCall struct {
	Metadata           json.RawMessage        `json:"metadata,omitempty"`
	ProtocolID         string                 `json:"protocolId,omitempty"`
	ID                 string                 `json:"id"`
	Name               string                 `json:"name"`
	Arguments          map[string]interface{} `json:"arguments"`
	ToolMessageID      string                 `json:"toolMessageId"`
	AssistantMessageID string                 `json:"assistantMessageId"`
	ConversationID     string                 `json:"conversationId"`
	TurnID             string                 `json:"turnId"`
	Iteration          int                    `json:"iteration"`
}

type Session struct {
	mu              sync.Mutex
	definitions     []llm.ToolDefinition
	byName          map[string]int
	pending         []PendingCall
	err             error
	unscopedErr     error
	scopeErrors     map[string]error
	dependencies    map[string]Dependency
	dependencyOrder []string
	metadata        map[string]json.RawMessage
}

type contextKey struct{}

func WithSession(ctx context.Context, session *Session) context.Context {
	return context.WithValue(ctx, contextKey{}, session)
}
func FromContext(ctx context.Context) *Session {
	session, _ := ctx.Value(contextKey{}).(*Session)
	return session
}
func canonical(name string) string {
	return strings.ToLower(mcpname.Canonical(strings.TrimSpace(name)))
}

func NewSession(definitions []llm.ToolDefinition) (*Session, error) {
	session := &Session{byName: make(map[string]int), scopeErrors: map[string]error{}}
	for _, definition := range definitions {
		if strings.TrimSpace(definition.Name) == "" {
			return nil, fmt.Errorf("client tool name is required")
		}
		key := canonical(definition.Name)
		if _, exists := session.byName[key]; exists {
			return nil, fmt.Errorf("duplicate client tool alias: %s", definition.Name)
		}
		copied, err := clone(definition)
		if err != nil {
			return nil, fmt.Errorf("client tool %s: %w", definition.Name, err)
		}
		session.byName[key] = len(session.definitions)
		session.definitions = append(session.definitions, copied)
	}
	return session, nil
}
func clone[T any](value T) (T, error) {
	var copy T
	data, err := json.Marshal(value)
	if err != nil {
		return copy, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	err = decoder.Decode(&copy)
	return copy, err
}
func (s *Session) Definitions() []llm.ToolDefinition { value, _ := clone(s.definitions); return value }
func (s *Session) Lookup(name string) (llm.ToolDefinition, bool) {
	index, ok := s.byName[canonical(name)]
	if !ok {
		return llm.ToolDefinition{}, false
	}
	value, _ := clone(s.definitions[index])
	return value, true
}
func (s *Session) ValidateBackend(definitions []llm.ToolDefinition) error {
	for _, definition := range definitions {
		if _, ok := s.byName[canonical(definition.Name)]; ok {
			return fmt.Errorf("client tool collides with backend tool: %s", definition.Name)
		}
	}
	return nil
}
func (s *Session) RecordError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
	s.unscopedErr = err
}
func (s *Session) Error() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *Session) Pending() []PendingCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, _ := clone(s.pending)
	return value
}

// Defer reserves a call id and serializes persistence. Duplicate identical calls
// reuse the original records; conflicting calls and persistence errors poison
// this session so the agent loop cannot treat a swallowed executor error as success.
func (s *Session) Defer(id, name string, args map[string]interface{}, persist func() (PendingCall, error)) (PendingCall, error) {
	return s.deferForScope("", "", id, name, args, persist)
}
func (s *Session) DeferForTurn(conversationID, turnID, id, name string, args map[string]interface{}, persist func() (PendingCall, error)) (PendingCall, error) {
	return s.deferForScope(conversationID, turnID, id, name, args, persist)
}
func (s *Session) deferForScope(conversationID, turnID, id, name string, args map[string]interface{}, persist func() (PendingCall, error)) (PendingCall, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope := conversationID + "\x00" + turnID
	fail := func(err error) (PendingCall, error) {
		if conversationID == "" && turnID == "" {
			s.unscopedErr = err
		} else {
			if s.scopeErrors[scope] == nil {
				s.scopeErrors[scope] = err
			}
		}
		if s.err == nil {
			s.err = err
		}
		return PendingCall{}, err
	}
	if s.unscopedErr != nil {
		return PendingCall{}, s.unscopedErr
	}
	if err := s.scopeErrors[scope]; err != nil {
		return PendingCall{}, err
	}
	if strings.TrimSpace(id) == "" {
		return fail(fmt.Errorf("client tool call id is required"))
	}
	copied, err := clone(args)
	if err != nil {
		return fail(err)
	}
	for _, pending := range s.pending {
		if pending.ID == id && ((conversationID == "" && turnID == "") || (pending.ConversationID == conversationID && pending.TurnID == turnID)) {
			if canonical(pending.Name) != canonical(name) || !reflect.DeepEqual(pending.Arguments, copied) {
				return fail(fmt.Errorf("conflicting duplicate client tool call: %s", id))
			}
			result, _ := clone(pending)
			return result, nil
		}
	}
	pending, err := persist()
	if err != nil {
		return fail(err)
	}
	pending.ID = id
	pending.Name = name
	pending.Arguments = copied
	pending.Metadata = append(json.RawMessage(nil), s.metadata[canonical(name)]...)
	s.pending = append(s.pending, pending)
	result, _ := clone(pending)
	return result, nil
}

func (s *Session) ErrorForTurn(conversationID, turnID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unscopedErr != nil {
		return s.unscopedErr
	}
	return s.scopeErrors[conversationID+"\x00"+turnID]
}
func (s *Session) PendingForTurn(conversationID, turnID string) []PendingCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []PendingCall
	for _, pending := range s.pending {
		if pending.ConversationID == conversationID && pending.TurnID == turnID {
			value, _ := clone(pending)
			result = append(result, value)
		}
	}
	return result
}

func (s *Session) RecordErrorForTurn(conversationID, turnID string, err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
	key := turnKey(conversationID, turnID)
	if s.scopeErrors == nil {
		s.scopeErrors = map[string]error{}
	}
	if s.scopeErrors[key] == nil {
		s.scopeErrors[key] = err
	}
}

// Definition metadata is presentation/routing data, never backend authority or
// provider instruction. Keep it lossless alongside request-scoped definitions.
func (s *Session) SetMetadata(name string, metadata json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byName[canonical(name)]; !ok {
		return fmt.Errorf("unknown client tool: %s", name)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &object); err != nil || object == nil {
		return fmt.Errorf("client tool metadata requires a JSON object")
	}
	if s.metadata == nil {
		s.metadata = map[string]json.RawMessage{}
	}
	s.metadata[canonical(name)] = append(json.RawMessage(nil), metadata...)
	return nil
}
func (s *Session) Metadata(name string) json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(json.RawMessage(nil), s.metadata[canonical(name)]...)
}
