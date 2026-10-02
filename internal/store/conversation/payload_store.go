package conversation

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	read "github.com/viant/agently-core/internal/datly/payload/reference"
	write "github.com/viant/agently-core/internal/datly/payload/write"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

type PayloadStore struct{ Invoker dexec.ComponentInvoker }

var payloadReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v2/api/agently/payload"},
}
var payloadWriterTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/payload"},
}

func (s *PayloadStore) Get(ctx context.Context, id string) (*read.PayloadView, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("payload component store is not configured")
	}
	if strings.TrimSpace(id) == "" {
		return nil, nil
	}
	input := &read.Input{}
	input.SetId(strings.TrimSpace(id))
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: payloadReaderTarget, Input: input})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("payload reader returned %T", value)
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	if len(out.Data) != 1 || out.Data[0] == nil {
		return nil, fmt.Errorf("payload identity returned %d rows", len(out.Data))
	}
	return out.Data[0], nil
}

func (s *PayloadStore) PatchTrusted(ctx context.Context, row *write.Payload) error {
	_, err := s.PatchTrustedResult(ctx, row)
	return err
}

// PatchTrustedResult returns successful generated mutation rows, including lifecycle
// defaults and presence, for callers that expose mutable input objects.
func (s *PayloadStore) PatchTrustedResult(ctx context.Context, row *write.Payload) (*write.Output, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("payload component store is not configured")
	}
	if row == nil {
		return nil, fmt.Errorf("payload mutation is required")
	}
	input := &write.Input{}
	input.SetPayloads([]*write.Payload{row})
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: payloadWriterTarget, Input: input})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*write.Output)
	if !ok || output == nil {
		return nil, fmt.Errorf("payload writer returned %T", value)
	}
	return output, nil
}

// DeleteUnreferencedTrusted removes only payloads that have no surviving
// references. The reader skips known shared IDs; the generated DELETE guard
// aborts the caller's transaction if a new reference wins after that read.
func (s *PayloadStore) DeleteUnreferencedTrusted(ctx context.Context, ids ...string) error {
	if s == nil || s.Invoker == nil {
		return fmt.Errorf("payload component store is not configured")
	}
	seen := map[string]bool{}
	selected := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			selected = append(selected, id)
		}
	}
	if len(selected) == 0 {
		return nil
	}
	query := &read.Input{}
	query.SetIds(selected)
	providers := []locator.Provider{
		provider.Named("payloadaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "checkReferences" {
				return true, true, nil
			}
			return nil, false, nil
		}),
		queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: []string{"id", "referenced"}}}}),
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: payloadReaderTarget, Input: query, Providers: providers})
	if err != nil {
		return err
	}
	out, ok := value.(*read.Output)
	if !ok || out == nil {
		return fmt.Errorf("payload reference reader returned %T", value)
	}
	for _, row := range out.Data {
		if row == nil || row.Referenced {
			continue
		}
		mutation := &write.Payload{}
		mutation.SetId(row.Id)
		mutation.SetShouldDelete(true)
		input := &write.Input{}
		input.SetPayloads([]*write.Payload{mutation})
		writerProviders := []locator.Provider{provider.Named("payloadaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "deleteUnreferenced" {
				return true, true, nil
			}
			return nil, false, nil
		})}
		value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: payloadWriterTarget, Input: input, Providers: writerProviders})
		if err != nil {
			return err
		}
		if _, ok := value.(*write.Output); !ok {
			return fmt.Errorf("payload delete writer returned %T", value)
		}
	}
	return nil
}
