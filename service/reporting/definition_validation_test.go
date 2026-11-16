package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCandidateValidationUsesConfiguredReportEngine(t *testing.T) {
	store := NewMemoryStore()
	service := New(Options{Store: store, Compiler: NewReportSpecCompiler(nil)})
	raw, err := os.ReadFile("testdata/synthetic_authored_report.json")
	require.NoError(t, err)
	require.NoError(t, service.ValidateDefinition(context.Background(), "report://example/summary", raw))
	require.Error(t, service.ValidateDefinition(context.Background(), "report://example/summary", json.RawMessage(`{"schemaVersion":1,"reportSpec":{}}`)))
	rows, err := store.ListSharedArtifacts(context.Background())
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestPersonalCandidateValidationPreservesCompilerCause(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("compiler dependency unavailable")} {
		t.Run(cause.Error(), func(t *testing.T) {
			raw := json.RawMessage(`{"schemaVersion":1,"reportSpec":{}}`)
			candidate := &SharedArtifact{ArtifactRef: "report://example/personal", ResourceDefinition: raw, personalSource: &PersonalReportSource{}}
			calls := 0
			err := ValidatePersonalReportCandidate(context.Background(), candidate, func(_ context.Context, uri string, definition json.RawMessage) error {
				calls++
				require.Equal(t, candidate.ArtifactRef, uri)
				require.Equal(t, raw, definition)
				return cause
			})
			require.ErrorIs(t, err, cause)
			require.Equal(t, 1, calls, "reuse the configured compiler exactly once")
			require.Equal(t, raw, candidate.ResourceDefinition)
		})
	}
}
