package integrate

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/scy/auth/flow"
	"golang.org/x/oauth2"
)

var ErrHeadlessAuthRequired = errors.New("interactive MCP authentication required in headless mode")

// HeadlessAuthRequiredError indicates that a headless runtime hit an MCP auth
// flow that requires user interaction.
type HeadlessAuthRequiredError struct {
	Origin           string
	AuthorizationURL string
}

func (e *HeadlessAuthRequiredError) Error() string {
	target := strings.TrimSpace(e.Origin)
	if target == "" {
		target = "MCP server"
	}
	if strings.TrimSpace(e.AuthorizationURL) != "" {
		return fmt.Sprintf("mcp unauthorized headless: interactive authentication required for %s", target)
	}
	return fmt.Sprintf("mcp headless auth required: interactive authentication required for %s", target)
}

func (e *HeadlessAuthRequiredError) Unwrap() error {
	return ErrHeadlessAuthRequired
}

type headlessAuthFlow struct{}

func (headlessAuthFlow) Token(ctx context.Context, cfg *oauth2.Config, options ...flow.Option) (*oauth2.Token, error) {
	origin := oauthConfigOrigin(cfg)
	logHeadlessAuth(ctx, origin, "oauth_token_flow_required")
	return nil, &HeadlessAuthRequiredError{Origin: origin}
}

type headlessFailureRT struct {
	inner http.RoundTripper
}

// NewHeadlessFailureRoundTripper wraps a transport so headless runtimes fail
// immediately when a 401 response advertises an interactive authorization URL.
func NewHeadlessFailureRoundTripper(inner http.RoundTripper) http.RoundTripper {
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &headlessFailureRT{inner: inner}
}

func (w *headlessFailureRT) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := w.inner.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	authURL := parseAuthorizationURI(resp)
	if authURL == "" {
		return resp, nil
	}
	resp.Body.Close()
	origin := strings.TrimSpace(req.URL.Host)
	logHeadlessAuth(req.Context(), origin, "authorization_uri_requires_interaction")
	return nil, &HeadlessAuthRequiredError{
		Origin:           origin,
		AuthorizationURL: authURL,
	}
}

func oauthConfigOrigin(cfg *oauth2.Config) string {
	if cfg == nil {
		return ""
	}
	for _, raw := range []string{cfg.Endpoint.AuthURL, cfg.Endpoint.TokenURL} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if u, err := url.Parse(raw); err == nil && strings.TrimSpace(u.Host) != "" {
			return strings.TrimSpace(u.Host)
		}
	}
	return ""
}

func logHeadlessAuth(ctx context.Context, origin, reason string) {
	mode, _ := memory.DiscoveryModeFromContext(ctx)
	userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if mode.Scheduler {
		log.Printf("scheduler auth headless fallback schedule=%q run=%q user=%q server=%q reason=%q",
			strings.TrimSpace(mode.ScheduleID),
			strings.TrimSpace(mode.ScheduleRunID),
			userID,
			strings.TrimSpace(origin),
			strings.TrimSpace(reason),
		)
		return
	}
	log.Printf("mcp unauthorized headless user=%q server=%q reason=%q", userID, strings.TrimSpace(origin), strings.TrimSpace(reason))
}
