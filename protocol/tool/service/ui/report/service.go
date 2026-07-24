package report

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

const Name = "ui/report"

type WindowInput struct {
	ClientID  string `json:"clientId,omitempty"`
	WindowID  string `json:"windowId,omitempty"`
	WindowKey string `json:"windowKey,omitempty"`
}

type ActionOutput struct {
	ClientID string                 `json:"clientId,omitempty"`
	OK       bool                   `json:"ok"`
	Error    string                 `json:"error,omitempty"`
	Result   map[string]interface{} `json:"result,omitempty"`
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
	input := reflect.TypeOf(&WindowInput{})
	output := reflect.TypeOf(&ActionOutput{})
	return []svc.Signature{
		{Name: "getCurrent", Description: "Inspect the canonical report currently shown in a live report-builder window, including its identity, current filters, and run/save readiness.", Input: input, Output: output},
		{Name: "run", Description: "Run the canonical report currently shown in a live report-builder window with its current filter values.", Input: input, Output: output},
		{Name: "save", Description: "Persist the canonical report currently shown in a live report-builder window and return its stable reportId and artifactId.", Input: input, Output: output},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "getcurrent":
		return s.getCurrent, nil
	case "run":
		return s.run, nil
	case "save":
		return s.save, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

func (s *Service) getCurrent(ctx context.Context, in, out interface{}) error {
	return s.execute(ctx, "getCurrent", "report.inspect", in, out)
}

func (s *Service) run(ctx context.Context, in, out interface{}) error {
	return s.execute(ctx, "run", "report.run_requested", in, out)
}

func (s *Service) save(ctx context.Context, in, out interface{}) error {
	return s.execute(ctx, "save", "report.save_requested", in, out)
}

func (s *Service) execute(ctx context.Context, action, eventKind string, in, out interface{}) error {
	input, ok := in.(*WindowInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ActionOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	if s.bridge == nil {
		return fmt.Errorf("ui bridge not configured")
	}
	clientID, namespace, window, err := s.resolveWindow(ctx, input)
	if err != nil {
		return err
	}
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  clientID,
		Namespace: namespace,
		Method:    "ui.report." + action,
		Params: map[string]interface{}{
			"windowId": strings.TrimSpace(window.WindowID),
		},
	})
	if err != nil {
		return err
	}
	output.ClientID = clientID
	output.OK = resp.OK
	output.Error = resp.Error
	if len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, &output.Result); err != nil {
			return fmt.Errorf("decode ui.report.%s result: %w", action, err)
		}
		if resultOK, exists := output.Result["ok"].(bool); exists {
			output.OK = output.OK && resultOK
		}
		if resultError, exists := output.Result["error"].(string); exists && strings.TrimSpace(resultError) != "" {
			output.Error = strings.TrimSpace(resultError)
		}
	}
	s.reg.RecordEvent(namespace, clientID, uireg.UIEvent{
		ConversationID: strings.TrimSpace(window.ConversationID),
		ClientID:       clientID,
		WindowID:       strings.TrimSpace(window.WindowID),
		WindowKey:      strings.TrimSpace(window.WindowKey),
		Kind:           eventKind,
		Actor:          "agent",
		Detail:         output.Result,
	})
	return nil
}

func (s *Service) resolveWindow(ctx context.Context, input *WindowInput) (string, string, *uireg.WindowSnapshot, error) {
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	clientID := normalizeOptionalClientID(input.ClientID)
	if clientID == "" {
		clientID = normalizeOptionalClientID(runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	}
	resolvedClientID, namespace, _, window, err := s.reg.FindWindow(
		ctx,
		conversationID,
		clientID,
		strings.TrimSpace(input.WindowID),
		strings.TrimSpace(input.WindowKey),
	)
	if err != nil {
		resolvedClientID, namespace, _, window, err = s.reg.FindReadableWindow(
			ctx,
			conversationID,
			clientID,
			strings.TrimSpace(input.WindowID),
			strings.TrimSpace(input.WindowKey),
		)
	}
	if err != nil {
		return "", "", nil, err
	}
	if window == nil || strings.TrimSpace(window.WindowID) == "" {
		return "", "", nil, fmt.Errorf("report window is required")
	}
	return resolvedClientID, namespace, window, nil
}

func normalizeOptionalClientID(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.EqualFold(value, "default") {
		return ""
	}
	return value
}
