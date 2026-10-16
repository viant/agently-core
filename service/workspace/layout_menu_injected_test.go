package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	ws "github.com/viant/agently-core/workspace"
	"net/http/httptest"
	"testing"
)

const menuShell = `version: 1
id: steward
topbar:
  actions:
    - id: automation
      title: Automation
      icon: time
      action: {type: window, windowKey: schedule}
left:
  history: {enabled: true, newConversation: true, search: {placeholder: Search conversations}}
  navigation: {enabled: true}
right: {mode: existing}
applications:
  - id: legacy
    title: Legacy
    menus:
      - id: secret
        title: Secret
        action: {type: window, windowKey: legacySecret}
`
const visibleApplications = `[{"id":"steward","title":"Workspace","icon":"applications","menus":[{"id":"advertisers","title":"Advertisers","icon":"office","action":{"type":"window","windowKey":"advertiserList","parameters":{"selected":9}}},{"id":"campaigns","title":"Campaigns","icon":"timeline-events","action":{"type":"window","windowKey":"campaignList"}}]}]`

func TestLayoutInjectedMenuReplacesOnlyApplicationsAndKeepsFallbackOptIn(t *testing.T) {
	previous := ws.Root()
	ws.SetRoot(t.TempDir())
	defer ws.SetRoot(previous)
	handler := NewMetadataHandler(nil, nil, "test")
	handler.SetAuthorizationRuntimes(nil, nil)
	handler.SetLayoutDefault([]byte(menuShell))
	original, err := handler.Layout(context.Background(), &LayoutRequest{})
	require.NoError(t, err)
	require.Equal(t, "legacy", original.Layout.Applications[0].ID)
	closed, checked := 0, 0
	revision := "menu-revision-one"
	handler.SetLayoutMenuReader(func(context.Context) (*LayoutMenuRead, error) {
		return &LayoutMenuRead{Applications: json.RawMessage(visibleApplications), Revision: revision, Revalidate: func(context.Context) error { checked++; return nil }, Close: func() error { closed++; return nil }}, nil
	})
	got, err := handler.Layout(context.Background(), &LayoutRequest{})
	require.NoError(t, err)
	require.Equal(t, original.Layout.Topbar, got.Layout.Topbar)
	require.Equal(t, original.Layout.Left, got.Layout.Left)
	require.Equal(t, original.Layout.Right, got.Layout.Right)
	require.Equal(t, "steward", got.Layout.Applications[0].ID)
	require.Equal(t, float64(9), got.Layout.Applications[0].Menus[0].Action.Parameters["selected"])
	require.Equal(t, 1, checked)
	require.Equal(t, 1, closed)
	require.NotEqual(t, original.LayoutRevision, got.LayoutRevision)
	firstRevision := got.LayoutRevision
	revision = "menu-revision-two"
	got, err = handler.Layout(context.Background(), &LayoutRequest{})
	require.NoError(t, err)
	require.NotEqual(t, firstRevision, got.LayoutRevision)
	handler.SetLayoutMenuReader(nil)
	got, err = handler.Layout(context.Background(), &LayoutRequest{})
	require.NoError(t, err)
	require.Equal(t, "legacy", got.Layout.Applications[0].ID)
}
func TestLayoutConfiguredEmptyOrDeniedMenuNeverReleasesLegacyApplications(t *testing.T) {
	previous := ws.Root()
	ws.SetRoot(t.TempDir())
	defer ws.SetRoot(previous)
	handler := NewMetadataHandler(nil, nil, "test")
	handler.SetAuthorizationRuntimes(nil, nil)
	handler.SetLayoutDefault([]byte(menuShell))
	for _, mode := range []string{"empty", "denied", "revoked-at-release", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			closed := 0
			handler.SetLayoutMenuReader(func(context.Context) (*LayoutMenuRead, error) {
				if mode == "denied" {
					return nil, errors.New("denied legacySecret")
				}
				raw := json.RawMessage(visibleApplications)
				if mode == "empty" {
					raw = json.RawMessage(`[]`)
				}
				if mode == "malformed" {
					raw = json.RawMessage(`null`)
				}
				return &LayoutMenuRead{Applications: raw, Revision: "menu-pin", Close: func() error { closed++; return nil }, Revalidate: func(context.Context) error {
					if mode == "revoked-at-release" {
						return errors.New("denied legacySecret")
					}
					return nil
				}}, nil
			})
			response := httptest.NewRecorder()
			handler.handleLayout()(response, httptest.NewRequest("GET", "/v1/workspace/layout", nil))
			require.NotContains(t, response.Body.String(), "legacySecret")
			require.NotContains(t, response.Body.String(), "advertiserList", mode)
			if mode == "empty" {
				require.Equal(t, 200, response.Code)
				require.Contains(t, response.Body.String(), `"applications":[]`)
			} else {
				require.Equal(t, 503, response.Code)
			}
			if mode != "denied" {
				require.Equal(t, 1, closed)
			}
		})
	}
}
