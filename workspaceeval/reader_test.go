package workspaceeval

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/evaluation"
)

type syntheticEvalReader struct {
	calls   int
	failure error
	rows    []*evaluation.Definition
}

func (r *syntheticEvalReader) LoadAll(context.Context) ([]*evaluation.Definition, error) {
	r.calls++
	return r.rows, r.failure
}
func TestBehavioralRunnerUsesAuthoritativeEvalReader(t *testing.T) {
	denied := errors.New("synthetic cohort admission lost")
	reader := &syntheticEvalReader{failure: denied}
	failures := runBehavioralEvalsWithReader(context.Background(), reader, t.TempDir(), t.TempDir(), "review", time.Second, "https://example.invalid", "", "", "agently")
	require.Equal(t, []string{denied.Error()}, failures)
	require.Equal(t, 1, reader.calls, "configured reader failure must not retry a workspace file")
}
func TestLoadEvalDocsWithReaderPreservesDescriptorContract(t *testing.T) {
	definition := &evaluation.Definition{SchemaVersion: 1, ID: "review", UserPrompt: "Review a synthetic record.", EntryAgent: "agent://example/review"}
	definition.ExpectedRouting.Agent = "agent://example/review"
	definition.ExpectedRouting.Profile = "review"
	definition.ExpectedOutput.Template = "summary"
	reader := &syntheticEvalReader{rows: []*evaluation.Definition{definition}}
	index, failures := LoadEvalDocsWithReader(context.Background(), reader)
	require.Empty(t, failures)
	require.Equal(t, *definition, index["review"])
}
