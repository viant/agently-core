package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	wscfg "github.com/viant/agently-core/workspace/config"
)

func TestPresentationIdentityConfigurationRequiresNoAuthorizationCatalog(t *testing.T) {
	t.Setenv("AGENTLY_AUTHZ_CONFIG", "")
	root := t.TempDir()
	raw := []byte(`{"kind":"operator-owned-test"}`)
	require.NoError(t, os.WriteFile(filepath.Join(root, "identity.json"), raw, 0600))
	config := &wscfg.Root{Raw: map[string]interface{}{"ui": map[string]interface{}{
		"presentation": map[string]interface{}{"identityFile": "identity.json"},
	}}}
	marker := errors.New("existing host callback")
	calls := 0
	options := Options{AuthorizationProviderFactory: func(ctx context.Context, identity, evaluation json.RawMessage) (AuthorizationProviders, error) {
		calls++
		require.Equal(t, raw, []byte(identity))
		require.Empty(t, evaluation)
		return AuthorizationProviders{Identity: fixtureHostIdentity{}}, nil
	}, ConfigureBuilder: func(context.Context, *executor.Builder) error { return marker }}
	configure, err := ConfigureAuthorization(options, root, config, nil)
	require.NoError(t, err)
	require.ErrorIs(t, configure(context.Background(), executor.NewBuilder()), marker)
	require.Equal(t, 1, calls)

	options.AuthorizationProviderFactory = func(context.Context, json.RawMessage, json.RawMessage) (AuthorizationProviders, error) {
		return AuthorizationProviders{}, nil
	}
	configure, err = ConfigureAuthorization(options, root, config, nil)
	require.NoError(t, err)
	require.ErrorContains(t, configure(context.Background(), executor.NewBuilder()), "presentation identity provider is unavailable")
}

func TestPresentationIdentityConfigurationErrorsRemainVisible(t *testing.T) {
	for _, raw := range []interface{}{"invalid", map[string]interface{}{}, map[string]interface{}{"identityFile": 42}, map[string]interface{}{"identityFile": "missing.json"}} {
		_, err := configurePresentation(Options{}, t.TempDir(), &wscfg.Root{Raw: map[string]interface{}{"ui": map[string]interface{}{"presentation": raw}}})
		require.Error(t, err)
	}
}
