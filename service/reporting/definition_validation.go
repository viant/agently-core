package reporting

import (
	"context"
	"encoding/json"
	"fmt"
)

// ValidateDefinition delegates candidate checking to the same configured engine
// used for report execution. No report is stored and no datasource is invoked.
func (s *Service) ValidateDefinition(ctx context.Context, uri string, definition json.RawMessage) error {
	if s == nil || s.execution == nil {
		return fmt.Errorf("report compilation is unavailable")
	}
	return s.execution.ValidateDefinition(ctx, uri, definition)
}

// ValidatePersonalReportCandidate checks complete personal customizations at
// the owning writer. Generic draft authoring retains its existing contract.
func ValidatePersonalReportCandidate(ctx context.Context, candidate *SharedArtifact, validate func(context.Context, string, json.RawMessage) error) error {
	if candidate == nil {
		return fmt.Errorf("report candidate required")
	}
	if !candidate.IsPersonalCopy() {
		return nil
	}
	if validate == nil {
		return fmt.Errorf("report compilation is unavailable")
	}
	if err := validate(ctx, candidate.ArtifactRef, candidate.ResourceDefinition); err != nil {
		return fmt.Errorf("personal report save phase=compile: %w", err)
	}
	return nil
}
