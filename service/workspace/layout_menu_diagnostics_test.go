package workspace

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	ws "github.com/viant/agently-core/workspace"
)

func TestLayoutMenuFailureLogsSafeStageWithoutDebug(t *testing.T) {
	t.Setenv("AGENTLY_DEBUG", "off")
	t.Setenv("AGENTLY_DEBUG_COMPONENTS", "other")
	previousRoot := ws.Root()
	ws.SetRoot(t.TempDir())
	t.Cleanup(func() { ws.SetRoot(previousRoot) })
	for _, stage := range []string{"definition_read", "revalidate_read"} {
		t.Run(stage, func(t *testing.T) {
			var output bytes.Buffer
			previousLog := log.Writer()
			log.SetOutput(&output)
			t.Cleanup(func() { log.SetOutput(previousLog) })

			handler := NewMetadataHandler(nil, nil, "test")
			handler.SetAuthorizationRuntimes(nil, nil)
			handler.SetLayoutDefault([]byte(menuShell))
			failure := markLayoutMenuFailure(stage, "internal", "navigation://workspace/layout", errors.Join(identity.ErrResourceDenied, errors.New("private session token")))
			require.ErrorIs(t, failure, identity.ErrResourceDenied)
			handler.SetLayoutMenuReader(func(context.Context) (*LayoutMenuRead, error) {
				if stage == "definition_read" {
					return nil, failure
				}
				return &LayoutMenuRead{Applications: []byte(`[]`), Revision: "pin", Revalidate: func(context.Context) error { return failure }}, nil
			})
			response := httptest.NewRecorder()
			handler.handleLayout()(response, httptest.NewRequest(http.MethodGet, "/v1/workspace/layout", nil))
			require.Equal(t, http.StatusServiceUnavailable, response.Code)
			require.Contains(t, output.String(), `layout.menu.failed stage="`+stage+`" connection="internal" resource="navigation://workspace/layout" category="denied"`)
			require.Equal(t, 1, strings.Count(output.String(), "layout.menu.failed"))
			require.NotContains(t, output.String(), "private session token")
			require.NotContains(t, response.Body.String(), "private session token")
		})
	}
}
