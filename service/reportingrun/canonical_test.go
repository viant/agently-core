package reportingrun

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	memory "github.com/viant/agently-core/app/store/reporting/memory"
	authsvc "github.com/viant/agently-core/service/auth"
	"testing"
)

func TestCanonicalModeRejectsDirectBrowserRunSnapshots(t *testing.T) {
	store := memory.New().(reportstore.RunClient)
	service := New(Options{Store: store, RequireCanonicalExecution: true})
	ctx := authsvc.InjectUser(context.Background(), "actor")
	_, err := service.Begin(ctx, &BeginInput{UIRunRequestID: "request", ConversationID: "conversation"})
	require.ErrorContains(t, err, "server run_report")
	_, err = service.Complete(ctx, &CompleteInput{ReportRunID: "forged", ReportSpec: json.RawMessage(`{}`), ReportFill: json.RawMessage(`{}`), ReportPrint: json.RawMessage(`{}`)})
	require.ErrorContains(t, err, "server run_report")
}
