package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/authlog"
	"golang.org/x/oauth2"
)

// MCPLinkCSRFHeader carries the per-session CSRF token required on the
// cookie-authenticated POST initiate and DELETE disconnect endpoints. Clients
// obtain the token from the status response.
const MCPLinkCSRFHeader = "X-Agently-Csrf"

// registerMCPLinkRoutes mounts the delegated MCP OAuth link endpoints. The
// /v1/api/auth prefix bypasses the middleware rejection, so every handler
// enforces its own session authentication, canonical user, CSRF and rate
// limits.
func (a *authExtension) registerMCPLinkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/api/auth/mcp/callback", a.handleMCPOAuthCallback())
	mux.HandleFunc("POST /v1/api/auth/mcp/callback", a.handleMCPOAuthCallback())
	mux.HandleFunc("GET /v1/api/auth/mcp/{server}/status", a.handleMCPOAuthStatus())
	mux.HandleFunc("POST /v1/api/auth/mcp/{server}/initiate", a.handleMCPOAuthInitiate())
	mux.HandleFunc("POST /v1/api/auth/mcp/{server}/oob", a.handleMCPOAuthOOB())
	mux.HandleFunc("DELETE /v1/api/auth/mcp/{server}", a.handleMCPOAuthDisconnect())
}

// mcpOOBGrant is the token envelope produced by a trusted local OAuth client.
// The server still validates the grant cryptographically and against the
// configured provider, resource and scopes before storing it.
type mcpOOBGrant struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	IDToken      string `json:"idToken,omitempty"`
	TokenType    string `json:"tokenType,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
}

// mcpLinkIdentity is the authenticated caller of a link endpoint.
type mcpLinkIdentity struct {
	session         *Session
	canonicalUserID string
	effectiveUserID string
}

// requireMCPLinkSession enforces the version-one contract: linking requires
// an active cookie-backed workspace session with a resolvable canonical
// users.id. Bearer-only callers receive unsupported_flow; everything else
// unauthenticated receives 401. It never creates or upserts a user.
func (a *authExtension) requireMCPLinkSession(w http.ResponseWriter, r *http.Request) (*mcpLinkIdentity, bool) {
	if a == nil || a.mcpLink == nil {
		runtimeJSON(w, http.StatusNotFound, map[string]any{"code": "oauth_link_unavailable"})
		return nil, false
	}
	sess := a.currentSession(r)
	if sess == nil || sess.IsExpired() {
		if bearerTokenFromRequest(r) != "" {
			runtimeJSON(w, http.StatusBadRequest, map[string]any{
				"code":    "unsupported_flow",
				"message": "delegated MCP linking requires a cookie-backed workspace session",
			})
			return nil, false
		}
		runtimeJSON(w, http.StatusUnauthorized, map[string]any{"code": "authorization_required"})
		return nil, false
	}
	if strings.TrimSpace(sess.UserID) == "" {
		a.canonicalizeSessionUser(r.Context(), sess)
	}
	canonical := strings.TrimSpace(sess.UserID)
	if canonical == "" {
		// Fail closed without revealing whether the server, provider or user
		// mapping is the missing piece.
		runtimeJSON(w, http.StatusForbidden, map[string]any{"code": "oauth_link_unavailable"})
		return nil, false
	}
	return &mcpLinkIdentity{
		session:         sess,
		canonicalUserID: canonical,
		effectiveUserID: strings.TrimSpace(firstNonEmpty(sess.Subject, sess.Email, sess.Username)),
	}, true
}

func (a *authExtension) requireMCPLinkCSRF(w http.ResponseWriter, r *http.Request, identity *mcpLinkIdentity) bool {
	presented := strings.TrimSpace(r.Header.Get(MCPLinkCSRFHeader))
	if a.mcpLink.keyring.mcpCSRFTokenValid(identity.session.ID, presented) {
		return true
	}
	runtimeJSON(w, http.StatusForbidden, map[string]any{"code": "csrf_required"})
	return false
}

func writeMCPLinkRateLimited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	runtimeJSON(w, http.StatusTooManyRequests, map[string]any{"code": "rate_limited", "retryAfterSeconds": 60})
}

// writeMCPLinkError maps service failures onto the stable, non-enumerable
// wire representation. Details go to audit logs only.
func writeMCPLinkError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errMCPProviderDisabled):
		runtimeJSON(w, http.StatusForbidden, map[string]any{"code": "provider_disabled"})
	case errors.Is(err, errMCPResourceConflict):
		runtimeJSON(w, http.StatusConflict, map[string]any{"code": "provider_resource_conflict"})
	case errors.Is(err, errMCPStateInvalid):
		runtimeJSON(w, http.StatusBadRequest, map[string]any{"code": "oauth_state_invalid"})
	default:
		authlog.Log(r.Context(), authlog.Event{
			Op:             "mcp_auth_link_endpoint",
			Classification: "delegated_auth",
			Action:         "reject",
			Err:            err,
		})
		runtimeJSON(w, http.StatusBadRequest, map[string]any{"code": "oauth_link_unavailable"})
	}
}

func (a *authExtension) handleMCPOAuthStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := a.requireMCPLinkSession(w, r)
		if !ok {
			return
		}
		service := a.mcpLink
		if !service.limits.statusUser.Allow(identity.canonicalUserID) {
			writeMCPLinkRateLimited(w)
			return
		}
		result := service.status(r.Context(), r.PathValue("server"), identity.canonicalUserID, identity.session.ID)
		runtimeJSON(w, http.StatusOK, result)
	}
}

func (a *authExtension) handleMCPOAuthInitiate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := a.requireMCPLinkSession(w, r)
		if !ok {
			return
		}
		if !a.requireMCPLinkCSRF(w, r, identity) {
			return
		}
		service := a.mcpLink
		if !service.limits.initiateUser.Allow(identity.canonicalUserID) || !service.limits.initiateIP.Allow(clientIPKey(r)) {
			writeMCPLinkRateLimited(w)
			return
		}
		link, err := service.resolveServer(r.Context(), r.PathValue("server"))
		if err != nil {
			writeMCPLinkError(w, r, err)
			return
		}
		returnURL := strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("returnURL"), r.URL.Query().Get("returnUrl")))
		result, err := service.initiate(r.Context(), link, identity.canonicalUserID, identity.session.ID, returnURL, hostedMCPCallbackURL(r))
		if err != nil {
			writeMCPLinkError(w, r, err)
			return
		}
		runtimeJSON(w, http.StatusOK, result)
	}
}

// handleMCPOAuthOOB links a grant obtained by a local/headless OAuth client.
// It intentionally requires the same cookie-backed workspace identity and
// per-session CSRF protection as the browser initiation flow. Tokens supplied
// by the client are never trusted as identity assertions: completeOOBLink
// verifies their issuer, signature/introspection, audience/resource, expiry
// and scopes before associating them with the canonical workspace user.
func (a *authExtension) handleMCPOAuthOOB() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := a.requireMCPLinkSession(w, r)
		if !ok {
			return
		}
		if !a.requireMCPLinkCSRF(w, r, identity) {
			return
		}
		service := a.mcpLink
		if !service.limits.initiateUser.Allow(identity.canonicalUserID) || !service.limits.initiateIP.Allow(clientIPKey(r)) {
			writeMCPLinkRateLimited(w)
			return
		}

		var in mcpOOBGrant
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&in); err != nil || strings.TrimSpace(in.AccessToken) == "" {
			writeMCPCallbackFailure(w, r, http.StatusBadRequest)
			return
		}
		token := &oauth2.Token{
			AccessToken:  strings.TrimSpace(in.AccessToken),
			RefreshToken: strings.TrimSpace(in.RefreshToken),
			TokenType:    strings.TrimSpace(in.TokenType),
		}
		if raw := strings.TrimSpace(in.ExpiresAt); raw != "" {
			expiresAt, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeMCPCallbackFailure(w, r, http.StatusBadRequest)
				return
			}
			token.Expiry = expiresAt
		}
		extra := map[string]interface{}{}
		if idToken := strings.TrimSpace(in.IDToken); idToken != "" {
			extra["id_token"] = idToken
		}
		if len(extra) > 0 {
			token = token.WithExtra(extra)
		}

		result, err := service.completeOOBLink(r.Context(), r.PathValue("server"), token, identity.canonicalUserID, identity.effectiveUserID)
		if err != nil {
			writeMCPCallbackFailure(w, r, http.StatusBadRequest)
			return
		}
		runtimeJSON(w, http.StatusOK, map[string]any{"status": "connected", "server": result.ServerName, "provider": result.ProviderRef})
	}
}

func (a *authExtension) handleMCPOAuthDisconnect() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := a.requireMCPLinkSession(w, r)
		if !ok {
			return
		}
		if !a.requireMCPLinkCSRF(w, r, identity) {
			return
		}
		service := a.mcpLink
		if !service.limits.initiateUser.Allow(identity.canonicalUserID) || !service.limits.initiateIP.Allow(clientIPKey(r)) {
			writeMCPLinkRateLimited(w)
			return
		}
		// Disconnect is idempotent and non-enumerable: unknown servers,
		// unknown providers and missing rows all report success. It never
		// touches the workspace session.
		service.disconnect(r.Context(), r.PathValue("server"), identity.canonicalUserID, identity.effectiveUserID)
		runtimeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	}
}

func (a *authExtension) handleMCPOAuthCallback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a == nil || a.mcpLink == nil {
			http.NotFound(w, r)
			return
		}
		service := a.mcpLink
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		stateBlob := strings.TrimSpace(r.URL.Query().Get("state"))
		if r.Method == http.MethodPost && (code == "" || stateBlob == "") {
			if err := r.ParseForm(); err == nil {
				if code == "" {
					code = strings.TrimSpace(r.PostForm.Get("code"))
				}
				if stateBlob == "" {
					stateBlob = strings.TrimSpace(r.PostForm.Get("state"))
				}
			}
		}
		// Failed callbacks are rate limited by source IP and state hash without
		// revealing whether a state, user, provider or code exists.
		limitKey := clientIPKey(r)
		if stateBlob != "" {
			limitKey += "|" + mcpStateHash(stateBlob)
		}
		if !service.limits.callbackIP.Allow(limitKey) {
			writeMCPLinkRateLimited(w)
			return
		}
		sess := a.currentSession(r)
		if sess == nil || sess.IsExpired() {
			writeMCPCallbackFailure(w, r, http.StatusUnauthorized)
			return
		}
		if strings.TrimSpace(sess.UserID) == "" {
			a.canonicalizeSessionUser(r.Context(), sess)
		}
		canonical := strings.TrimSpace(sess.UserID)
		effective := strings.TrimSpace(firstNonEmpty(sess.Subject, sess.Email, sess.Username))
		result, err := service.completeCallback(r.Context(), code, stateBlob, sess, canonical, effective)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, errMCPResourceConflict) {
				status = http.StatusConflict
			}
			writeMCPCallbackFailure(w, r, status)
			return
		}
		if wantsJSON(r) || r.Method == http.MethodPost {
			runtimeJSON(w, http.StatusOK, map[string]any{"status": "connected", "server": result.ServerName, "provider": result.ProviderRef})
			return
		}
		if result.ReturnURL != "" {
			http.Redirect(w, r, result.ReturnURL, http.StatusTemporaryRedirect)
			return
		}
		writeMCPCallbackPopupCompletion(w, result.ServerName)
	}
}

// writeMCPCallbackFailure renders one uniform failure page/JSON without
// distinguishing state, session, user, provider or exchange failures beyond
// the conflict status mandated by the design.
func writeMCPCallbackFailure(w http.ResponseWriter, r *http.Request, status int) {
	if wantsJSON(r) || r.Method == http.MethodPost {
		runtimeJSON(w, status, map[string]any{"code": "oauth_link_failed"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprint(w, `<!DOCTYPE html><html><head><title>Connection failed</title></head><body>
<p>The provider connection could not be completed. Close this window and try connecting again.</p>
<script>
if (window.opener) {
  try { window.opener.postMessage({type: "agently.mcpAuth", status: "failed"}, window.location.origin); } catch (e) {}
  window.close();
}
</script>
</body></html>`)
}

// writeMCPCallbackPopupCompletion notifies the opener and closes the popup.
// No token, code or state material ever reaches this response.
func writeMCPCallbackPopupCompletion(w http.ResponseWriter, serverName string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	escaped := html.EscapeString(serverName)
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>Connected</title></head><body>
<p>Provider connected for %s. You can close this window.</p>
<script>
if (window.opener) {
  try { window.opener.postMessage({type: "agently.mcpAuth", status: "connected", server: %q}, window.location.origin); } catch (e) {}
  window.close();
}
</script>
</body></html>`, escaped, escaped)
}
