package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/service/reporting/catalog"
)

func TestReportListToolDefaultPreservesCatalogPagingAndAPI(t *testing.T) {
	ctx := context.Background()
	source := catalogSource{}
	var entries []catalog.ReportCatalogCandidate
	for i := 0; i < 14; i++ {
		namespace := "shared"
		if i == 13 {
			namespace = "other"
		}
		uri := fmt.Sprintf("report://%s/entry-%02d", namespace, i)
		source[uri] = json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Example"},"reportSpec":{}}`)
		owner := "alice"
		if i%2 != 0 {
			owner = "bob"
		}
		entries = append(entries, catalog.ReportCatalogCandidate{URI: uri, Title: fmt.Sprintf("Example %02d", i), OwnerID: owner})
	}
	resolver := &identity.ResourceResolver{Source: source, Policy: reportPolicyFunc(func(_ context.Context, ref identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
		if ref.URI == "report://shared/entry-12" {
			return identity.ResourceDecision{}, identity.ErrResourceDenied
		}
		return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: "alice/account", ValidUntil: time.Now().Add(time.Minute)}, nil
	})}
	provider := &catalog.ReportCatalogService{
		Identity: func(context.Context) (identity.VerifiedActor, error) {
			return identity.VerifiedActor{Subject: "alice", Issuer: "issuer", TenantID: "tenant", AccountID: "account", IdentityRevision: "revision", ValidUntil: time.Now().Add(time.Minute)}, nil
		},
		Resolver: func(context.Context) (*identity.ResourceResolver, error) { return resolver, nil },
		Inventories: []catalog.ReportInventory{catalog.ReportInventoryFunc(func(context.Context, identity.VerifiedActor) ([]catalog.ReportCatalogCandidate, error) {
			return entries, nil
		})},
	}
	service := New(Options{Store: NewMemoryStore(), ReportCatalog: provider})
	method, err := service.Method("list_reports")
	require.NoError(t, err)
	input := &ListReportsInput{Namespace: "shared"}
	var first ListReportsResult
	require.NoError(t, method(ctx, input, &first))
	require.Len(t, first.Reports, 7)
	require.NotEmpty(t, first.NextCursor)
	require.Zero(t, input.Limit, "tool defaults must not mutate caller input")
	var second ListReportsResult
	require.NoError(t, method(ctx, &ListReportsInput{Namespace: "shared", Cursor: first.NextCursor}, &second))
	require.Len(t, second.Reports, 5)
	require.Empty(t, second.NextCursor)
	seen := map[string]bool{}
	for _, page := range []*ListReportsResult{&first, &second} {
		for _, report := range page.Reports {
			require.Equal(t, "shared", report.Namespace)
			require.NotEqual(t, "report://shared/entry-12", report.ResourceURI)
			require.False(t, seen[report.ResourceURI], "duplicate across pages")
			seen[report.ResourceURI] = true
		}
	}
	for _, limit := range []int{3, 20} {
		var result ListReportsResult
		require.NoError(t, method(ctx, &ListReportsInput{Namespace: "shared", Limit: limit}, &result))
		require.Len(t, result.Reports, min(limit, 12))
	}
	var exact ListReportsResult
	require.NoError(t, method(ctx, &ListReportsInput{Namespace: "shared", ReportID: "report://shared/entry-11"}, &exact))
	require.Len(t, exact.Reports, 1, "exact lookup beyond the first page must stay reachable")
	all, err := service.ListReports(ctx, &ListReportsInput{Namespace: "shared"})
	require.NoError(t, err)
	require.Len(t, all.Reports, 12, "ordinary API default must remain unchanged")
	var owned ListReportsResult
	require.NoError(t, method(ctx, &ListReportsInput{Namespace: "shared", CurrentUserOnly: true}, &owned))
	require.Len(t, owned.Reports, 6)
	for _, report := range owned.Reports {
		require.True(t, report.OwnedByCurrentUser)
	}
}

func TestReportListToolPreservesUnpagedLegacyStore(t *testing.T) {
	service := New(Options{Store: NewStoreAdapter(reportmemory.New())})
	ctx := authsvc.InjectUser(context.Background(), "owner")
	for i := 0; i < 12; i++ {
		_, err := service.SaveReport(ctx, &SaveReportRequest{
			ReportID: fmt.Sprintf("example-%02d", i), Title: "Example",
			ReportDocument: json.RawMessage(`{"kind":"reportDocument","id":"example"}`),
			ReportSpec:     json.RawMessage(validTestReportSpecJSON()),
		})
		require.NoError(t, err)
	}
	method, err := service.Method("list_reports")
	require.NoError(t, err)
	var all ListReportsResult
	require.NoError(t, method(ctx, &ListReportsInput{}, &all))
	require.Len(t, all.Reports, 12, "do not truncate a store without continuation")
	var bounded ListReportsResult
	require.NoError(t, method(ctx, &ListReportsInput{Limit: 3}, &bounded))
	require.Len(t, bounded.Reports, 3)
}
