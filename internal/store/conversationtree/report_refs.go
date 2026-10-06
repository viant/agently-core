package conversationtree

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	contextread "github.com/viant/agently-core/internal/datly/reporting/context/read"
	runread "github.com/viant/agently-core/internal/datly/reporting/run/read"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

var reportRunReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[runread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/run"},
}
var reportContextReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[contextread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/conversation-context"},
}

// ValidateReportReferences checks report ownership and rejects a context
// outside the graph that points at an active report run inside it. Returned
// run IDs feed the later generated deletion batch.
func (d *Discoverer) ValidateReportReferences(ctx context.Context, graph *Graph) ([]string, error) {
	if d == nil || d.Invoker == nil || d.OwnerID == nil {
		return nil, fmt.Errorf("conversation graph reader is not configured")
	}
	userID := strings.TrimSpace(d.OwnerID(ctx))
	if err := d.authorize(ctx, graph); err != nil {
		return nil, err
	}
	if len(graph.Nodes) == 0 {
		return nil, nil
	}
	hasRuns, err := d.hasTable(ctx, "report_run")
	if err != nil {
		return nil, err
	}
	hasContexts, err := d.hasTable(ctx, "conversation_report_context")
	if err != nil {
		return nil, err
	}
	conversationIDs := sortedMapKeys(graph.Nodes)
	runIDs := map[string]bool{}
	if hasContexts {
		query := &contextread.Input{}
		query.SetConversationIDs(conversationIDs)
		rows, err := d.reportContexts(ctx, query, userID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row == nil {
				continue
			}
			if !d.systemRetention && strings.TrimSpace(row.OwnerId) != userID {
				return nil, ErrPermissionDenied
			}
			if id := strings.TrimSpace(row.ActiveReportRunId); id != "" {
				runIDs[id] = true
			}
		}
	}
	if hasRuns {
		query := &runread.Input{}
		query.SetConversationIDs(conversationIDs)
		rows, err := d.reportRuns(ctx, query, userID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row == nil {
				continue
			}
			if !d.systemRetention && strings.TrimSpace(row.OwnerId) != userID {
				return nil, ErrPermissionDenied
			}
			runIDs[row.ReportRunId] = true
		}
		if len(runIDs) > 0 {
			query = &runread.Input{}
			query.SetReportRunIDs(sortedMapKeys(runIDs))
			rows, err = d.reportRuns(ctx, query, userID)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				if row != nil && !d.systemRetention && strings.TrimSpace(row.OwnerId) != userID {
					return nil, ErrPermissionDenied
				}
			}
		}
	}
	result := sortedMapKeys(runIDs)
	if hasRuns && hasContexts && len(result) > 0 {
		query := &contextread.Input{}
		query.SetActiveReportRunIDs(result)
		rows, err := d.reportContexts(ctx, query, userID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row != nil && graph.Nodes[row.ConversationId] == nil {
				return nil, ErrGraphReferenced
			}
		}
	}
	return result, nil
}

func reportProviders(owner string) []locator.Provider {
	return []locator.Provider{
		provider.Named("reportaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "internal" {
				return true, true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
	}
}

func (d *Discoverer) reportRuns(ctx context.Context, input *runread.Input, owner string) ([]*runread.Run, error) {
	value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: reportRunReaderTarget, Input: input, Providers: planProviders("reportaccess", owner, "report_run_id", "owner_id", "conversation_id", "revision")})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*runread.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("report run reader returned %T", value)
	}
	return out.Data, nil
}

func (d *Discoverer) reportContexts(ctx context.Context, input *contextread.Input, owner string) ([]*contextread.Context, error) {
	value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: reportContextReaderTarget, Input: input, Providers: planProviders("reportaccess", owner, "owner_id", "conversation_id", "active_report_run_id", "revision")})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*contextread.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("report context reader returned %T", value)
	}
	return out.Data, nil
}
