package conversation

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	payloaddelete "github.com/viant/agently-core/internal/datly/payload/delete"
	read "github.com/viant/agently-core/internal/datly/payload/reference"
	write "github.com/viant/agently-core/internal/datly/payload/write"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	"github.com/viant/agently-core/internal/store/maintenancediag"
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
var payloadDeleteTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[payloaddelete.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/payload/delete"},
}
var payloadBulkDeleteTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[payloaddelete.BulkDeleteComponent]().PkgPath(), Name: "PayloadBulkDelete"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/payload/delete-bulk"},
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
// references. The reader skips known shared IDs; the private key-only writer
// never loads their contents. The row/bulk DELETE guard aborts the caller's
// transaction if a new reference wins after that read. Sequential bounded
// invocations share the caller's transaction; this store never commits a chunk.
func (s *PayloadStore) DeleteUnreferencedTrusted(ctx context.Context, ids ...string) (retErr error) {
	if s == nil || s.Invoker == nil {
		return fmt.Errorf("payload component store is not configured")
	}
	ctx, err := payloaddelete.PinMode(ctx)
	if err != nil {
		return err
	}
	mode := payloaddelete.PinnedMode(ctx)
	target := payloadDeleteTarget
	if mode == payloaddelete.BulkMode {
		target = payloadBulkDeleteTarget
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
	done := maintenancediag.Phase(ctx, "payload_delete_batches")
	var referenceBatches, writerBatches, submitted, sharedSkipped, missing, deleteStatements, affected int
	defer func() {
		done(retErr, fmt.Sprintf("mode=%s candidates=%d reference_batches=%d writer_batches=%d submitted=%d shared_skipped=%d missing=%d delete_statements=%d affected=%d", mode, len(selected), referenceBatches, writerBatches, submitted, sharedSkipped, missing, deleteStatements, affected))
	}()
	providers := []locator.Provider{
		provider.Named("payloadaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "checkReferences" {
				return true, true, nil
			}
			return nil, false, nil
		}),
		queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: []string{"id", "referenced"}}}}),
	}
	writerProviders := []locator.Provider{provider.Named("payloadaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "deleteUnreferenced" {
			return true, true, nil
		}
		return nil, false, nil
	})}
	for start := 0; start < len(selected); start += payloaddelete.MaxBatchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(start+payloaddelete.MaxBatchSize, len(selected))
		query := &read.Input{}
		query.SetIds(selected[start:end])
		// Explicitly override reader pagination: every key in this bounded chunk
		// must be checked, even if the component's default page is smaller.
		query.SetLimit(end - start)
		referenceBatches++
		value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: payloadReaderTarget, Input: query, Providers: providers})
		if err != nil {
			return err
		}
		out, ok := value.(*read.Output)
		if !ok || out == nil {
			return fmt.Errorf("payload reference reader returned %T", value)
		}
		missing += end - start - len(out.Data)
		mutations := make([]*payloaddelete.PayloadDelete, 0, len(out.Data))
		for _, row := range out.Data {
			if row == nil {
				continue
			}
			if row.Referenced {
				sharedSkipped++
				continue
			}
			mutation := &payloaddelete.PayloadDelete{}
			mutation.SetId(row.Id)
			mutation.SetShouldDelete(true)
			mutations = append(mutations, mutation)
		}
		if len(mutations) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		input := &payloaddelete.Input{}
		input.SetPayloads(mutations)
		writerBatches++
		submitted += len(mutations)
		value, err = s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: target, Input: input, Providers: writerProviders})
		if err != nil {
			return err
		}
		output, ok := value.(*payloaddelete.Output)
		if !ok || output == nil {
			return fmt.Errorf("payload delete writer returned %T", value)
		}
		affected += len(output.Data)
		if mode == payloaddelete.BulkMode {
			if len(output.Data) > 0 {
				deleteStatements++
			}
		} else {
			deleteStatements += len(output.Data)
		}
	}
	return nil
}
