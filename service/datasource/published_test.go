package datasource

import (
	"context"
	"errors"
	"testing"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/protocol/primitive/observe"
	"github.com/viant/forge/backend/types"
)

type publishedExecutor struct {
	calls int
	name  string
	args  map[string]interface{}
	err   error
}

func (e *publishedExecutor) Execute(_ context.Context, name string, args map[string]interface{}) (string, error) {
	e.calls++
	e.name = name
	e.args = args
	if e.err != nil {
		return "", e.err
	}
	return `{"items":[{"name":"published"}]}`, nil
}
func TestFetchPublishedExecutesOneToolWithoutMetadataLookup(t *testing.T) {
	executor := &publishedExecutor{}
	service := New(Options{Executor: executor, ReadContext: func(context.Context) (context.Context, error) {
		t.Fatal("metadata read on published fetch")
		return nil, nil
	}})
	source := &dsproto.DataSource{ID: "connectors", DataSource: types.DataSource{Selectors: &types.Selectors{Data: "items"}}, Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "studio", Method: "sdk.connectors.list"}}
	counts := map[observe.Phase]int{}
	ctx := observe.WithObserver(context.Background(), observe.ObserverFunc(func(e observe.Event) { counts[e.Phase]++ }))
	result, err := service.FetchPublished(ctx, source, map[string]interface{}{"limit": 25}, nil)
	if err != nil || len(result.Rows) != 1 || result.Rows[0]["name"] != "published" || executor.calls != 1 || executor.name != "studio:sdk.connectors.list" {
		t.Fatalf("published fetch: %+v %v executor=%+v", result, err, executor)
	}
	if counts["datasource.published.total"] != 1 || counts["datasource.published.backend"] != 1 {
		t.Fatalf("published timing phases: %+v", counts)
	}
}
func TestFetchPublishedMCPLeavesBusinessDecisionToProducer(t *testing.T) {
	executor := &publishedExecutor{}
	consumerChecks := 0
	service := New(Options{Executor: executor, Authorize: func(context.Context, string, map[string]interface{}) error {
		consumerChecks++
		return errors.New("consumer would deny business data")
	}})
	source := &dsproto.DataSource{ID: "orders", DataSource: types.DataSource{Selectors: &types.Selectors{Data: "items"}}, Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "datastudio", Method: "AdvancedReportingRun"}}
	result, err := service.FetchPublished(context.Background(), source, nil, nil)
	if err != nil || len(result.Rows) != 1 || consumerChecks != 0 || executor.calls != 1 {
		t.Fatalf("producer-authorized fetch: result=%+v err=%v consumer=%d calls=%d", result, err, consumerChecks, executor.calls)
	}
	executor.err = errors.New("producer denied")
	result, err = service.FetchPublished(context.Background(), source, nil, nil)
	if err == nil || result != nil || consumerChecks != 0 || executor.calls != 2 {
		t.Fatalf("producer denial was not final: result=%+v err=%v consumer=%d calls=%d", result, err, consumerChecks, executor.calls)
	}
}
func TestFetchPublishedInlineStateNeverCallsTool(t *testing.T) {
	executor := &publishedExecutor{}
	service := New(Options{Executor: executor})
	source := &dsproto.DataSource{ID: "filter", Backend: &dsproto.Backend{Kind: dsproto.BackendInline, Rows: []map[string]interface{}{{"draft": "local"}}}}
	result, err := service.FetchPublished(context.Background(), source, nil, nil)
	if err != nil || len(result.Rows) != 1 || executor.calls != 0 {
		t.Fatalf("inline fetch: %+v %v calls=%d", result, err, executor.calls)
	}
}
