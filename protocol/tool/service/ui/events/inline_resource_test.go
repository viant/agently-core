package events

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	forge "github.com/viant/agently-core/service/primitiveprovider"
)

func TestInlineReportEventsRemainConversationScopedWithResourceWindows(t *testing.T) {
	type ownerKey struct{}
	bridge := forge.NewService(&forge.Config{WindowDefinitions: &frameworkHandoffCatalog{denied: true}, NamespaceResolver: func(ctx context.Context) (forge.NamespaceIdentity, error) {
		owner, _ := ctx.Value(ownerKey{}).(string)
		return forge.NamespaceIdentity{Namespace: owner, ValidUntil: time.Now().Add(time.Minute)}, nil
	}})
	service := New(bridge)
	ctx := requestctx.WithConversationID(context.WithValue(context.Background(), ownerKey{}, "alice"), "conv-inline")
	for _, kind := range []string{"report.context_updated", "report.export_start", "report.export_complete"} {
		result := &RecordOutput{}
		require.NoError(t, service.record(ctx, &RecordInput{Kind: kind, Detail: map[string]interface{}{"sourceKind": "inline"}}, result))
		require.True(t, result.Recorded)
		require.Nil(t, result.Event.Resource)
		require.Empty(t, result.Event.WindowID)
	}
	own, err := service.reg.ListConversationEventsContext(ctx, "conv-inline")
	require.NoError(t, err)
	require.Len(t, own, 3)
	foreign := requestctx.WithConversationID(context.WithValue(context.Background(), ownerKey{}, "bob"), "conv-inline")
	rows, err := service.reg.ListConversationEventsContext(foreign, "conv-inline")
	require.NoError(t, err)
	require.Empty(t, rows)
	wrongConversation := requestctx.WithConversationID(ctx, "different")
	rows, err = service.reg.ListConversationEventsContext(wrongConversation, "conv-inline")
	require.NoError(t, err)
	require.Empty(t, rows)
	for _, input := range []*RecordInput{
		{Kind: "report.export_start", WindowID: "unknown", Detail: map[string]interface{}{"sourceKind": "inline"}},
		{Kind: "report.export_start", ClientID: "unknown", Detail: map[string]interface{}{"sourceKind": "inline"}},
	} {
		require.Error(t, service.record(ctx, input, &RecordOutput{}), "explicit window identity still uses the owning window path")
	}
}
