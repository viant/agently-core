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

// ActionResult is the typed view of the report bridge result. ActionOutput
// keeps the raw map to preserve forward-compatible UI payloads and the exact
// JSON wire shape; orchestration code uses this struct instead of ad-hoc keys.
type ActionResult struct {
	OK                *bool                  `json:"ok,omitempty"`
	Error             string                 `json:"error,omitempty"`
	WindowID          string                 `json:"windowId,omitempty"`
	ReportID          string                 `json:"reportId,omitempty"`
	ReportName        string                 `json:"reportName,omitempty"`
	ArtifactID        string                 `json:"artifactId,omitempty"`
	Request           map[string]interface{} `json:"request,omitempty"`
	CanRun            *bool                  `json:"canRun,omitempty"`
	CanSave           *bool                  `json:"canSave,omitempty"`
	HasCompletedRun   *bool                  `json:"hasCompletedRun,omitempty"`
	Accepted          *bool                  `json:"accepted,omitempty"`
	Materialized      *bool                  `json:"materialized,omitempty"`
	MaterializationID string                 `json:"materializationId,omitempty"`
	Status            string                 `json:"status,omitempty"`
	Durable           *bool                  `json:"durable,omitempty"`
	ReportRunID       string                 `json:"reportRunId,omitempty"`
	Revision          *int64                 `json:"revision,omitempty"`
	DatasetRefs       []string               `json:"datasetRefs,omitempty"`
	RowCounts         map[string]int64       `json:"rowCounts,omitempty"`
	Materialization   *MaterializationResult `json:"materialization,omitempty"`
}

type MaterializationResult struct {
	ID                string           `json:"id,omitempty"`
	RequestID         string           `json:"requestId,omitempty"`
	MaterializationID string           `json:"materializationId,omitempty"`
	Status            string           `json:"status,omitempty"`
	DatasetRefs       []string         `json:"datasetRefs,omitempty"`
	RowCounts         map[string]int64 `json:"rowCounts,omitempty"`
	Errors            []string         `json:"errors,omitempty"`
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
		result, err := decodeActionResult(output.Result)
		if err != nil {
			return fmt.Errorf("decode typed ui.report.%s result: %w", action, err)
		}
		if result.OK != nil {
			output.OK = output.OK && *result.OK
		}
		if resultError := strings.TrimSpace(result.Error); resultError != "" {
			output.Error = resultError
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
	result, err := decodeActionResult(output.Result)
	if err != nil {
		return fmt.Errorf("decode report run result: %w", err)
	}
	if boolResult(result.Materialized) {
		if strings.EqualFold(strings.TrimSpace(result.Status), "completed") && strings.TrimSpace(result.MaterializationID) != "" {
			output.OK = true
			output.Error = ""
			return nil
		}
		return fmt.Errorf("native report materialization did not return an exact completed materializationId")
	}
	if boolResult(result.Accepted) && strings.EqualFold(strings.TrimSpace(result.Status), "running") {
		return s.waitForNativeMaterialization(ctx, output, result)
	}
	if !boolResult(result.Durable) {
		return fmt.Errorf("browser report run did not return durable=true")
	}
	reportRunID := strings.TrimSpace(result.ReportRunID)
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
	if err := output.mergeResult(ActionResult{
		ReportRunID: reportRunID,
		Durable:     boolResultPointer(true),
		Status:      status,
		Revision:    int64ResultPointer(run.Revision),
	}); err != nil {
		return fmt.Errorf("update durable report run result: %w", err)
	}
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

func (s *Service) waitForNativeMaterialization(ctx context.Context, output *ActionOutput, result *ActionResult) error {
	expectedID := strings.TrimSpace(result.MaterializationID)
	windowID := strings.TrimSpace(result.WindowID)
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
		inspectionResult, err := decodeActionResult(inspection.Result)
		if err != nil {
			return fmt.Errorf("decode native report materialization %q: %w", expectedID, err)
		}
		materialization := inspectionResult.Materialization
		if materialization == nil {
			continue
		}
		actualID := firstNonEmpty(materialization.ID, materialization.RequestID, materialization.MaterializationID)
		if actualID != expectedID {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(materialization.Status))
		switch status {
		case "completed":
			output.OK = true
			output.Error = ""
			if err := output.mergeResult(ActionResult{
				Materialized: boolResultPointer(true),
				Status:       status,
				DatasetRefs:  materialization.DatasetRefs,
				RowCounts:    materialization.RowCounts,
			}); err != nil {
				return fmt.Errorf("update native report materialization %q: %w", expectedID, err)
			}
			return nil
		case "failed":
			output.OK = false
			output.Error = strings.Join(materialization.Errors, "; ")
			if output.Error == "" {
				output.Error = "native report materialization failed"
			}
			return fmt.Errorf("native report materialization %q failed: %s", expectedID, output.Error)
		}
	}
}

func decodeActionResult(values map[string]interface{}) (*ActionResult, error) {
	result := &ActionResult{}
	if len(values) == 0 {
		return result, nil
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(payload, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (o *ActionOutput) mergeResult(patch ActionResult) error {
	payload, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	values := map[string]interface{}{}
	if err := json.Unmarshal(payload, &values); err != nil {
		return err
	}
	// Keep Go-side numeric identity stable for callers inspecting ActionOutput
	// before it is serialized; JSON encoding still emits the same number.
	if patch.Revision != nil {
		values["revision"] = *patch.Revision
	}
	if o.Result == nil {
		o.Result = map[string]interface{}{}
	}
	for key, value := range values {
		o.Result[key] = value
	}
	return nil
}

func boolResult(value *bool) bool { return value != nil && *value }

func boolResultPointer(value bool) *bool { return &value }

func int64ResultPointer(value int64) *int64 { return &value }

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
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
