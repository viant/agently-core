package evidence

import (
	"context"
	"encoding/json"
)

const ForecastCommandNamespace = "_agentlyForecastCommand"

type ReportCommandTarget struct {
	WindowID  string
	Workspace json.RawMessage
}
type ReportCommand struct {
	RequestID    string `json:"requestId"`
	AdmissionRef string `json:"reportAdmissionRef"`
}
type ReportCommands interface {
	IssueReportCommand(context.Context, ReportCommandTarget) (*ReportCommand, error)
}
type reportCommandsKey struct{}

func WithReportCommands(ctx context.Context, commands ReportCommands) context.Context {
	return context.WithValue(ctx, reportCommandsKey{}, commands)
}
func ReportCommandsFromContext(ctx context.Context) ReportCommands {
	value, _ := ctx.Value(reportCommandsKey{}).(ReportCommands)
	return value
}

type ReportAdmissionInput struct {
	ConversationID string
	BuilderRef     string
	RequestID      string
	AdmissionRef   string
}
type ReportArtifacts struct {
	Spec  json.RawMessage
	Fill  json.RawMessage
	Print json.RawMessage
}

// ReportAdmissions is injected in the authenticated HTTP report lifecycle. The
// caller supplies only an opaque reference; implementations load all authority
// from the immutable native command receipt, not from an active-turn guess.
type ReportAdmissions interface {
	AdmitReport(context.Context, ReportAdmissionInput) (json.RawMessage, error)
	VerifyReport(context.Context, string, json.RawMessage, ReportArtifacts) error
}

// ReportCompilation brackets the trusted pure compiler. RecordCompiled receives
// only server-produced canonical artifacts, never a client completion payload.
type ReportCompilation interface {
	CompileContext(context.Context, string, string) (context.Context, error)
	RecordCompiled(context.Context, string, ReportArtifacts) error
}
