package user

import (
	"encoding/json"
	"errors"
	forgeresource "github.com/viant/forge/backend/resource"

	"github.com/viant/agently-core/service/reporting"
	"github.com/viant/forge/backend/reporting/registry"
	resourcehost "github.com/viant/forge/backend/resourcehost"
)

func decodeUserReport(raw json.RawMessage) (registry.ReportEnvelope, error) {
	var envelope registry.ReportEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return envelope, err
	}
	if err := resourcehost.ValidateReportEnvelope(int64(envelope.SchemaVersion), raw); err != nil {
		return envelope, err
	}
	return envelope, nil
}
func userReportDefinition(row *reporting.SharedArtifact) (json.RawMessage, error) {
	raw, _, err := userReportDefinitionAndVersion(row)
	return raw, err
}

func userReportDefinitionAndVersion(row *reporting.SharedArtifact) (json.RawMessage, int64, error) {
	raw := row.ResourceDefinition
	if len(raw) == 0 {
		var err error
		raw, err = json.Marshal(registry.ReportEnvelope{SchemaVersion: 1, ReportDocument: cloneReportJSON(row.Document), ReportSpec: cloneReportJSON(row.ReportSpec), ReportFill: cloneReportJSON(row.ReportFill), ReportPrint: cloneReportJSON(row.ReportPrint), CompileState: cloneReportJSON(row.CompileState), Metadata: cloneReportJSON(row.Metadata)})
		if err != nil {
			return nil, 0, errors.Join(forgeresource.ErrStoredDefinition, err)
		}
	}
	envelope, err := decodeUserReport(raw)
	if err != nil {
		return nil, 0, errors.Join(forgeresource.ErrStoredDefinition, err)
	}
	// The delivery projection only changes provenance metadata.
	projected, err := reporting.UserReportDefinition(row, raw)
	if err != nil {
		return nil, 0, errors.Join(forgeresource.ErrStoredDefinition, err)
	}
	return projected, int64(envelope.SchemaVersion), nil
}
