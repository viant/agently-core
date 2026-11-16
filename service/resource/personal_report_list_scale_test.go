package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	"github.com/viant/agently-core/service/reporting"
	reportcatalog "github.com/viant/agently-core/service/reporting/catalog"
	userprovider "github.com/viant/agently-core/service/resource/provider/user"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type reportListScaleStore struct {
	*reporting.MemoryStore
	gets, lists, bytes atomic.Int64
}

func (s *reportListScaleStore) GetSharedArtifact(ctx context.Context, id string) (*reporting.SharedArtifact, error) {
	s.gets.Add(1)
	row, err := s.MemoryStore.GetSharedArtifact(ctx, id)
	if row != nil {
		s.bytes.Add(int64(len(row.ResourceDefinition)))
	}
	return row, err
}
func (s *reportListScaleStore) ListSharedArtifacts(ctx context.Context) ([]*reporting.SharedArtifact, error) {
	s.lists.Add(1)
	rows, err := s.MemoryStore.ListSharedArtifacts(ctx)
	for _, row := range rows {
		s.bytes.Add(int64(len(row.ResourceDefinition)))
	}
	return rows, err
}

func TestPersonalReportListNativeScaleCharacterization(t *testing.T) {
	for _, size := range []int{10, 101} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			ctx := zeroMetadataUserContext("alice")
			store := &reportListScaleStore{MemoryStore: reporting.NewMemoryStore()}
			source, err := userprovider.NewReports(store, "user-source")
			require.NoError(t, err)
			for i := 0; i < size; i++ {
				raw, _ := json.Marshal(map[string]any{"schemaVersion": 1, "reportDocument": map[string]any{"title": fmt.Sprintf("Report %d", i)}, "metadata": map[string]any{"description": strings.Repeat("x", 65536)}})
				_, err = source.CreateResource(ctx, primitive.CreateResourceInput{URI: fmt.Sprintf("report://users/alice/report-%d", i), FormatVersion: 1, Definition: raw})
				require.NoError(t, err)
			}
			actor, verify := PublicReadActors()
			provider, err := NewLocalProvider(LocalConfig{ProviderIdentity: "user-source", Actor: actor, Verify: verify, Catalog: source, Authoring: source, Authorize: userprovider.Authorize, Validators: map[string]LocalResourceValidator{"report": ValidateReportEnvelope}})
			require.NoError(t, err)
			mgr, err := manager.New(nil)
			require.NoError(t, err)
			defer mgr.CloseConversation("")
			require.NoError(t, mgr.RegisterLocal(ctx, "user-source", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "user-source"}, func(context.Context) (mcpclient.Interface, error) { return NewLocalMCPClient(provider) }))
			gateway := NewGateway(mgr, actor, verify, "")
			defer gateway.Close()
			catalog := &ReportGatewayCatalog{Gateway: gateway}
			for _, phase := range []string{"cold", "warm"} {
				store.gets.Store(0)
				store.lists.Store(0)
				store.bytes.Store(0)
				start := time.Now()
				result, err := catalog.List(ctx, reportcatalog.ReportCatalogQuery{Namespace: "users", Limit: 10})
				require.NoError(t, err)
				require.Len(t, result.Reports, 10)
				if phase == "warm" {
					require.Zero(t, store.gets.Load())
					require.Zero(t, store.lists.Load())
				} else {
					require.LessOrEqual(t, store.gets.Load(), int64(40), "definition reads must be bounded by the requested page")
				}
				elapsed := time.Since(start)
				encoded, err := json.Marshal(result)
				require.NoError(t, err)
				require.NotContains(t, string(encoded), strings.Repeat("x", 100))
				t.Logf("phase=%s inventory=%d page=10 gets=%d lists=%d definitionBytes=%d elapsed=%s", phase, size, store.gets.Load(), store.lists.Load(), store.bytes.Load(), elapsed)
			}
		})
	}
}
