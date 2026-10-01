package conversation

import (
	"context"
	generatedfilemodel "github.com/viant/agently-core/model/generatedfile"
)

// GeneratedFileClient is an optional extension implemented by concrete
// conversation clients that support generated-file persistence.
type GeneratedFileClient interface {
	GetGeneratedFiles(ctx context.Context, input *generatedfilemodel.Input) ([]*generatedfilemodel.GeneratedFileView, error)
	PatchGeneratedFile(ctx context.Context, generatedFile *generatedfilemodel.GeneratedFile) error
}
