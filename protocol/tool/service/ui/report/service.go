package report

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

const Name = "ui/report"

const toolTimeout = 330 * time.Second

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

type TerminalRunWaiter interface {
	WaitTerminal(ctx context.Context, reportRunID, conversationID string) (*reportrun.Record, error)
}

type Option func(*Service)

// WithOrchestration enables the T3 run-only durable continuation. A nil
// waiter remains fail-closed and causes run to return a configuration error.
func WithOrchestration(waiter TerminalRunWaiter) Option {
	return func(service *Service) {
		service.orchestration = true
		service.reportRuns = waiter
	}
}

type Service struct {
	bridge        *forgeuisvc.Service
	reg           *uireg.Registry
	orchestration bool
	reportRuns    TerminalRunWaiter
}

func New(bridge *forgeuisvc.Service, options ...Option) *Service {
	service := &Service{bridge: bridge, reg: uireg.New(bridge)}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

func (s *Service) Name() string { return Name }

func (s *Service) MethodToolTimeout(method string) time.Duration {
	if strings.EqualFold(strings.TrimSpace(method), "run") {
		return toolTimeout
	}
	return 0
}

func (s *Service) MethodRetryable(method string) (bool, bool) {
	if strings.EqualFold(strings.TrimSpace(method), "run") {
		return false, true
	}
	return false, false
}

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
	if err := s.execute(ctx, "run", "report.run_requested", in, out); err != nil {
		return err
	}
	if !s.orchestration {
		return nil
	}
	output, ok := out.(*ActionOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	return s.waitForDurableRun(ctx, output)
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

func (s *Service) waitForDurableRun(ctx context.Context, output *ActionOutput) error {
	if s.reportRuns == nil {
		return fmt.Errorf("ui report orchestration is enabled but durable report-run service is not configured")
	}
	if output == nil || !output.OK {
		message := "browser report run failed"
		if output != nil && strings.TrimSpace(output.Error) != "" {
			message += ": " + strings.TrimSpace(output.Error)
		}
		return fmt.Errorf("%s", message)
	}
	if output.Result["materialized"] == true {
		status, _ := output.Result["status"].(string)
		materializationID, _ := output.Result["materializationId"].(string)
		if strings.EqualFold(strings.TrimSpace(status), "completed") && strings.TrimSpace(materializationID) != "" {
			output.OK = true
			output.Error = ""
			return nil
		}
		return fmt.Errorf("native report materialization did not return an exact completed materializationId")
	}
	if output.Result["accepted"] == true && strings.EqualFold(stringResult(output.Result, "status"), "running") {
		return s.waitForNativeMaterialization(ctx, output)
	}
	if output.Result == nil || output.Result["durable"] != true {
		return fmt.Errorf("browser report run did not return durable=true")
	}
	reportRunID, _ := output.Result["reportRunId"].(string)
	reportRunID = strings.TrimSpace(reportRunID)
	if reportRunID == "" {
		return fmt.Errorf("browser report run did not return an exact reportRunId")
	}
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	if conversationID == "" {
		return fmt.Errorf("trusted current conversation is required for durable report run")
	}

	run, err := s.reportRuns.WaitTerminal(ctx, reportRunID, conversationID)
	if err != nil {
		return fmt.Errorf("wait for durable report run %q: %w", reportRunID, err)
	}
	if run == nil ||
		strings.TrimSpace(run.ReportRunID) != reportRunID ||
		strings.TrimSpace(run.ConversationID) != conversationID {
		return fmt.Errorf("durable report-run service returned a mismatched run")
	}
	status := strings.ToLower(strings.TrimSpace(run.Status))
	output.Result["reportRunId"] = reportRunID
	output.Result["durable"] = true
	output.Result["status"] = status
	output.Result["revision"] = run.Revision
	switch status {
	case reportrun.StatusCompleted:
		output.OK = true
		output.Error = ""
		return nil
	case reportrun.StatusFailed:
		output.OK = false
		output.Error = strings.TrimSpace(run.FailureText)
		if output.Error == "" {
			output.Error = "report run failed"
		}
		return fmt.Errorf("durable report run %q failed: %s", reportRunID, output.Error)
	default:
		return fmt.Errorf("durable report run %q returned non-terminal status %q", reportRunID, status)
	}
}

func (s *Service) waitForNativeMaterialization(ctx context.Context, output *ActionOutput) error {
	expectedID := stringResult(output.Result, "materializationId")
	windowID := stringResult(output.Result, "windowId")
	if expectedID == "" || windowID == "" {
		return fmt.Errorf("native report run did not return an exact windowId and materializationId")
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for native report materialization %q: %w", expectedID, ctx.Err())
		case <-ticker.C:
		}
		inspection := &ActionOutput{}
		if err := s.execute(ctx, "getCurrent", "report.run_polled", &WindowInput{
			ClientID: output.ClientID,
			WindowID: windowID,
		}, inspection); err != nil {
			return fmt.Errorf("inspect native report materialization %q: %w", expectedID, err)
		}
		materialization, _ := inspection.Result["materialization"].(map[string]interface{})
		if len(materialization) == 0 {
			continue
		}
		actualID := stringResult(materialization, "id", "requestId", "materializationId")
		if actualID != expectedID {
			continue
		}
		status := strings.ToLower(stringResult(materialization, "status"))
		switch status {
		case "completed":
			output.OK = true
			output.Error = ""
			output.Result["materialized"] = true
			output.Result["status"] = status
			for _, key := range []string{"datasetRefs", "rowCounts"} {
				if value, ok := materialization[key]; ok {
					output.Result[key] = value
				}
			}
			return nil
		case "failed":
			output.OK = false
			output.Error = stringSliceResult(materialization, "errors")
			if output.Error == "" {
				output.Error = "native report materialization failed"
			}
			return fmt.Errorf("native report materialization %q failed: %s", expectedID, output.Error)
		}
	}
}

func stringResult(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func stringSliceResult(values map[string]interface{}, key string) string {
	raw, ok := values[key].([]interface{})
	if !ok {
		return ""
	}
	items := make([]string, 0, len(raw))
	for _, item := range raw {
		if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
			items = append(items, strings.TrimSpace(value))
		}
	}
	return strings.Join(items, "; ")
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
