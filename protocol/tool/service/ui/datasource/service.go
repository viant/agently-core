package datasource

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

const Name = "ui/datasource"

type PeekInput struct {
	ClientID      string `json:"clientId,omitempty"`
	WindowID      string `json:"windowId,omitempty"`
	WindowKey     string `json:"windowKey,omitempty"`
	DataSourceRef string `json:"dataSourceRef"`
}

type PeekOutput struct {
	ClientID      string                    `json:"clientId,omitempty"`
	WindowID      string                    `json:"windowId,omitempty"`
	WindowKey     string                    `json:"windowKey,omitempty"`
	DataSourceRef string                    `json:"dataSourceRef,omitempty"`
	Snapshot      *uireg.DataSourceSnapshot `json:"snapshot,omitempty"`
}

type Service struct {
	reg *uireg.Registry
}

func New(bridge *forgeuisvc.Service) *Service {
	return &Service{reg: uireg.New(bridge)}
}

func (s *Service) Name() string { return Name }

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "peek", Description: "Return the live datasource snapshot the current UI window is showing.", Input: reflect.TypeOf(&PeekInput{}), Output: reflect.TypeOf(&PeekOutput{})},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "peek":
		return s.peek, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

func (s *Service) peek(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*PeekInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*PeekOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	clientID, _, win, err := s.reg.FindWindow(ctx, conversationID, input.ClientID, input.WindowID, input.WindowKey)
	if err != nil {
		return err
	}
	ref := strings.TrimSpace(input.DataSourceRef)
	if ref == "" {
		return fmt.Errorf("dataSourceRef is required")
	}
	snap, ok := win.DataSources[ref]
	if !ok {
		return fmt.Errorf("datasource %q not found on window", ref)
	}
	output.ClientID = clientID
	output.WindowID = win.WindowID
	output.WindowKey = win.WindowKey
	output.DataSourceRef = ref
	output.Snapshot = &snap
	return nil
}
