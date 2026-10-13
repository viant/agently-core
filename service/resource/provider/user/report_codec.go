package user

import (
	"bytes"
	"encoding/json"
	"io"

	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/reporting"
	"github.com/viant/forge/backend/reporting/registry"
)

func decodeUserReport(raw json.RawMessage) (registry.ReportEnvelope, error) {
	var envelope registry.ReportEnvelope
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&envelope); err != nil {
		return envelope, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return envelope, identity.ErrResource
	}
	object := func(v json.RawMessage) bool {
		var value map[string]json.RawMessage
		return json.Unmarshal(v, &value) == nil && value != nil
	}
	if envelope.SchemaVersion != 1 || !object(envelope.ReportDocument) && !object(envelope.ReportSpec) || len(envelope.ReportDocument) > 0 && !object(envelope.ReportDocument) || (len(envelope.ReportSpec) > 0 && !object(envelope.ReportSpec)) {
		return envelope, identity.ErrResource
	}
	switch envelope.Format {
	case "":
		if len(envelope.BuilderDefinition) != 0 {
			return envelope, identity.ErrResource
		}
	case registry.AuthoredReportFormat:
		if !object(envelope.ReportDocument) || envelope.BuilderRef == "" || !object(envelope.BuilderDefinition) {
			return envelope, identity.ErrResource
		}
	default:
		return envelope, identity.ErrResource
	}
	return envelope, nil
}
func userReportDefinition(row *reporting.SharedArtifact) (json.RawMessage, error) {
	if len(row.ResourceDefinition) > 0 {
		if _, err := decodeUserReport(row.ResourceDefinition); err != nil {
			return nil, err
		}
		return cloneReportJSON(row.ResourceDefinition), nil
	}
	raw, err := json.Marshal(registry.ReportEnvelope{SchemaVersion: 1, ReportDocument: cloneReportJSON(row.Document), ReportSpec: cloneReportJSON(row.ReportSpec), ReportFill: cloneReportJSON(row.ReportFill), ReportPrint: cloneReportJSON(row.ReportPrint), CompileState: cloneReportJSON(row.CompileState), Metadata: cloneReportJSON(row.Metadata)})
	if err != nil {
		return nil, err
	}
	if _, err = decodeUserReport(raw); err != nil {
		return nil, err
	}
	return raw, nil
}
