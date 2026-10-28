package workspaceeval

import (
	"context"
	"fmt"

	"github.com/viant/agently-core/protocol/evaluation"
)

// EvalReader allows the behavioral runner to consume the same admitted
// descriptors as the runtime. A configured reader is authoritative; failures
// never retry the legacy workspace directory.
type EvalReader interface {
	LoadAll(context.Context) ([]*evaluation.Definition, error)
}

func LoadEvalDocsWithReader(ctx context.Context, reader EvalReader) (map[string]EvalDoc, []string) {
	if reader == nil {
		return nil, []string{"eval reader is required"}
	}
	rows, err := reader.LoadAll(ctx)
	if err != nil {
		return nil, []string{err.Error()}
	}
	result := map[string]EvalDoc{}
	for _, row := range rows {
		if row == nil || row.ID == "" {
			return nil, []string{"eval descriptor is missing its identity"}
		}
		if _, duplicate := result[row.ID]; duplicate {
			return nil, []string{fmt.Sprintf("duplicate eval identity %q", row.ID)}
		}
		result[row.ID] = *row
	}
	return result, nil
}
