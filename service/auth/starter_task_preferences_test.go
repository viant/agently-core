package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStarterTaskPreferencesUseAuthenticatedUserSettings(t *testing.T) {
	ctx := context.Background()
	users := newNativeUserServiceFixture(t)
	for _, name := range []string{"alice", "bob"} {
		_, err := users.UpsertWithProvider(ctx, name, name, name+"@example.test", "oauth", name)
		require.NoError(t, err)
	}
	require.NoError(t, users.UpdatePreferences(ctx, "alice", &PreferencesPatch{AgentPrefs: map[string]map[string]any{"agent": {"example": true}}}))
	mux := http.NewServeMux()
	handler := &Handler{users: users}
	handler.RegisterPreferences(mux)
	runtime := &Runtime{ext: &authExtension{users: users}}
	body := `{"starterTasks":{"hidden":["startertask://example/default#review"],"order":["personal#own"],"own":[{"id":"own","title":"Personal","prompt":"Review my document."}]}}`
	request := httptest.NewRequest(http.MethodPatch, "/v1/api/me/preferences", strings.NewReader(body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request.WithContext(InjectUser(ctx, "alice")))
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	alice, err := runtime.StarterTaskPreferences(InjectUser(ctx, "alice"))
	require.NoError(t, err)
	require.NotNil(t, alice)
	require.Equal(t, []string{"startertask://example/default#review"}, alice.Hidden)
	require.Equal(t, []string{"personal#own"}, alice.Order)
	require.Equal(t, "Review my document.", alice.Own[0].Prompt)
	bob, err := runtime.StarterTaskPreferences(InjectUser(ctx, "bob"))
	require.NoError(t, err)
	require.Nil(t, bob)
	stored, err := users.GetByUsername(ctx, "alice")
	require.NoError(t, err)
	require.Contains(t, stored.Preferences, "agentPrefs")
}
