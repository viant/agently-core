package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
)

func demoMux(t *testing.T, endpoint string) (*aguiDemoRegistry, *http.ServeMux) {
	t.Helper()
	registry, err := newAGUIDemoRegistry([]AGUIDemoBackend{{ID: "public", Label: "Public demo", URL: endpoint, AllowedSubjects: []string{"owner", "other"}}})
	require.NoError(t, err)
	mux := http.NewServeMux()
	registry.register(mux)
	return registry, mux
}

func demoRequest(t *testing.T, mux http.Handler, subject, method, path string, input any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(input)
	require.NoError(t, err)
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	if subject != "" {
		r = r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: subject}))
	}
	r.Header.Set("Authorization", "Bearer private-bff-token")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Cookie", "agently_session=private-session")
	r.Header.Set("X-Agently-Debug", "true")
	r.Header.Set("X-User-ID", "untrusted-user")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func demoThread(t *testing.T, mux http.Handler, subject string) string {
	t.Helper()
	w := demoRequest(t, mux, subject, "POST", "/v1/ag-ui/backends/public/threads", nil)
	require.Equal(t, 200, w.Code)
	var payload struct {
		ThreadID string `json:"threadId"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.NotEmpty(t, payload.ThreadID)
	return payload.ThreadID
}

func demoInput(thread, run string) map[string]any {
	return map[string]any{"threadId": thread, "runId": run, "messages": []any{map[string]any{"id": "user", "role": "user", "content": "synthetic interop check"}}, "state": map[string]any{}, "tools": []any{}, "context": []any{}, "forwardedProps": map[string]any{}}
}

func TestAGUIDemoBFFAuthorizationOwnershipAndHeaderIsolation(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		for _, name := range []string{"Authorization", "Cookie", "X-Agently-Debug", "X-User-ID"} {
			require.Empty(t, r.Header.Get(name), name)
		}
		require.Equal(t, "text/event-stream", r.Header.Get("Accept"))
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Set-Cookie", "foreign=private")
		_, _ = io.WriteString(w, "data: {\"type\":\"RAW\",\"event\":{\"ok\":true}}\n\n")
	}))
	defer upstream.Close()
	_, mux := demoMux(t, upstream.URL)
	anonymous := demoRequest(t, mux, "", "GET", "/v1/ag-ui/backends", nil)
	require.Equal(t, 200, anonymous.Code)
	require.NotContains(t, anonymous.Body.String(), "Public demo")
	require.Equal(t, 401, demoRequest(t, mux, "", "POST", "/v1/ag-ui/backends/public/threads", nil).Code)
	require.Equal(t, 403, demoRequest(t, mux, "unlisted", "POST", "/v1/ag-ui/backends/public/threads", nil).Code)
	require.Equal(t, 400, demoRequest(t, mux, "owner", "POST", "/v1/ag-ui/backends/public/threads", map[string]any{"threadId": "caller-chosen"}).Code)
	form := httptest.NewRequest("POST", "/v1/ag-ui/backends/public/threads", strings.NewReader("{}"))
	form = form.WithContext(iauth.WithUserInfo(form.Context(), &iauth.UserInfo{Subject: "owner"}))
	form.Header.Set("Content-Type", "text/plain")
	formResponse := httptest.NewRecorder()
	mux.ServeHTTP(formResponse, form)
	require.Equal(t, 415, formResponse.Code)
	list := demoRequest(t, mux, "owner", "GET", "/v1/ag-ui/backends", nil)
	require.Equal(t, 200, list.Code)
	require.NotContains(t, list.Body.String(), upstream.URL)
	require.NotContains(t, list.Body.String(), "allowedSubjects")
	thread := demoThread(t, mux, "owner")
	input := demoInput(thread, "run")
	require.Equal(t, 403, demoRequest(t, mux, "other", "POST", "/v1/ag-ui/backends/public/run", input).Code)
	w := demoRequest(t, mux, "owner", "POST", "/v1/ag-ui/backends/public/run", input)
	require.Equal(t, 200, w.Code)
	require.Empty(t, w.Header().Get("Set-Cookie"))
	require.Equal(t, "data: {\"type\":\"RAW\",\"event\":{\"ok\":true}}\n\n", w.Body.String())
	require.Equal(t, 409, demoRequest(t, mux, "owner", "POST", "/v1/ag-ui/backends/public/run", input).Code)
	input["state"] = map[string]any{"changed": true}
	require.Contains(t, demoRequest(t, mux, "owner", "POST", "/v1/ag-ui/backends/public/run", input).Body.String(), "conflicts")
	require.EqualValues(t, 1, calls.Load())
	_, restarted := demoMux(t, upstream.URL)
	input["runId"] = "after-restart"
	require.Equal(t, 403, demoRequest(t, restarted, "owner", "POST", "/v1/ag-ui/backends/public/run", input).Code)
}

func TestAGUIDemoRejectsRedirectsAndUnownedReferences(t *testing.T) {
	var calls atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer redirectTarget.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, redirectTarget.URL, 307) }))
	defer upstream.Close()
	_, mux := demoMux(t, upstream.URL)
	thread := demoThread(t, mux, "owner")
	input := demoInput(thread, "run")
	input["parentRunId"] = "foreign"
	require.Equal(t, 403, demoRequest(t, mux, "owner", "POST", "/v1/ag-ui/backends/public/run", input).Code)
	delete(input, "parentRunId")
	input["forwardedProps"] = map[string]any{"agently": map[string]any{"version": "1", "operation": "chat"}}
	require.Equal(t, 400, demoRequest(t, mux, "owner", "POST", "/v1/ag-ui/backends/public/run", input).Code)
	input["forwardedProps"] = map[string]any{}
	require.Equal(t, 502, demoRequest(t, mux, "owner", "POST", "/v1/ag-ui/backends/public/run", input).Code)
	require.Zero(t, calls.Load())
}

func TestAGUIDemoFlushesBeforeUpstreamFinishes(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
		_, _ = io.WriteString(w, "data: last\n\n")
	}))
	defer upstream.Close()
	_, mux := demoMux(t, upstream.URL)
	thread := demoThread(t, mux, "owner")
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: "owner"})))
	}))
	defer proxy.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	body, _ := json.Marshal(demoInput(thread, "run"))
	r, err := http.NewRequestWithContext(ctx, "POST", proxy.URL+"/v1/ag-ui/backends/public/run", bytes.NewReader(body))
	require.NoError(t, err)
	r.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	defer response.Body.Close()
	data := make([]byte, len("data: first\n\n"))
	_, err = io.ReadFull(response.Body, data)
	require.NoError(t, err, "first frame must arrive while upstream is still waiting")
	require.Equal(t, "data: first\n\n", string(data))
}

func TestAGUIDemoRegistryRequiresExplicitSafeConfiguration(t *testing.T) {
	for _, cfg := range []AGUIDemoBackend{
		{ID: "agently", URL: "https://demo.invalid", AllowedSubjects: []string{"owner"}},
		{ID: "public", URL: "https://demo.invalid", AllowedSubjects: []string{"*"}},
		{ID: "public", URL: "https://demo.invalid"},
		{ID: "public", URL: "http://remote.invalid", AllowedSubjects: []string{"owner"}},
		{ID: "public", URL: "https://secret@demo.invalid", AllowedSubjects: []string{"owner"}},
		{ID: "public", URL: "https://demo.invalid?token=secret", AllowedSubjects: []string{"owner"}},
	} {
		_, err := newAGUIDemoRegistry([]AGUIDemoBackend{cfg})
		require.Error(t, err)
		require.False(t, strings.Contains(err.Error(), "secret"))
	}
}
