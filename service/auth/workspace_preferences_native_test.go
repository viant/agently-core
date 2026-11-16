package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	userwrite "github.com/viant/agently-core/internal/datly/user/write"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/authz"
	"github.com/viant/jsonrpc"
	xhandler "github.com/viant/xdatly/handler"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkspacePreferencesCanonicalIDCannotSelectAnotherUsername(t *testing.T) {
	ctx := context.Background()
	users := newNativeUserServiceFixture(t)
	aliceID, err := users.UpsertWithProvider(ctx, "alice", "Alice", "alice@example.test", "oauth", "alice-subject")
	require.NoError(t, err)
	bobID, err := users.UpsertWithProvider(ctx, aliceID, "Bob", "bob@example.test", "oauth", "bob-subject")
	require.NoError(t, err)
	require.NotEqual(t, aliceID, bobID)
	ctx = iauth.WithCanonicalUserID(InjectUser(ctx, "alice-subject"), aliceID)
	require.NoError(t, users.UpdatePreferences(ctx, aliceID, &PreferencesPatch{WorkspaceAction: &WorkspaceMutation{Action: "set_title", Label: "Alice workspace"}}))
	alice, err := users.GetByID(ctx, aliceID)
	require.NoError(t, err)
	require.Contains(t, alice.Preferences, "workspace")
	bob, err := users.GetByID(ctx, bobID)
	require.NoError(t, err)
	require.NotContains(t, bob.Preferences, "workspace")
	preferences, err := readPreferences(ctx, users, aliceID)
	require.NoError(t, err)
	require.Contains(t, preferences, "workspace")
}

