package reportingrun

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/viant/agently-core/model/reportrun"
	"github.com/viant/agently-core/runtime/evidence"
)

func (s *Service) admitForecastCommand(ctx context.Context, input *BeginInput, requested json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	trimmed := bytes.TrimSpace(requested)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if err := json.Unmarshal(requested, &fields); err != nil {
			return nil, err
		}
	}
	if _, supplied := fields[evidence.ForecastCommandNamespace]; supplied {
		return nil, invalid("forecast command namespace is server-owned")
	}
	if strings.TrimSpace(input.ReportAdmissionRef) == "" {
		return requested, nil
	}
	if input.ReportAdmissionRef != strings.TrimSpace(input.ReportAdmissionRef) {
		return nil, invalid("invalid report admission reference")
	}
	if len(trimmed) > 0 && trimmed[0] != '{' && !bytes.Equal(trimmed, []byte("null")) {
		return nil, invalid("linked report requestedParams must be an object")
	}
	if s.reportAdmissions == nil {
		return nil, invalid("report command admission is not configured")
	}
	linkage, err := s.reportAdmissions.AdmitReport(ctx, evidence.ReportAdmissionInput{ConversationID: input.ConversationID, BuilderRef: input.BuilderRef, RequestID: input.UIRunRequestID, AdmissionRef: input.ReportAdmissionRef})
	if err != nil {
		return nil, err
	}
	if !json.Valid(linkage) {
		return nil, invalid("unconfirmed report command admission")
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	fields[evidence.ForecastCommandNamespace] = linkage
	return json.Marshal(fields)
}

func (s *Service) verifyForecastCommand(ctx context.Context, run *reportrun.Record, artifacts evidence.ReportArtifacts) error {
	var fields map[string]json.RawMessage
	trimmed := bytes.TrimSpace(run.RequestedParams)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if err := json.Unmarshal(run.RequestedParams, &fields); err != nil {
			return err
		}
	}
	linkage, present := fields[evidence.ForecastCommandNamespace]
	if !present {
		return nil
	}
	if s.reportAdmissions == nil {
		return invalid("report command verification is not configured")
	}
	return s.reportAdmissions.VerifyReport(ctx, run.ConversationID, linkage, artifacts)
}
