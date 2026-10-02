package conversation

import (
	"context"
	"fmt"
	"reflect"

	read "github.com/viant/agently-core/internal/datly/generatedfile/read"
	write "github.com/viant/agently-core/internal/datly/generatedfile/write"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

type GeneratedFileStore struct{ Invoker dexec.ComponentInvoker }

var generatedFileReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v2/api/agently/generated-file"},
}
var generatedFileWriterTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/generated-file"},
}

func (s *GeneratedFileStore) List(ctx context.Context, input *read.Input) ([]*read.GeneratedFileView, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("generated file component store is not configured")
	}
	if input == nil {
		input = &read.Input{}
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: generatedFileReaderTarget, Input: input})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("generated file reader returned %T", value)
	}
	return out.Data, nil
}

func (s *GeneratedFileStore) PatchTrusted(ctx context.Context, row *write.GeneratedFile) error {
	_, err := s.PatchTrustedResult(ctx, row)
	return err
}

// PatchTrustedResult returns successful generated mutation rows, including lifecycle
// defaults and presence, for callers that expose mutable input objects.
func (s *GeneratedFileStore) PatchTrustedResult(ctx context.Context, row *write.GeneratedFile) (*write.Output, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("generated file component store is not configured")
	}
	if row == nil {
		return nil, fmt.Errorf("generated file mutation is required")
	}
	input := &write.Input{}
	input.SetGeneratedFiles([]*write.GeneratedFile{row})
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: generatedFileWriterTarget, Input: input})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*write.Output)
	if !ok || output == nil {
		return nil, fmt.Errorf("generated file writer returned %T", value)
	}
	return output, nil
}
