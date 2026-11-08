package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/forge/backend/types"
	"github.com/viant/jsonrpc/transport/server/base"
)

type transportOwnerKey struct{}

func TestHTTPBridgeCredentialRotationUsesExistingReconnect(t *testing.T) {
	service := NewService(&Config{NamespaceResolver: func(context.Context) (NamespaceIdentity, error) {
		return NamespaceIdentity{Namespace: "reader", SessionBound: true}, nil
	}, LocalStateOwnerResolver: func(ctx context.Context) (*types.LocalStateOwner, error) {
		return &types.LocalStateOwner{Binding: ctx.Value(transportOwnerKey{}).(string), ValidUntil: time.Now().Add(time.Hour)}, nil
	}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		service.Hub().ServeHTTPRPC(w, r.WithContext(context.WithValue(r.Context(), transportOwnerKey{}, r.Header.Get("Test-Owner"))))
	}))
	defer server.Close()
	call := func(owner, session, method string, data any) (int, string) {
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{"clientId": "reader-client", "data": data}})
		require.NoError(t, err)
		req, err := http.NewRequest(http.MethodPost, server.URL, bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Test-Owner", owner)
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		response, err := server.Client().Do(req)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		return response.StatusCode, response.Header.Get("Mcp-Session-Id")
	}
	status, original := call("verified-before", "", "ui.hello", nil)
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, original)
	snapshot := map[string]any{"conversationId": "chat", "windows": []any{map[string]any{"windowId": "kept-instance", "windowForm": map[string]any{"draft": "unsaved"}}}}
	status, _ = call("verified-before", original, "ui.snapshot", snapshot)
	require.Equal(t, http.StatusOK, status)
	status, same := call("verified-before", original, "ui.snapshot.status", nil)
	require.Equal(t, http.StatusOK, status, "unchanged credentials keep the original session")
	require.Equal(t, original, same)
	status, _ = call("verified-after", original, "ui.snapshot.status", nil)
	require.Equal(t, http.StatusNotFound, status, "existing bridge recovery recognizes a retired session")
	status, renewed := call("verified-after", "", "ui.hello", nil)
	require.Equal(t, http.StatusOK, status)
	require.NotEqual(t, original, renewed)
	status, _ = call("verified-after", renewed, "ui.snapshot.status", nil)
	require.Equal(t, http.StatusOK, status, "new credential reconnects once")
	require.Contains(t, string(service.Hub().Snapshot("reader", "reader-client")), `"draft":"unsaved"`)
	service.hub.httpBridge.onSessionClose(&base.Session{Id: original})
	require.Contains(t, string(service.Hub().Snapshot("reader", "reader-client")), `"draft":"unsaved"`, "late retired-session cleanup must preserve the renewed snapshot")
	status, _ = call("verified-after", renewed, "ui.snapshot.status", nil)
	require.Equal(t, http.StatusOK, status)
}
