package reporting

import (
	"context"
	"github.com/stretchr/testify/require"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportmodel "github.com/viant/agently-core/model/reportshareartifact"
	identity "github.com/viant/agently-core/protocol/resource"
	"testing"
)

type staleNativeReportClient struct{ reportstore.Client }

func (staleNativeReportClient) UpdateSharedArtifactCAS(context.Context, *reportmodel.Record, int) error {
	return reportstore.ErrCASMismatch
}

func TestReportStoreCASPreservesNativeAndResourceStaleCauses(t *testing.T) {
	store := NewStoreAdapter(staleNativeReportClient{}).(interface {
		UpdateSharedArtifactCAS(context.Context, *SharedArtifact, int) error
	})
	err := store.UpdateSharedArtifactCAS(context.Background(), &SharedArtifact{ArtifactID: "report", OwnerID: "alice"}, 3)
	require.ErrorIs(t, err, reportstore.ErrCASMismatch)
	require.ErrorIs(t, err, identity.ErrResourceStale)
	require.NotErrorIs(t, err, identity.ErrResourceDenied)
}
