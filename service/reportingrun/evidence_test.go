package reportingrun

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/evidence"
	authsvc "github.com/viant/agently-core/service/auth"
)

type admissionVerifier struct {
	fail     bool
	verified int
}

func (v *admissionVerifier) AdmitReport(_ context.Context, input evidence.ReportAdmissionInput) (json.RawMessage, error) {
	if input.AdmissionRef != "server-reference" || input.RequestID != "server-request" || input.BuilderRef != "builder" || input.ConversationID != "conversation" {
		return nil, fmt.Errorf("receipt substitution")
	}
	return json.RawMessage(`{"version":1,"ref":"server-reference","requestId":"server-request"}`), nil
}
func (v *admissionVerifier) VerifyReport(_ context.Context, conversation string, link json.RawMessage, artifacts evidence.ReportArtifacts) error {
	v.verified++
	if v.fail || conversation != "conversation" {
		return fmt.Errorf("artifact proof mismatch")
	}
	return nil
}
func TestReportCommandLinkageRejectsForgeryAndCompletionIsAtomic(t *testing.T) {
	service, _ := newTestService(t)
	verifier := &admissionVerifier{}
	service.reportAdmissions = verifier
	ctx := authsvc.InjectUser(context.Background(), "owner")
	input := &BeginInput{ReportAdmissionRef: "server-reference", UIRunRequestID: "server-request", ConversationID: "conversation", BuilderRef: "builder", Origin: "prompt", RequestedParams: json.RawMessage(`{"filters":{"id":1}}`)}
	result, err := service.Begin(ctx, input)
	require.NoError(t, err)
	require.JSONEq(t, `{"filters":{"id":1},"_agentlyForecastCommand":{"version":1,"ref":"server-reference","requestId":"server-request"}}`, string(result.Run.RequestedParams))
	repeated, err := service.Begin(ctx, input)
	require.NoError(t, err)
	require.Equal(t, result.Run.ReportRunID, repeated.Run.ReportRunID)
	replay := *input
	replay.UIRunRequestID = "another-request"
	_, err = service.Begin(ctx, &replay)
	require.ErrorContains(t, err, "receipt substitution")
	forged := *input
	forged.RequestedParams = result.Run.RequestedParams
	_, err = service.Begin(ctx, &forged)
	require.ErrorContains(t, err, "server-owned")
	completion := &CompleteInput{ReportRunID: result.Run.ReportRunID, ConversationID: "conversation", ExpectedRevision: result.Run.Revision, ReportSpec: testSpec, ReportFill: testFill, ReportPrint: testPrint}
	verifier.fail = true
	_, err = service.Complete(ctx, completion)
	require.ErrorContains(t, err, "artifact proof mismatch")
	still, err := service.GetRun(ctx, result.Run.ReportRunID, "conversation")
	require.NoError(t, err)
	require.Equal(t, "running", still.Status)
	require.Equal(t, result.Run.Revision, still.Revision)
	verifier.fail = false
	_, err = service.Complete(ctx, completion)
	require.NoError(t, err)
	verifier.fail = true
	_, err = service.Complete(ctx, completion)
	require.ErrorContains(t, err, "artifact proof mismatch", "idempotence must revalidate a linked receipt")
}

func TestManualReportParamsRemainUnlinkedAndUnchanged(t *testing.T) {
	service, _ := newTestService(t)
	ctx := authsvc.InjectUser(context.Background(), "owner")
	result, err := service.Begin(ctx, &BeginInput{UIRunRequestID: "manual", Origin: "manual", RequestedParams: json.RawMessage(`[1,"existing arbitrary JSON"]`)})
	require.NoError(t, err)
	require.JSONEq(t, `[1,"existing arbitrary JSON"]`, string(result.Run.RequestedParams))
	_, err = service.Begin(ctx, &BeginInput{UIRunRequestID: "forged-manual", Origin: "manual", RequestedParams: json.RawMessage(`{"_agentlyForecastCommand":null}`)})
	require.ErrorContains(t, err, "server-owned")
}
