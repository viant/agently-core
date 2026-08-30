package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

const Name = "ui/feed"

type Operation struct {
	DataSourceRef string      `json:"dataSourceRef"`
	Op            string      `json:"op"`
	Path          string      `json:"path"`
	Value         interface{} `json:"value,omitempty"`
}

type UpdateInput struct {
	ClientID   string      `json:"clientId,omitempty"`
	FeedID     string      `json:"feedId"`
	Operations []Operation `json:"operations"`
}

type UpdateOutput struct {
	ClientID string `json:"clientId,omitempty"`
	OK       bool   `json:"ok,omitempty"`
	Error    string `json:"error,omitempty"`
}

type GetInput struct {
	ClientID       string   `json:"clientId,omitempty"`
	FeedID         string   `json:"feedId"`
	DataSourceRefs []string `json:"dataSourceRefs"`
}

type GetOutput struct {
	ClientID string      `json:"clientId,omitempty"`
	Data     interface{} `json:"data,omitempty"`
}

type Service struct {
	bridge *forgeuisvc.Service
	reg    *uireg.Registry
}

func New(bridge *forgeuisvc.Service) *Service {
	return &Service{bridge: bridge, reg: uireg.New(bridge)}
}
func (s *Service) Name() string { return Name }
func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "get", Description: "Read live form, collection, and selection state from named datasources of an active Tool Feed.", Input: reflect.TypeOf(&GetInput{}), Output: reflect.TypeOf(&GetOutput{})},
		{Name: "update", Description: "Apply preview-only JSON Pointer add, replace, or remove operations to an active Tool Feed and move it to the current turn.", Input: reflect.TypeOf(&UpdateInput{}), Output: reflect.TypeOf(&UpdateOutput{})},
	}
}
func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "get":
		return s.get, nil
	case "update":
		return s.update, nil
	}
	return nil, svc.NewMethodNotFoundError(name)
}

func (s *Service) client(ctx context.Context, requested string) (*uireg.ClientSnapshot, string, error) {
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	if conversationID == "" {
		return nil, "", fmt.Errorf("conversation id is required")
	}
	clients, err := s.reg.ListAttachedByConversation(ctx, conversationID)
	if err != nil {
		return nil, "", err
	}
	if len(clients) == 0 {
		return nil, "", fmt.Errorf("no attached UI client for conversation")
	}
	preferred := strings.TrimSpace(requested)
	if preferred == "" {
		preferred = strings.TrimSpace(runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	}
	selected := clients[0]
	for _, candidate := range clients {
		if preferred != "" && candidate.ClientID == preferred {
			selected = candidate
			break
		}
	}
	return &selected, conversationID, nil
}

func (s *Service) get(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*GetInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*GetOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	if strings.TrimSpace(input.FeedID) == "" || len(input.DataSourceRefs) == 0 || len(input.DataSourceRefs) > 32 {
		return fmt.Errorf("feedId and between 1 and 32 dataSourceRefs are required")
	}
	selected, conversationID, err := s.client(ctx, input.ClientID)
	if err != nil {
		return err
	}
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{ClientID: selected.ClientID, Namespace: selected.Namespace, Method: "ui.feed.get", Params: map[string]interface{}{
		"conversationId": conversationID,
		"feedId":         strings.TrimSpace(input.FeedID),
		"dataSourceRefs": input.DataSourceRefs,
	}})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("ui feed read failed: %s", strings.TrimSpace(resp.Error))
	}
	var data interface{}
	if len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, &data); err != nil {
			return err
		}
	}
	output.ClientID = selected.ClientID
	output.Data = data
	return nil
}

func (s *Service) update(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*UpdateInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*UpdateOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	feedID := strings.TrimSpace(input.FeedID)
	if feedID == "" {
		return fmt.Errorf("feedId is required")
	}
	if len(input.Operations) == 0 || len(input.Operations) > 64 {
		return fmt.Errorf("operations must contain between 1 and 64 items")
	}
	for i, operation := range input.Operations {
		op := strings.ToLower(strings.TrimSpace(operation.Op))
		if op != "add" && op != "replace" && op != "remove" {
			return fmt.Errorf("operations[%d].op must be add, replace, or remove", i)
		}
		if strings.TrimSpace(operation.DataSourceRef) == "" || !strings.HasPrefix(strings.TrimSpace(operation.Path), "/") {
			return fmt.Errorf("operations[%d] requires dataSourceRef and an absolute JSON Pointer path", i)
		}
	}
	selected, conversationID, err := s.client(ctx, input.ClientID)
	if err != nil {
		return err
	}
	turnID := ""
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		turnID = strings.TrimSpace(turn.TurnID)
	}
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  selected.ClientID,
		Namespace: selected.Namespace,
		Method:    "ui.feed.update",
		Params: map[string]interface{}{
			"conversationId": conversationID,
			"turnId":         turnID,
			"feedId":         feedID,
			"operations":     input.Operations,
		},
	})
	if err != nil {
		return err
	}
	output.ClientID = selected.ClientID
	output.OK = resp.OK
	output.Error = resp.Error
	return nil
}
