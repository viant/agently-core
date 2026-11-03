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

func (r reader) Count(context.Context, *projection.Input) (int64, error) { return 1, r.fail }

func (r reader) Read(_ context.Context, i *projection.Input) ([]*projection.Row, error) {
	if r.fail != nil {
		return nil, r.fail
	}
	return []*projection.Row{{Id: "turn", ConversationId: "own", OwnerID: i.OwnerID, Status: "completed"}}, nil
}

func TestAuditHTTPBoundsAndUIOff(t *testing.T) {
	h := New(audit.New(reader{}, audit.Options{UserIDs: []string{"*"}}, nil))
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/capabilities", "", 200},
		{"GET", "/turns?conversationId=own", "", 200},
		{"GET", "/turns?conversationId=own&includeContent=true", "", 200},
		{"GET", "/turns?conversationId=own&includeContent=false", "", 200},
		{"GET", "/turns?conversationId=own&includeTotal=true", "", 200},
		{"GET", "/turns?conversationId=own&includeTotal=false", "", 200},
		{"GET", "/turns?conversationId=own&includeTotal=1", "", 400},
		{"GET", "/turns?conversationId=own&includeTotal=", "", 400},
		{"GET", "/turns?conversationId=own&includeTotal=true&includeTotal=false", "", 400},
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
		{"POST", "/export/json", `{"conversationId":"own"}`, 200},
		{"POST", "/export/json", `{"conversationId":"own","allowed":true}`, 400},
		{"POST", "/export/json", `{"conversationId":"own"} {}`, 400},
		{"POST", "/export/json", `{"conversationId":"own","cursor":"invalid"}`, 400},
		{"POST", "/export/json", `{"conversationId":"own","userId":"bob"}`, 403},
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
			if tc.path == "/export/json" && tc.status == 200 {
				require.Equal(t, "application/json", w.Header().Get("Content-Type"))
				require.Equal(t, `attachment; filename="conversation-audit.json"`, w.Header().Get("Content-Disposition"))
				require.Contains(t, w.Body.String(), `"models":[]`)
			}
			if strings.HasSuffix(tc.path, "includeTotal=true") && tc.status == 200 {
				require.Contains(t, w.Body.String(), `"totalCount":1`)
			}
			if strings.HasSuffix(tc.path, "includeTotal=false") && tc.status == 200 {
				require.NotContains(t, w.Body.String(), `"totalCount"`)
			}
		})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/api/conversation-audit/capabilities", nil))
	require.Equal(t, 401, w.Code)
}

func TestAuditHTTPExportErrorsNeverCommitFile(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{audit.ErrTooLarge, 413}, {audit.ErrBusy, 429}, {context.DeadlineExceeded, 504}, {errors.New("synthetic storage detail"), 500}} {
		for _, path := range []string{"/export", "/export/json"} {
			h := New(audit.New(reader{tc.err}, audit.Options{UserIDs: []string{"*"}}, nil))
			r := httptest.NewRequest(http.MethodPost, "/v1/api/conversation-audit"+path, strings.NewReader(`{"conversationId":"own"}`))
			r = r.WithContext(authctx.WithUserInfo(r.Context(), &authctx.UserInfo{Subject: "alice"}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			require.Equal(t, tc.status, w.Code)
			require.NotContains(t, w.Header().Get("Content-Type"), "csv")
			require.Empty(t, w.Header().Get("Content-Disposition"))
			require.NotContains(t, w.Body.String(), "synthetic storage detail")
		}
	}
}

func TestAuditHTTPGrantsAndOwnerModes(t *testing.T) {
	h := New(audit.New(reader{}, audit.Options{UIEnabled: true, UserIDs: []string{"alice"}, AuditorUserIDs: []string{"admin"}}, nil))
	for _, tc := range []struct {
		name, user, method, path, body string
		status                         int
	}{
		{"no grant capabilities", "outsider", "GET", "/capabilities", "", 200},
		{"no grant list", "outsider", "GET", "/turns?conversationId=own", "", 403},
		{"no grant count", "outsider", "GET", "/turns?conversationId=own&includeTotal=true", "", 403},
		{"no grant json", "outsider", "POST", "/export/json", `{"conversationId":"own"}`, 403},
		{"ordinary count all", "alice", "GET", "/turns?conversationId=own&userId=bob&includeTotal=true", "", 403},
		{"admin count", "admin", "GET", "/turns?conversationId=own&userIdContains=ali&includeTotal=true", "", 200},
		{"admin json", "admin", "POST", "/export/json", `{"conversationId":"own","userIdContains":"ali"}`, 200},
		{"no grant export", "outsider", "POST", "/export", `{"conversationId":"own"}`, 403},
		{"no grant detail", "outsider", "GET", "/conversations/own/turns/turn", "", 403},
		{"no grant payload", "outsider", "GET", "/conversations/own/turns/turn/payloads/body", "", 403},
		{"ordinary partial", "alice", "GET", "/turns?conversationId=own&userIdContains=ali", "", 403},
		{"ordinary empty partial", "alice", "GET", "/turns?conversationId=own&userIdContains=", "", 403},
		{"ordinary partial export", "alice", "POST", "/export", `{"conversationId":"own","userIdContains":"ali"}`, 403},
		{"admin partial", "admin", "GET", "/turns?conversationId=own&userIdContains=ali", "", 200},
		{"admin exact", "admin", "GET", "/turns?conversationId=own&userId=alice", "", 200},
		{"admin all", "admin", "GET", "/turns?conversationId=own", "", 200},
		{"admin empty partial", "admin", "GET", "/turns?conversationId=own&userIdContains=", "", 400},
		{"admin ambiguous", "admin", "GET", "/turns?conversationId=own&userId=alice&userIdContains=ali", "", 400},
		{"admin duplicate partial", "admin", "GET", "/turns?conversationId=own&userIdContains=ali&userIdContains=bob", "", 400},
		{"admin partial export", "admin", "POST", "/export", `{"conversationId":"own","userIdContains":"ali"}`, 200},
		{"admin empty partial export", "admin", "POST", "/export", `{"conversationId":"own","userIdContains":" "}`, 400},
		{"admin ambiguous export", "admin", "POST", "/export", `{"conversationId":"own","userId":"alice","userIdContains":"ali"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/v1/api/conversation-audit"+tc.path, strings.NewReader(tc.body))
			r = r.WithContext(authctx.WithUserInfo(r.Context(), &authctx.UserInfo{Subject: tc.user}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
			if tc.name == "no grant capabilities" {
				require.Contains(t, w.Body.String(), `"canAudit":false`)
				require.NotContains(t, w.Body.String(), `"users"`)
			}
		})
	}
}
