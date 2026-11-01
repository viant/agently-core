package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	svcauth "github.com/viant/agently-core/service/auth"
)

func TestHTTPSessionOriginPassesRuntimeProtection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("auth:\n  enabled: true\n  cookieName: example_session\n  local:\n    enabled: true\n"), 0600))
	runtime, err := svcauth.NewRuntime(ctx, root, nil)
	require.NoError(t, err)
	var calls atomic.Int32
	server := httptest.NewServer(svcauth.WithAuthExtensions(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"example-conversation"}`))
	}), runtime))
	defer server.Close()
	client, err := NewHTTP(server.URL)
	require.NoError(t, err)
	require.NoError(t, client.AuthLocalLogin(ctx, "example-user"))
	_, err = client.CreateConversation(ctx, &CreateConversationInput{Title: "Example"})
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load())

	for _, origin := range []string{"https://foreign.example.test", ""} {
		req, err := client.newRequest(ctx, http.MethodPost, "/v1/conversations", nil, "application/json")
		require.NoError(t, err)
		req.Header.Set("Origin", origin)
		client.applySessionOrigin(req)
		require.Equal(t, origin, req.Header.Get("Origin"), "explicit caller origin must survive")
		response, err := client.HTTPClient().Do(req)
		require.NoError(t, err)
		response.Body.Close()
		require.Equal(t, http.StatusForbidden, response.StatusCode)
	}
	require.EqualValues(t, 1, calls.Load(), "foreign or missing origin must not reach the backend")
}

func TestHTTPSessionOriginOnlyUsesConfiguredAPI(t *testing.T) {
	client, err := NewHTTP("https://api.example.test/prefix")
	require.NoError(t, err)
	base, _ := url.Parse(client.BaseURL())
	client.HTTPClient().Jar.SetCookies(base, []*http.Cookie{{Name: "example_session", Value: "synthetic-session", Path: "/"}})
	for _, tc := range []struct{ method, target, bearer, want string }{
		{http.MethodPost, "https://api.example.test/prefix/v1/conversations", "", "https://api.example.test"},
		{http.MethodGet, "https://api.example.test/prefix/v1/conversations", "", ""},
		{http.MethodPost, "https://other.example.test/v1/conversations", "", ""},
		{http.MethodPost, "http://api.example.test/v1/conversations", "", ""},
		{http.MethodPost, "https://api.example.test/prefix/v1/conversations", "synthetic-bearer", ""},
	} {
		req, err := http.NewRequest(tc.method, tc.target, nil)
		require.NoError(t, err)
		if tc.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+tc.bearer)
		}
		client.applySessionOrigin(req)
		require.Equal(t, tc.want, req.Header.Get("Origin"))
	}
}

func TestHTTPAuthenticatedRedirectDoesNotReachAnotherOrigin(t *testing.T) {
	var foreignCalls atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignCalls.Add(1) }))
	defer foreign.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/same" {
			http.Redirect(w, r, "/done", http.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path == "/done" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, r, foreign.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	for _, cookie := range []bool{true, false} {
		client, err := NewHTTP(server.URL)
		require.NoError(t, err)
		base, _ := url.Parse(server.URL)
		if cookie {
			client.HTTPClient().Jar.SetCookies(base, []*http.Cookie{{Name: "example_session", Value: "synthetic-session"}})
		} else {
			client.authToken = "synthetic-bearer"
		}
		req, err := client.newRequest(context.Background(), http.MethodPost, "/foreign", nil, "")
		require.NoError(t, err)
		_, err = client.HTTPClient().Do(req)
		require.ErrorContains(t, err, "cannot redirect across origins")
		req, err = client.newRequest(context.Background(), http.MethodPost, "/same", nil, "")
		require.NoError(t, err)
		response, err := client.HTTPClient().Do(req)
		require.NoError(t, err)
		response.Body.Close()
		require.Equal(t, http.StatusNoContent, response.StatusCode)
	}
	require.Zero(t, foreignCalls.Load())
}

func TestHTTPRedirectPreservesCallerPolicyAndClient(t *testing.T) {
	var calls int
	original := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { calls++; return http.ErrUseLastResponse }}
	client, err := NewHTTP("https://api.example.test", WithHTTPClient(original))
	require.NoError(t, err)
	require.NotSame(t, original, client.HTTPClient())
	req, _ := http.NewRequest(http.MethodGet, "https://api.example.test/next", nil)
	require.ErrorIs(t, client.HTTPClient().CheckRedirect(req, nil), http.ErrUseLastResponse)
	require.Equal(t, 1, calls)
}
