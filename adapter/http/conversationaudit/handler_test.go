package conversationaudit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	projection "github.com/viant/agently-core/internal/datly/conversationaudit/read"
	audit "github.com/viant/agently-core/service/conversationaudit"
)

type reader struct{ fail error }

func (r reader) Read(_ context.Context, i *projection.Input) ([]*projection.Row, error) {
	if r.fail != nil {
		return nil, r.fail
	}
	return []*projection.Row{{Id: "turn", ConversationId: "own", OwnerID: i.OwnerID, Status: "completed"}}, nil
}

func TestAuditHTTPBoundsAndUIOff(t *testing.T) {
	h := New(audit.New(reader{}, audit.Options{}, nil))
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/capabilities", "", 200},
		{"GET", "/turns?conversationId=own", "", 200},
		{"GET", "/turns?conversationId=own&includeContent=true", "", 200},
		{"GET", "/turns?conversationId=own&includeContent=false", "", 200},
		{"GET", "/turns?conversationId=own&includeContent=1", "", 400},
		{"GET", "/turns?conversationId=own&includeContent=", "", 400},
		{"GET", "/turns?conversationId=own&includeContent=true&includeContent=false", "", 400},
		{"GET", "/turns?conversationId=own&includeContent=true&turnIds=foreign", "", 400},
		{"GET", "/turns", "", 400},
		{"GET", "/turns?userId=alice", "", 400},
		{"GET", "/turns?conversationId=own&userId=bob", "", 403},
		{"GET", "/turns?conversationId=own&conversationId=other", "", 400},
		{"GET", "/turns?conversationId=own&allowed=true", "", 400},
		{"GET", "/turns?from=not-a-date&to=2026-10-29T00:00:00Z", "", 400},
		{"GET", "/turns?conversationId=own&limit=201", "", 400},
		{"POST", "/export", `{"conversationId":"own","allowed":true}`, 400},
		{"POST", "/export", `{"conversationId":"own"} {}`, 400},
		{"POST", "/export", `{"conversationId":"own","userId":"bob"}`, 403},
		{"POST", "/export", `{"conversationId":"own"}`, 200},
		{"DELETE", "/turns", "", 405},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/v1/api/conversation-audit"+tc.path, strings.NewReader(tc.body))
			r = r.WithContext(authctx.WithUserInfo(r.Context(), &authctx.UserInfo{Subject: "alice"}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
			if tc.path == "/capabilities" {
				require.Contains(t, w.Body.String(), `"uiEnabled":false`)
			}
			if strings.Contains(tc.path, "includeContent=true") && tc.status == 200 {
				require.Contains(t, w.Body.String(), `"input":""`)
				require.Contains(t, w.Body.String(), `"models":[]`)
				require.NotContains(t, w.Body.String(), `"message":`)
			}
			if tc.path == "/export" && tc.status == 200 {
				require.Contains(t, w.Header().Get("Content-Type"), "text/csv")
				require.Contains(t, w.Body.String(), "feedback_rating")
			}
		})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/api/conversation-audit/capabilities", nil))
	require.Equal(t, 403, w.Code)
}

func TestAuditHTTPExportErrorsNeverCommitCSV(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{audit.ErrTooLarge, 413}, {audit.ErrBusy, 429}, {context.DeadlineExceeded, 504}, {errors.New("synthetic storage detail"), 500}} {
		h := New(audit.New(reader{tc.err}, audit.Options{}, nil))
		r := httptest.NewRequest(http.MethodPost, "/v1/api/conversation-audit/export", strings.NewReader(`{"conversationId":"own"}`))
		r = r.WithContext(authctx.WithUserInfo(r.Context(), &authctx.UserInfo{Subject: "alice"}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		require.Equal(t, tc.status, w.Code)
		require.NotContains(t, w.Header().Get("Content-Type"), "csv")
		require.Empty(t, w.Header().Get("Content-Disposition"))
		require.NotContains(t, w.Body.String(), "synthetic storage detail")
	}
}
