package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	"github.com/viant/agently-core/runtime/evidence"
)

type deniedForecast struct{ contentCalls, fenceCalls int }

func (p *deniedForecast) Content(_ context.Context, content string) (string, error) {
	p.contentCalls++
	if content == "" {
		return "", nil
	}
	return "", fmt.Errorf("missing forecast bindings")
}
func (p *deniedForecast) Fence(context.Context, string, string) (string, error) {
	p.fenceCalls++
	return "", fmt.Errorf("missing forecast bindings")
}
func (p *deniedForecast) Stream(context.Context, string, string, bool) (string, error) {
	return "", fmt.Errorf("unexpected stream")
}

func TestFencedCompilerCannotBypassEvidenceThroughExplicitOrEncodedFences(t *testing.T) {
	service := New(Options{Exporter: NewForgeExporter(nil), Store: NewStoreAdapter(reportmemory.New())})
	for _, payload := range []json.RawMessage{json.RawMessage(`{"version":2,"data":[1]}`), json.RawMessage(`"{\"version\":2,\"data\":[1]}"`)} {
		guard := &deniedForecast{}
		ctx := evidence.WithPublication(context.Background(), guard)
		output, err := service.CompileFencedReport(ctx, &CompileFencedReportRequest{Fences: []FencedReportFence{{Kind: "forge-data", Payload: payload}}})
		require.True(t, evidence.IsRejection(err), "%v", err)
		require.Nil(t, output)
		require.Equal(t, 1, guard.fenceCalls)
	}
	guard := &deniedForecast{}
	output, err := service.CompileFencedReport(evidence.WithPublication(context.Background(), guard), &CompileFencedReportRequest{Content: validFencedReportContent()})
	require.True(t, evidence.IsRejection(err), "%v", err)
	require.Nil(t, output)
	require.Equal(t, 1, guard.contentCalls)
}
