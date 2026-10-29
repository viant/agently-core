package sdk

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/authz"
	"github.com/viant/forge/backend/artifactcohort"
)

type noCohortFacts struct{}

func (noCohortFacts) Resolve(context.Context) (authz.Facts, error) {
	return authz.Facts{}, authz.ErrDenied
}
func TestDirectToolPreservesRealCohortDenialStatus(t *testing.T) {
	replica, err := artifactcohort.NewReplica(artifactcohort.Config{Enabled: true, Source: "example", Tenant: "tenant", Account: "account", Caller: artifactcohort.NamespaceCaller{Namespace: "example"}, Facts: noCohortFacts{}, SessionKey: func(context.Context) (string, error) { return "", authz.ErrDenied }})
	require.NoError(t, err)
	source, err := artifactcohort.NewMetadataSource(replica, []string{"example"})
	require.NoError(t, err)
	_, denied := source.AdmissionKey(context.Background())
	require.Error(t, denied)
	wrapped := fmt.Errorf("selected resource: %w", denied)
	require.Equal(t, http.StatusForbidden, statusForToolExecuteError(wrapped))
	status, _, result := toolExecuteErrorResponse(wrapped, `{"private":"partial"}`)
	require.Equal(t, http.StatusForbidden, status)
	require.Empty(t, result)
}