func TestRuntimeAuthExtensionMountsNativeWorkspacePreferences(t *testing.T) {
	ctx := context.Background()
	users := newNativeUserServiceFixture(t)
	_, err := users.UpsertWithProvider(ctx, "workspace-alice", "Alice", "alice@example.test", "provider", "user-123")
	require.NoError(t, err)
	authority := newSessionOAuthAuthority(t)
	runtime := &Runtime{cfg: authority.config(false), sessions: NewManager(time.Hour, nil)}
	runtime.ext = newAuthExtension(runtime.cfg, runtime.sessions, "", nil, users)
	handler := WithAuthExtensions(http.NotFoundHandler(), runtime)
	access, id := authority.pair(t, "resource")
	body, err := json.Marshal(map[string]string{"idToken": id, "accessToken": access})
	require.NoError(t, err)
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/v1/api/auth/session", strings.NewReader(string(body))))
	require.Equal(t, http.StatusOK, login.Code, login.Body.String())
	var cookie *http.Cookie
	for _, candidate := range login.Result().Cookies() {
		if candidate.Name == runtime.cfg.CookieName {
			cookie = candidate
		}
	}
	require.NotNil(t, cookie)

	// Enter through the same runtime wrapper used by the real application,
	// rather than mounting RegisterPreferences directly in the fixture.
	request := func(method, body string, authenticated bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/v1/api/me/preferences", strings.NewReader(body))
		if authenticated {
			r.AddCookie(cookie)
			r.Header.Set("Sec-Fetch-Site", "same-origin")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	require.Equal(t, http.StatusUnauthorized, request(http.MethodGet, "", false).Code)
	patch := request(http.MethodPatch, `{"workspaceAction":{"action":"set_title","label":"My checks"}}`, true)
	require.Equal(t, http.StatusNoContent, patch.Code, patch.Body.String())
	read := request(http.MethodGet, "", true)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	require.Equal(t, "private, no-store", read.Header().Get("Cache-Control"))
	require.Contains(t, read.Body.String(), `"title":"My checks"`)
}

func TestWorkspacePreferencesNativeWriterRejectsStaleSettings(t *testing.T) {
	ctx := context.Background()
	svc := newNativeUserServiceFixture(t)
	_, err := svc.UpsertWithProvider(ctx, "workspace-alice", "Alice", "alice@example.test", "oauth", "workspace-alice")
	require.NoError(t, err)
	before, err := svc.GetByUsername(ctx, "workspace-alice")
	require.NoError(t, err)
	prefs := &WorkspacePreferences{}
	require.NoError(t, prefs.Pin("weekly", "Weekly", WorkspacePin{ID: "orders", Kind: "window", Provider: "example", URI: "window://example/orders"}))
	require.NoError(t, svc.UpdatePreferences(ctx, "workspace-alice", &PreferencesPatch{Workspace: prefs}))
	stale := &userwrite.User{}
	stale.SetId(before.ID)
	stale.SetSettings(userTextPtr(`{"starterTasks":{"hidden":["lost"]}}`))
	err = svc.writePreferences(ctx, stale, before.settingsRaw)
	var conflict *xhandler.Conflict
	require.ErrorAs(t, err, &conflict, "stale native SQL mutation must return the original conflict")
	require.EqualError(t, err, preferencesChangedMessage)
	var status interface{ HTTPStatusCode() int }
	require.ErrorAs(t, err, &status)
	require.Equal(t, http.StatusConflict, status.HTTPStatusCode())

	mux := http.NewServeMux()
	(&Handler{users: &conflictingWorkspacePreferences{UserService: svc, err: err}}).RegisterPreferences(mux)
	request := httptest.NewRequest(http.MethodPatch, "/v1/api/me/preferences", strings.NewReader(`{"workspace":{"groups":[]}}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request.WithContext(InjectUser(ctx, "workspace-alice")))
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "reload and retry")
	after, err := svc.GetByUsername(ctx, "workspace-alice")
	require.NoError(t, err)
	require.Contains(t, after.Preferences, "workspace")
	require.NotContains(t, after.Preferences, "starterTasks")
	require.NoError(t, svc.UpdatePreferences(ctx, "workspace-alice", &PreferencesPatch{StarterTasks: &StarterTaskPreferences{Hidden: []string{"example#task"}}}))
	after, err = svc.GetByUsername(ctx, "workspace-alice")
	require.NoError(t, err)
	require.Contains(t, after.Preferences, "workspace")
	require.Contains(t, after.Preferences, "starterTasks")
}

type conflictingWorkspacePreferences struct {
	UserService
	err error
}

func (s *conflictingWorkspacePreferences) UpdatePreferences(context.Context, string, *PreferencesPatch) error {
	return s.err
}

func TestWorkspaceActionsPersistThroughAuthenticatedPreferenceBoundary(t *testing.T) {
	ctx := context.Background()
	svc := newNativeUserServiceFixture(t)
	for _, name := range []string{"workspace-alice", "workspace-bob"} {
		_, err := svc.UpsertWithProvider(ctx, name, name, name+"@example.test", "oauth", name)
		require.NoError(t, err)
	}
	mux := http.NewServeMux()
	(&Handler{users: svc}).RegisterPreferences(mux)
	edit := func(body string, code int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPatch, "/v1/api/me/preferences", strings.NewReader(body))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request.WithContext(InjectUser(ctx, "workspace-alice")))
		require.Equal(t, code, response.Code, response.Body.String())
	}
	edit(`{"workspaceAction":{"action":"pin","groupId":"weekly","label":"Weekly","pin":{"id":"orders","kind":"window","provider":"example","uri":"window://example/orders"}}}`, http.StatusNoContent)
	edit(`{"workspaceAction":{"action":"set_enabled","enabled":false}}`, http.StatusNoContent)
	edit(`{"workspaceAction":{"action":"create_group","groupId":"planning","label":"Planning"}}`, http.StatusNoContent)
	edit(`{"workspaceAction":{"action":"create_group","groupId":"another","label":"Weekly"}}`, http.StatusBadRequest)
	edit(`{"workspaceAction":{"action":"rename_group","groupId":"planning","label":"weekly"}}`, http.StatusBadRequest)
	edit(`{"workspaceAction":{"action":"pin","groupId":"weekly","pin":{"id":"overview","kind":"report","provider":"example","uri":"report://example/overview"}}}`, http.StatusNoContent)
	edit(`{"workspaceAction":{"action":"set_application_visibility","applicationId":"example/dsp","hidden":true}}`, http.StatusNoContent)
	edit(`{"workspaceAction":{"action":"move_item","itemId":"orders","targetGroupId":"missing"}}`, http.StatusBadRequest)
	alice, err := svc.GetByUsername(ctx, "workspace-alice")
	require.NoError(t, err)
	raw, err := json.Marshal(alice.Preferences["workspace"])
	require.NoError(t, err)
	var prefs WorkspacePreferences
	require.NoError(t, json.Unmarshal(raw, &prefs))
	require.False(t, *prefs.Enabled)
	require.Len(t, prefs.Groups[0].Items, 2)
	require.Equal(t, []string{"example/dsp"}, prefs.HiddenApplications)
	bob, err := svc.GetByUsername(ctx, "workspace-bob")
	require.NoError(t, err)
	require.NotContains(t, bob.Preferences, "workspace")
}

func TestWorkspaceToolsUseSameNativePreferencesAndAuthenticatedOwner(t *testing.T) {
	ctx := context.Background()
	users := newNativeUserServiceFixture(t)
	for _, name := range []string{"tool-alice", "tool-bob"} {
		_, err := users.UpsertWithProvider(ctx, name, name, name+"@example.test", "oauth", name)
		require.NoError(t, err)
	}
	tools := NewWorkspaceTools(&Runtime{ext: &authExtension{users: users}})
	update, err := tools.Method("update")
	require.NoError(t, err)
	pin := &WorkspaceMutation{Action: "pin", GroupID: "weekly", Label: "Weekly", Pin: &WorkspacePin{ID: "orders", Kind: "window", Provider: "example", URI: "window://example/orders"}}
	var output WorkspaceMutationReceipt
	require.NoError(t, update(InjectUser(ctx, "tool-alice"), pin, &output))
	require.True(t, *output.Enabled)
	require.Equal(t, "saved", output.Status)
	require.Equal(t, "weekly", output.GroupID)
	require.Equal(t, "orders", output.ItemID)
	read, err := tools.Method("get")
	require.NoError(t, err)
	var other WorkspacePreferences
	require.NoError(t, read(InjectUser(ctx, "tool-bob"), &WorkspaceReadRequest{}, &other))
	require.Empty(t, other.Groups)
	require.Error(t, update(ctx, pin, &output), "missing ingress identity cannot select an owner")
}

func TestWorkspacePreferencesMySQLIndependentConnectionsCAS(t *testing.T) {
	servers, db, prefix := oauthMySQLRuntimes(t)
	ctx := context.Background()
	id := prefix + "workspace-user"
	_, err := db.ExecContext(ctx, "INSERT INTO users(id,username,provider,timezone,settings) VALUES(?,?,'local','UTC',NULL)", id, id)
	require.NoError(t, err)
	services := []*DatlyUserService{NewDatlyUserService(servers[0]), NewDatlyUserService(servers[1])}
	previous, err := services[0].GetByUsername(ctx, id)
	require.NoError(t, err)
	a := &userwrite.User{}
	a.SetId(id)
	a.SetSettings(userTextPtr(`{"label":"Alpha","_preferencesRevision":"one"}`))
	b := &userwrite.User{}
	b.SetId(id)
	b.SetSettings(userTextPtr(`{"label":"Beta","_preferencesRevision":"two"}`))
	require.NoError(t, services[0].writePreferences(ctx, a, previous.settingsRaw))
	var conflict *xhandler.Conflict
	require.ErrorAs(t, services[1].writePreferences(ctx, b, previous.settingsRaw), &conflict)
	require.NoError(t, services[0].UpdatePreferences(ctx, id, &PreferencesPatch{AgentPrefs: map[string]map[string]any{"example": {"label": "Alpha"}}}))
	old, err := services[1].GetByUsername(ctx, id)
	require.NoError(t, err)
	require.NoError(t, services[0].UpdatePreferences(ctx, id, &PreferencesPatch{AgentPrefs: map[string]map[string]any{"example": {"label": "alpha"}}}))
	stale := &userwrite.User{}
	stale.SetId(id)
	stale.SetSettings(userTextPtr(`{"overwrite":"stale"}`))
	require.ErrorAs(t, services[1].writePreferences(ctx, stale, old.settingsRaw), &conflict, "case-only edit must still invalidate the old settings value")
	saved, err := services[0].GetByUsername(ctx, id)
	require.NoError(t, err)
	require.Contains(t, saved.Preferences, "agentPrefs")
	require.NotContains(t, saved.Preferences, "overwrite")
	start := make(chan struct{})
	results := make(chan error, 2)
	for index := range services {
		go func(i int) {
			<-start
			entity := &userwrite.User{}
			entity.SetId(id)
			entity.SetSettings(userTextPtr(fmt.Sprintf(`{"winner":%d,"_preferencesRevision":"race-%d"}`, i, i)))
			results <- services[i].writePreferences(ctx, entity, saved.settingsRaw)
		}(index)
	}
	close(start)
	wins, conflicts := 0, 0
	for range services {
		err := <-results
		if err == nil {
			wins++
		} else {
			var conflict *xhandler.Conflict
			require.ErrorAs(t, err, &conflict)
			conflicts++
		}
	}
	require.Equal(t, 1, wins)
	require.Equal(t, 1, conflicts)

}

func TestWorkspaceNativePreferencesPreserveExactLargeParameters(t *testing.T) {
	ctx := context.Background()
	users := newNativeUserServiceFixture(t)
	_, err := users.UpsertWithProvider(ctx, "numeric-workspace", "Numeric", "numeric@example.test", "local", "numeric-workspace")
	require.NoError(t, err)
	input := &PreferencesPatch{WorkspaceAction: &WorkspaceMutation{Action: "pin", Pin: &WorkspacePin{ID: "report", Kind: "report", Provider: "example", URI: "report://example/overview", Parameters: map[string]json.RawMessage{"entityId": json.RawMessage(`9007199254740993`)}}}}
	require.NoError(t, users.UpdatePreferences(ctx, "numeric-workspace", input))
	require.NoError(t, users.UpdatePreferences(ctx, "numeric-workspace", &PreferencesPatch{StarterTasks: &StarterTaskPreferences{Hidden: []string{"example#task"}}}))
	saved, err := users.GetByUsername(ctx, "numeric-workspace")
	require.NoError(t, err)
	raw, err := json.Marshal(saved.Preferences["workspace"])
	require.NoError(t, err)
	require.Contains(t, string(raw), "9007199254740993")
	require.NotContains(t, string(raw), "9007199254740992")
}

func TestNativePreferenceWriterVerifiesOnlyChangedReportDefaults(t *testing.T) {
	users := newNativeUserServiceFixture(t)
	ctx := context.Background()
	id, err := users.UpsertWithProvider(ctx, "default-owner", "Owner", "owner@example.test", "oauth", "owner-subject")
	require.NoError(t, err)
	ctx = iauth.WithCanonicalUserID(InjectUser(ctx, "owner-subject"), id)
	entry := WorkspaceReportDefault{SourceProvider: "source", SourceURI: "report://example/original", TargetURI: "report://users/owner/copy"}
	calls := 0
	denied := fmt.Errorf("source verification failed")
	users.SetWorkspaceDefaultVerifier(func(context.Context, WorkspaceReportDefault) error { calls++; return denied })
	err = users.UpdatePreferences(ctx, id, &PreferencesPatch{Workspace: &WorkspacePreferences{Defaults: []WorkspaceReportDefault{entry}}})
	require.ErrorIs(t, err, denied)
	stored, err := users.GetByID(ctx, id)
	require.NoError(t, err)
	require.NotContains(t, stored.Preferences, "workspace")
	users.SetWorkspaceDefaultVerifier(func(context.Context, WorkspaceReportDefault) error { calls++; return nil })
	require.NoError(t, users.UpdatePreferences(ctx, id, &PreferencesPatch{WorkspaceAction: &WorkspaceMutation{Action: "set_default", Default: &entry}}))
	require.Equal(t, 2, calls)
	// An unrelated title edit and an unchanged whole-document replacement must
	// neither resolve the original again nor turn an outage into a settings error.
	users.SetWorkspaceDefaultVerifier(func(context.Context, WorkspaceReportDefault) error { calls++; return denied })
	require.NoError(t, users.UpdatePreferences(ctx, id, &PreferencesPatch{WorkspaceAction: &WorkspaceMutation{Action: "set_title", Label: "My desk"}}))
	require.NoError(t, users.UpdatePreferences(ctx, id, &PreferencesPatch{Workspace: &WorkspacePreferences{Title: "My desk", Defaults: []WorkspaceReportDefault{entry}}}))
	require.Equal(t, 2, calls)
	changed := entry
	changed.TargetURI = "report://users/owner/different"
	require.ErrorIs(t, users.UpdatePreferences(ctx, id, &PreferencesPatch{Workspace: &WorkspacePreferences{Defaults: []WorkspaceReportDefault{changed}}}), denied)
	require.Equal(t, 3, calls)
	require.NoError(t, users.UpdatePreferences(ctx, id, &PreferencesPatch{Workspace: &WorkspacePreferences{}}))
	require.Equal(t, 3, calls, "removing a default needs no source read")
}

func TestReportDefaultVerificationCannotBeBypassedByHTTPReplacementOrTool(t *testing.T) {
	ctx := context.Background()
	users := newNativeUserServiceFixture(t)
	id, err := users.UpsertWithProvider(ctx, "default-http", "Owner", "owner@example.test", "oauth", "default-http")
	require.NoError(t, err)
	ctx = iauth.WithCanonicalUserID(InjectUser(ctx, "default-http"), id)
	runtime := &Runtime{ext: &authExtension{users: users}}
	calls := 0
	reject := true
	failure := &WorkspacePreferenceError{Cause: fmt.Errorf("personal copy does not match the source")}
	require.NoError(t, runtime.SetWorkspaceDefaultVerifier(func(callCtx context.Context, entry WorkspaceReportDefault) error {
		calls++
		require.Equal(t, id, iauth.CanonicalUserID(callCtx))
		if reject {
			return failure
		}
		return nil
	}))
	mux := http.NewServeMux()
	(&Handler{users: users}).RegisterPreferences(mux)
	entry := WorkspaceReportDefault{SourceProvider: "example", SourceURI: "report://example/original", TargetURI: "report://users/owner/copy"}
	send := func(patch PreferencesPatch, intended int) {
		raw, err := json.Marshal(patch)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPatch, "/v1/api/me/preferences", strings.NewReader(string(raw))).WithContext(ctx)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, req)
		require.Equal(t, intended, out.Code, out.Body.String())
	}
	send(PreferencesPatch{Workspace: &WorkspacePreferences{Defaults: []WorkspaceReportDefault{entry}}}, http.StatusBadRequest)
	send(PreferencesPatch{WorkspaceAction: &WorkspaceMutation{Action: "set_default", Default: &entry}}, http.StatusBadRequest)
	update, err := NewWorkspaceTools(runtime).Method("update")
	require.NoError(t, err)
	var receipt WorkspaceMutationReceipt
	require.ErrorIs(t, update(ctx, &WorkspaceMutation{Action: "set_default", Default: &entry}, &receipt), failure)
	require.Equal(t, 3, calls)
	row, err := users.GetByID(ctx, id)
	require.NoError(t, err)
	require.NotContains(t, row.Preferences, "workspace")
	reject = false
	require.NoError(t, update(ctx, &WorkspaceMutation{Action: "set_default", Default: &entry}, &receipt))
	require.Equal(t, 4, calls, "tool must not perform a second verification")
	send(PreferencesPatch{Workspace: &WorkspacePreferences{Title: "Desk", Defaults: []WorkspaceReportDefault{entry}}}, http.StatusNoContent)
	require.Equal(t, 4, calls, "unchanged HTTP replacement must not reread source")
	require.NotNil(t, receipt.DefaultChange)
	require.Nil(t, receipt.DefaultChange.Previous)
	require.Equal(t, &entry, receipt.DefaultChange.Current)
	second := entry
	second.TargetURI = "report://users/owner/second"
	require.NoError(t, update(ctx, &WorkspaceMutation{Action: "set_default", Default: &second}, &receipt))
	require.Equal(t, &entry, receipt.DefaultChange.Previous)
	require.Equal(t, &second, receipt.DefaultChange.Current)
	require.Equal(t, 5, calls, "only the changed mapping needs verification")
	require.NoError(t, update(ctx, &WorkspaceMutation{Action: "clear_default", Default: &second}, &receipt))
	require.Equal(t, &second, receipt.DefaultChange.Previous)
	require.Nil(t, receipt.DefaultChange.Current)
	require.Equal(t, 5, calls, "clearing a default needs no source read")
	require.NoError(t, update(ctx, &WorkspaceMutation{Action: "clear_default", Default: &second}, &receipt))
	require.Equal(t, "unchanged", receipt.Status)
	require.Nil(t, receipt.DefaultChange.Previous)
	require.Nil(t, receipt.DefaultChange.Current)

	row, err = users.GetByID(ctx, id)
	require.NoError(t, err)
	require.Contains(t, row.Preferences, "workspace")
}

func TestWorkspaceVisibilityToolUsesOnlyAcknowledgedBrowserWrite(t *testing.T) {
	users := newNativeUserServiceFixture(t)
	ctx := context.Background()
	id, err := users.UpsertWithProvider(ctx, "browser-owner", "Owner", "owner@example.test", "oauth", "browser-owner")
	require.NoError(t, err)
	ctx = iauth.WithCanonicalUserID(InjectUser(ctx, "browser-owner"), id)
	tools := NewWorkspaceTools(&Runtime{ext: &authExtension{users: users}})
	update, err := tools.Method("update")
	require.NoError(t, err)
	hidden := true
	mutation := &WorkspaceMutation{Action: "set_application_visibility", ApplicationID: `["example","app"]`, Hidden: &hidden}
	var output WorkspaceMutationReceipt
	require.Error(t, update(ctx, mutation, &output), "no browser must not fall back to a server write")
	calls := 0
	tools.SetBrowserVisibility = func(context.Context, *WorkspaceMutation) (*WorkspaceMutationReceipt, error) {
		calls++
		return &WorkspaceMutationReceipt{Status: "saved", Storage: "browser", Action: mutation.Action, Hidden: &hidden}, nil
	}
	require.NoError(t, update(ctx, mutation, &output))
	require.Equal(t, "browser", output.Storage)
	require.Equal(t, 1, calls)
	tools.SetBrowserVisibility = func(context.Context, *WorkspaceMutation) (*WorkspaceMutationReceipt, error) {
		return &WorkspaceMutationReceipt{Status: "pending"}, nil
	}
	require.Error(t, update(ctx, mutation, &output))
	row, err := users.GetByID(ctx, id)
	require.NoError(t, err)
	require.NotContains(t, row.Preferences, "workspace", "all browser outcomes leave server workspace preferences untouched")
}

func TestUnavailablePreferenceWriterDoesNotAcknowledgeSave(t *testing.T) {
	ctx := InjectUser(context.Background(), "owner")
	for _, users := range []*DatlyUserService{nil, {}} {
		handler := &Handler{users: users}
		mux := http.NewServeMux()
		handler.RegisterPreferences(mux)
		req := httptest.NewRequest(http.MethodPatch, "/v1/api/me/preferences", strings.NewReader(`{"workspaceAction":{"action":"set_title","label":"Desk"}}`)).WithContext(ctx)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		read := httptest.NewRecorder()
		mux.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/v1/api/me/preferences", nil).WithContext(ctx))
		require.Equal(t, http.StatusServiceUnavailable, read.Code)
		workspace := NewWorkspaceTools(&Runtime{ext: &authExtension{users: users}})
		update, err := workspace.Method("update")
		require.NoError(t, err)
		var receipt WorkspaceMutationReceipt
		require.Error(t, update(ctx, &WorkspaceMutation{Action: "set_title", Label: "Desk"}, &receipt))
		require.Empty(t, receipt.Status)
	}
}

func TestPreferenceWriterRejectsForeignOwnerBeforeVerifyingDefault(t *testing.T) {
	users := newNativeUserServiceFixture(t)
	ctx := context.Background()
	alice, err := users.UpsertWithProvider(ctx, "alice-pref", "Alice", "alice@example.test", "example", "subject-a")
	require.NoError(t, err)
	bob, err := users.UpsertWithProvider(ctx, "bob-pref", "Bob", "bob@example.test", "example", "subject-b")
	require.NoError(t, err)
	caller := iauth.WithCanonicalUserID(InjectUser(ctx, "subject-a"), alice)
	verified := 0
	users.SetWorkspaceDefaultVerifier(func(context.Context, WorkspaceReportDefault) error { verified++; return nil })
	entry := WorkspaceReportDefault{SourceProvider: "example", SourceURI: "report://example/original", TargetURI: "report://users/fixture/copy"}
	patch := &PreferencesPatch{WorkspaceAction: &WorkspaceMutation{Action: "set_default", Default: &entry}}
	err = users.UpdatePreferences(caller, "bob-pref", patch)
	require.ErrorIs(t, err, authz.ErrDenied)
	require.Zero(t, verified)
	require.Nil(t, patch.committedWorkspace)
	require.Nil(t, patch.committedDefaultChange)
	stored, err := users.GetByID(ctx, bob)
	require.NoError(t, err)
	require.NotContains(t, stored.Preferences, "workspace")
}

func TestPreferencesUseVerifiedProviderSubjectWhenLoginNameCollides(t *testing.T) {
	users := newNativeUserServiceFixture(t)
	ctx := context.Background()
	alice, err := users.UpsertWithProvider(ctx, "alice-login", "Alice", "alice@example.test", "issuer-a", "same-name")
	require.NoError(t, err)
	bob, err := users.UpsertWithProvider(ctx, "same-name", "Bob", "bob@example.test", "issuer-b", "subject-b")
	require.NoError(t, err)
	caller := iauth.WithProvider(InjectUser(ctx, "same-name"), "issuer-a")
	require.NoError(t, users.UpdatePreferences(caller, "same-name", &PreferencesPatch{WorkspaceAction: &WorkspaceMutation{Action: "set_title", Label: "My desk"}}))
	prefs, err := readPreferences(caller, users, "same-name")
	require.NoError(t, err)
	require.Equal(t, "My desk", prefs["workspace"].(map[string]interface{})["title"])
	a, err := users.GetByID(ctx, alice)
	require.NoError(t, err)
	require.Contains(t, a.Preferences, "workspace")
	b, err := users.GetByID(ctx, bob)
	require.NoError(t, err)
	require.NotContains(t, b.Preferences, "workspace")
}

func TestPreferencePatchReportsVerifierFailureWithoutDiagnosticPayload(t *testing.T) {
	base := context.Background()
	users := newNativeUserServiceFixture(t)
	id, err := users.UpsertWithProvider(base, "pref-http", "Example", "owner@example.test", "fixture", "pref-subject")
	require.NoError(t, err)
	ctx := iauth.WithCanonicalUserID(InjectUser(base, "pref-subject"), id)
	mux := http.NewServeMux()
	(&Handler{users: users}).RegisterPreferences(mux)
	input := PreferencesPatch{WorkspaceAction: &WorkspaceMutation{Action: "set_default", Default: &WorkspaceReportDefault{SourceProvider: "example", SourceURI: "report://example/base", TargetURI: "report://users/example/copy"}}}
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		cause  error
		status int
	}{
		{"upstream unavailable", jsonrpc.NewHTTPStatusError(503), 503},
		{"provider authentication", jsonrpc.NewHTTPStatusError(401), 502},
		{"cancelled request", context.Canceled, 499},
		{"timeout", context.DeadlineExceeded, 504},
		{"owner denied", authz.ErrDenied, 403},
		{"report denied", identity.ErrResourceDenied, 403},
		{"stale report", identity.ErrResourceStale, 409},
		{"missing target", jsonrpc.NewHTTPStatusError(404), 404},
		{"conflict", jsonrpc.NewHTTPStatusError(409), 409},
		{"unknown failure", errors.New("unknown cause"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			users.SetWorkspaceDefaultVerifier(func(context.Context, WorkspaceReportDefault) error {
				return fmt.Errorf("private-provider-details: %w", tc.cause)
			})
			req := httptest.NewRequest(http.MethodPatch, "/v1/api/me/preferences", bytes.NewReader(raw)).WithContext(ctx)
			out := httptest.NewRecorder()
			mux.ServeHTTP(out, req)
			require.Equal(t, tc.status, out.Code)
			require.NotContains(t, out.Body.String(), "private-provider-details")
		})
	}
	record, err := users.GetByID(base, id)
	require.NoError(t, err)
	require.NotContains(t, record.Preferences, "workspace")
}

type commitThenRemoveWorkspace struct {
	UserService
	native        *DatlyUserService
	externalReads int
}

func (s *commitThenRemoveWorkspace) UpdatePreferences(ctx context.Context, name string, p *PreferencesPatch) error {
	if err := s.native.UpdatePreferences(ctx, name, p); err != nil {
		return err
	}
	return s.native.UpdatePreferences(ctx, name, &PreferencesPatch{WorkspaceAction: &WorkspaceMutation{Action: "unpin", ItemID: "owned-pin"}})
}
func (s *commitThenRemoveWorkspace) GetByUsername(ctx context.Context, n string) (*User, error) {
	s.externalReads++
	return s.native.GetByUsername(ctx, n)
}
func TestPinReceiptBelongsToCompletedMutation(t *testing.T) {
	ctx := InjectUser(t.Context(), "receipt-owner")
	svc := newNativeUserServiceFixture(t)
	_, err := svc.UpsertWithProvider(ctx, "receipt-owner", "fixture", "", "local", "receipt-owner")
	require.NoError(t, err)
	wrapper := &commitThenRemoveWorkspace{UserService: svc, native: svc}
	runtime := &Runtime{ext: &authExtension{users: wrapper}}
	update, err := NewWorkspaceTools(runtime).Method("update")
	require.NoError(t, err)
	var receipt WorkspaceMutationReceipt
	require.NoError(t, update(ctx, &WorkspaceMutation{Action: "pin", GroupID: "checks", Label: "Checks", Pin: &WorkspacePin{ID: "owned-pin", Kind: "report", Provider: "publisher", URI: "report://example/summary"}}, &receipt))
	t.Logf("receipt status=%s item=%q extra readbacks=%d", receipt.Status, receipt.ItemID, wrapper.externalReads)
	require.Equal(t, "owned-pin", receipt.ItemID, "receipt must identify the pin that succeeded before the independent unpin")
	require.Zero(t, wrapper.externalReads, "native writer already held the committed preference candidate")
	current, err := runtime.WorkspacePreferences(ctx)
	require.NoError(t, err)
	require.Empty(t, current.Groups[0].Items)
}

func TestPreferencePatchBindsBothFormsToReadOwner(t *testing.T) {
	ctx := context.Background()
	users := newNativeUserServiceFixture(t)
	mux := http.NewServeMux()
	(&Handler{users: users}).RegisterPreferences(mux)
	request := func(caller context.Context, method, body string) *httptest.ResponseRecorder {
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, httptest.NewRequest(method, "/v1/api/me/preferences", strings.NewReader(body)).WithContext(caller))
		return out
	}
	account := func(name string) (context.Context, string) {
		id, err := users.UpsertWithProvider(ctx, name, name, "", "fixture", name)
		require.NoError(t, err)
		return iauth.WithCanonicalUserID(InjectUser(ctx, name), id), id
	}
	alice, aliceID := account("alice")
	bob, bobID := account("bob")
	read := request(alice, http.MethodGet, "")
	require.Equal(t, http.StatusOK, read.Code)
	var form struct {
		Owner string `json:"preferenceOwnerId"`
	}
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &form))
	require.Equal(t, aliceID, form.Owner)
	runtime := &Runtime{ext: &authExtension{users: users}}
	tasks, err := runtime.StarterTaskPreferences(alice)
	require.NoError(t, err)
	require.Equal(t, form.Owner, tasks.OwnerID)
	before, err := users.GetByID(ctx, bobID)
	require.NoError(t, err)
	for _, update := range []string{
		`"workspaceAction":{"action":"set_title","label":"Personal desk"}`,
		`"starterTasks":{"hidden":["personal#one"]}`,
	} {
		body := fmt.Sprintf(`{"expectedOwnerId":%q,%s}`, form.Owner, update)
		stale := request(bob, http.MethodPatch, body)
		require.Equal(t, http.StatusConflict, stale.Code)
		require.Contains(t, stale.Body.String(), preferencesChangedMessage)
		require.Equal(t, http.StatusNoContent, request(alice, http.MethodPatch, body).Code)
	}
	after, err := users.GetByID(ctx, bobID)
	require.NoError(t, err)
	require.Equal(t, before.settingsRaw, after.settingsRaw)
	saved, err := users.GetByID(ctx, aliceID)
	require.NoError(t, err)
	require.Contains(t, saved.Preferences, "workspace")
	require.Contains(t, saved.Preferences, "starterTasks")
	require.NotContains(t, saved.Preferences, "expectedOwnerId")
	require.NotContains(t, saved.Preferences, "preferenceOwnerId")
}
