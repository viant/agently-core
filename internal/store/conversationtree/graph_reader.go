package conversationtree

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"

	compact "github.com/viant/agently-core/internal/datly/conversation/graph/read"
	legacy "github.com/viant/agently-core/internal/datly/conversation/read"
	payloaddelete "github.com/viant/agently-core/internal/datly/payload/delete"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	"github.com/viant/agently-core/internal/store/conversation"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

const GraphReaderEnvironment = "AGENTLY_DELETE_GRAPH_READER"

type graphReaderMode string

const (
	graphReaderLegacy  graphReaderMode = "legacy"
	graphReaderCompact graphReaderMode = "compact"
)

type graphReaderContextKey struct{}

// PinGraphReader selects once for the entire managed operation, including
// nested schedule cascades and graph revalidation. Invalid configuration fails
// before starting the transaction; compact is the default. Set legacy explicitly
// to compare or roll back the graph reader without rebuilding.
func PinGraphReader(ctx context.Context) (context.Context, error) {
	if _, ok := ctx.Value(graphReaderContextKey{}).(graphReaderMode); ok {
		return payloaddelete.PinMode(ctx)
	}
	mode := graphReaderMode(strings.TrimSpace(os.Getenv(GraphReaderEnvironment)))
	if mode == "" {
		mode = graphReaderCompact
	}
	if mode != graphReaderLegacy && mode != graphReaderCompact {
		return ctx, fmt.Errorf("%s must be legacy or compact, got %q", GraphReaderEnvironment, mode)
	}
	return payloaddelete.PinMode(context.WithValue(ctx, graphReaderContextKey{}, mode))
}

var compactGraphReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[compact.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/conversation/graph"},
}

// graphRows is deliberately local to deletion. Ordinary conversation reads,
// retention candidate selection and schedule root selection keep their readers.
func (d *Discoverer) graphRows(ctx context.Context, input *legacy.ConversationInput, fields ...[]string) ([]*legacy.ConversationView, error) {
	if ctx.Value(graphReaderContextKey{}) == graphReaderCompact {
		return d.compactGraphRows(ctx, input, false)
	}
	return (&conversation.Store{Invoker: d.Invoker, OwnerID: d.OwnerID}).GraphRows(ctx, input, fields...)
}

func (d *Discoverer) compactGraphRows(ctx context.Context, input *legacy.ConversationInput, forUpdate bool) ([]*legacy.ConversationView, error) {
	if input == nil || input.Has == nil || (!input.Has.Ids && !input.Has.ParentIds && !input.Has.ParentTurnIds) {
		return nil, fmt.Errorf("compact conversation graph read requires a bounded predicate")
	}
	query := &compact.Input{}
	if input.Has.Ids {
		query.SetIDs(input.Ids)
	}
	if input.Has.ParentIds {
		query.SetParentIDs(input.ParentIds)
	}
	if input.Has.ParentTurnIds {
		query.SetParentTurnIDs(input.ParentTurnIds)
	}
	providers := []locator.Provider{provider.Named("conversationtreescope", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "internal" {
			return true, true, nil
		}
		return nil, false, nil
	})}
	value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{
		Target: compactGraphReaderTarget, Input: query, Providers: providers,
		ReaderOptions: queryselectors.ForUpdateOptions(ctx, forUpdate),
	})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*compact.Output)
	if !ok || output == nil {
		return nil, fmt.Errorf("compact conversation graph reader returned %T", value)
	}
	rows := make([]*legacy.ConversationView, 0, len(output.Data))
	for _, row := range output.Data {
		if row != nil {
			rows = append(rows, &legacy.ConversationView{
				Id: row.Id, CreatedByUserId: row.CreatedByUserId, Status: row.Status,
				ScheduleRunId: row.ScheduleRunId, CreatedAtRaw: row.CreatedAtRaw,
			})
		}
	}
	return rows, nil
}
