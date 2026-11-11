package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	auth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/service/ui/layoutgate"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/authz"
)

type layoutDisplayFacts struct {
	t       *testing.T
	calls   int
	failure error
	expired bool
}

func (f *layoutDisplayFacts) Resolve(ctx context.Context, request *permittedview.Request) (*permittedview.Snapshot, error) {
	f.calls++
	require.Equal(f.t, &permittedview.Request{IncludePrincipal: true}, request)
	if f.failure != nil {
		return nil, f.failure
	}
	subject := auth.EffectiveUserID(ctx)
	features := []string{}
	if subject == "editor" {
		features = append(features, "EXAMPLE_DISPLAY")
	}
	expires := time.Now().Add(time.Minute)
	if f.expired {
		expires = time.Now().Add(-time.Second)
	}
	return &permittedview.Snapshot{
		AuthorizationVersion: "principal:" + subject,
		ExpiresAt:            expires,
		Principal:            map[string]any{"subject": subject, "features": features},
	}, nil
}

func layoutDisplayFixture(t *testing.T, federated bool, resolver permittedview.Resolver) *MetadataHandler {
	t.Helper()
	apps := json.RawMessage(`[{"id":"example","title":"Example","menus":[{"id":"ordinary","title":"Ordinary","action":{"type":"window","resourceUri":"window://example/ordinary"}},{"id":"feature","title":"Feature","visibleWhen":{"source":"authorization","field":"principal.features","contains":"EXAMPLE_DISPLAY"},"action":{"type":"window","resourceUri":"window://example/feature"}},{"id":"feature-again","title":"Feature again","visibleWhen":{"source":"authorization","field":"principal.features","contains":"EXAMPLE_DISPLAY"},"action":{"type":"window","resourceUri":"window://example/other"}}]}]`)
	topbar := `"topbar":{"actions":[{"id":"feature-action","title":"Feature action","visibleWhen":{"source":"authorization","field":"principal.features","contains":"EXAMPLE_DISPLAY"},"action":{"type":"window","resourceUri":"window://example/action"}}]}`
	raw := []byte(`{"version":1,"id":"shell",` + topbar + `,"applications":` + string(apps) + `}`)
	handler := NewMetadataHandler(nil, nil, "test")
	handler.SetAuthorizationRuntimes(nil, nil)
	if federated {
		raw = []byte(`{"version":1,"id":"shell",` + topbar + `,"applications":[]}`)
		handler.SetLayoutMenuReader(func(context.Context) (*LayoutMenuRead, error) {
			return &LayoutMenuRead{
				Applications:              apps,
				Revision:                  "published-menu",
				ProviderOwnedApplications: true,
				Close:                     func() error { return nil },
				Revalidate:                func(context.Context) error { return nil },
			}, nil
		})
	}
	require.NoError(t, handler.SetCapturedLayout(t.TempDir(), "embedded", raw, func(context.Context) error { return nil }))
	handler.SetLayoutPresentationResolver(resolver)
	return handler
}

func TestLayoutPrincipalPresentationParityAndRequestIsolation(t *testing.T) {
	for _, federated := range []bool{false, true} {
		t.Run(fmt.Sprintf("federated=%t", federated), func(t *testing.T) {
			facts := &layoutDisplayFacts{t: t}
			handler := layoutDisplayFixture(t, federated, facts)
			var editorRevision string
			for index, subject := range []string{"editor", "viewer", "editor"} {
				request := httptest.NewRequest(http.MethodGet, "/v1/workspace/layout", nil)
				request = request.WithContext(auth.WithUserInfo(request.Context(), &auth.UserInfo{Subject: subject}))
				response := httptest.NewRecorder()
				handler.handleLayout()(response, request)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Equal(t, index+1, facts.calls, "all guarded nodes reuse one request principal")
				var result LayoutResponse
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
				require.Len(t, result.Layout.Applications, 1)
				if subject == "editor" {
					require.Len(t, result.Layout.Applications[0].Menus, 3)
					require.Len(t, result.Layout.Topbar.Actions, 1)
					if editorRevision == "" {
						editorRevision = result.LayoutRevision
					}
					require.Equal(t, editorRevision, result.LayoutRevision)
				} else {
					require.Len(t, result.Layout.Applications[0].Menus, 1)
					require.Empty(t, result.Layout.Topbar.Actions)
					require.NotEqual(t, editorRevision, result.LayoutRevision)
				}
				require.NotContains(t, response.Body.String(), "EXAMPLE_DISPLAY", "display facts and evaluated guards are not delivered as menu data")
				require.NotContains(t, response.Body.String(), `"subject"`)
			}
		})
	}
}

func TestLayoutPresentationErrorsPreserveCausesAndSafeHTTPStatus(t *testing.T) {
	dependency := errors.New("private dependency detail")
	for _, scenario := range []struct {
		name    string
		failure error
		expired bool
		status  int
		cause   error
	}{
		{"unavailable", dependency, false, http.StatusServiceUnavailable, dependency},
		{"identity", authz.ErrIdentityDenied, false, http.StatusUnauthorized, authz.ErrIdentityDenied},
		{"denied", fmt.Errorf("private denial: %w", authz.ErrDenied), false, http.StatusForbidden, authz.ErrDenied},
		{"canceled", context.Canceled, false, http.StatusRequestTimeout, context.Canceled},
		{"deadline", context.DeadlineExceeded, false, http.StatusGatewayTimeout, context.DeadlineExceeded},
		{"expired", nil, true, http.StatusServiceUnavailable, layoutgate.ErrFactsUnavailable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			facts := &layoutDisplayFacts{t: t, failure: scenario.failure, expired: scenario.expired}
			handler := layoutDisplayFixture(t, true, facts)
			value, err := handler.Layout(context.Background(), nil)
			require.Nil(t, value)
			require.ErrorIs(t, err, scenario.cause)
			response := httptest.NewRecorder()
			handler.handleLayout()(response, httptest.NewRequest(http.MethodGet, "/v1/workspace/layout", nil))
			require.Equal(t, scenario.status, response.Code)
			require.NotContains(t, response.Body.String(), "private")
			require.NotContains(t, response.Body.String(), "ordinary")
		})
	}
}

func TestAlreadyProjectedLayoutDoesNotResolvePrincipalAgain(t *testing.T) {
	facts := &layoutDisplayFacts{t: t, failure: errors.New("unneeded source read")}
	handler := NewMetadataHandler(nil, nil, "test")
	handler.SetAuthorizationRuntimes(nil, nil)
	handler.SetLayoutPresentationResolver(facts)
	require.NoError(t, handler.SetCapturedLayout(t.TempDir(), "embedded", []byte(`{"version":1,"id":"shell","applications":[{"id":"example","title":"Example","menus":[{"id":"ordinary","title":"Ordinary","action":{"type":"window","resourceUri":"window://example/ordinary"}}]}]}`), func(context.Context) error { return nil }))
	result, err := handler.Layout(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, result.Layout.Applications, 1)
	require.Zero(t, facts.calls)
}
