package manager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	token "github.com/viant/agently-core/internal/auth/token"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/mcp"
	protoclient "github.com/viant/mcp-protocol/client"
	mcpclient "github.com/viant/mcp/client"
	auth "github.com/viant/mcp/client/auth"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

// Provider returns client options for a given MCP server name.
type Provider interface {
	Options(ctx context.Context, serverName string) (*mcpcfg.MCPClient, error)
}

// Option configures Manager. It can return an error which will be bubbled up by New.
type Option func(*Manager) error

// WithTTL sets idle TTL before reaping a client.
func WithTTL(ttl time.Duration) Option { return func(m *Manager) error { m.ttl = ttl; return nil } }

// WithHandlerFactory sets a factory for per-connection client handlers (for elicitation, etc.).
func WithHandlerFactory(newHandler func() protoclient.Handler) Option {
	return func(m *Manager) error { m.newHandler = newHandler; return nil }
}

// WithCookieJar injects a host-controlled CookieJar that will be applied to
// newly created MCP clients via ClientOptions, overriding any per-provider jar.
func WithCookieJar(jar http.CookieJar) Option {
	return func(m *Manager) error { m.cookieJar = jar; return nil }
}

// JarProvider returns a per-request CookieJar (e.g., per-user) chosen from context.
// When provided, it takes precedence over the static cookieJar set via WithCookieJar.
type JarProvider func(ctx context.Context) (http.CookieJar, error)

// WithCookieJarProvider injects a provider that selects a CookieJar per request (e.g., per user).
func WithCookieJarProvider(p JarProvider) Option {
	return func(m *Manager) error { m.jarProvider = p; return nil }
}

// WithAuthRoundTripper enables auth integration by attaching the provided
// RoundTripper as an Authorizer interceptor to created MCP clients.
func WithAuthRoundTripper(rt *authtransport.RoundTripper) Option {
	return func(m *Manager) error { m.authRT = rt; return nil }
}

// AuthRTProvider returns a per-request auth RoundTripper (e.g., per-user) chosen from context.
// When provided, it takes precedence over the static authRT set via WithAuthRoundTripper.
type AuthRTProvider func(ctx context.Context) *authtransport.RoundTripper

// WithAuthRoundTripperProvider injects a provider that selects an auth RoundTripper per request.
func WithAuthRoundTripperProvider(p AuthRTProvider) Option {
	return func(m *Manager) error { m.authRTProvider = p; return nil }
}

// UserIDExtractor returns a user identifier from context for pool isolation.
// When set, the pool key becomes "userID:convID" instead of just "convID"
// to prevent shared conversations from leaking MCP auth across users.
type UserIDExtractor func(ctx context.Context) string

// WithUserIDExtractor sets the function used to derive a user-scoped pool key.
func WithUserIDExtractor(fn UserIDExtractor) Option {
	return func(m *Manager) error { m.userIDFn = fn; return nil }
}

// WithTokenProvider injects the shared token lifecycle manager so MCP requests
// can refresh tokens just before outbound auth is attached.
func WithTokenProvider(tp token.Provider) Option {
	return func(m *Manager) error { m.tokenProvider = tp; return nil }
}

// Manager caches MCP clients per (userID:conversationID, serverName) and handles idle reaping.
type Manager struct {
	prov           Provider
	ttl            time.Duration
	newHandler     func() protoclient.Handler
	cookieJar      http.CookieJar
	jarProvider    JarProvider
	authRT         *authtransport.RoundTripper
	authRTProvider AuthRTProvider
	userIDFn       UserIDExtractor
	tokenProvider  token.Provider

	mu   sync.Mutex
	pool map[string]map[string]*entry // poolKey -> serverName -> entry
}

type entry struct {
	client mcpclient.Interface
	usedAt time.Time
}

// New creates a Manager with the given Provider and options.
func New(prov Provider, opts ...Option) (*Manager, error) {
	// Default idle TTL reduced to 5 minutes to ensure per-conversation
	// MCP clients are disconnected and removed promptly when idle.
	m := &Manager{prov: prov, ttl: 5 * time.Minute, pool: map[string]map[string]*entry{}}
	for _, o := range opts {
		if err := o(m); err != nil {
			return nil, fmt.Errorf("mcp manager option: %w", err)
		}
	}
	return m, nil
}

// poolKey returns a user-scoped key for the connection pool.
// When a UserIDExtractor is configured, the key is "userID:convID" to
// prevent shared conversations from leaking MCP auth/tokens across users.
func (m *Manager) poolKey(ctx context.Context, convID string) string {
	if m.userIDFn != nil {
		if uid := strings.TrimSpace(m.userIDFn(ctx)); uid != "" {
			return uid + ":" + convID
		}
	}
	return convID
}

// Options exposes the underlying provider client options (authoring metadata,
// timeouts, etc.) for a given server name.
func (m *Manager) Options(ctx context.Context, serverName string) (*mcpcfg.MCPClient, error) {
	if m == nil || m.prov == nil {
		return nil, errors.New("mcp manager: provider not configured")
	}
	return m.prov.Options(ctx, serverName)
}

// Get returns an MCP client for (user+convID, serverName), creating it if needed.
// When a UserIDExtractor is configured, the pool key includes the user ID to
// prevent shared conversations from leaking MCP auth/tokens across users.
func (m *Manager) Get(ctx context.Context, convID, serverName string) (mcpclient.Interface, error) {
	if m.prov == nil {
		return nil, errors.New("mcp manager: provider not configured")
	}
	key := m.poolKey(ctx, convID)
	m.mu.Lock()
	// Intentionally avoid `defer m.mu.Unlock()` here.
	// This lock must be released before client creation/network setup
	// (provider options, transport init) to prevent global manager stalls.
	// Maintain per-conversation client to correlate elicitation correctly.
	if m.pool[key] == nil {
		m.pool[key] = map[string]*entry{}
	}
	if e := m.pool[key][serverName]; e != nil && e.client != nil {
		e.usedAt = time.Now()
		client := e.client
		m.mu.Unlock()
		return client, nil
	}
	m.mu.Unlock()

	client, err := m.newClient(ctx, convID, serverName)
	if err != nil {
		return nil, err
	}

	// Double-check under lock: another goroutine may have inserted meanwhile.
	m.mu.Lock()
	if m.pool[key] == nil {
		m.pool[key] = map[string]*entry{}
	}
	if e := m.pool[key][serverName]; e != nil && e.client != nil {
		e.usedAt = time.Now()
		existing := e.client
		m.mu.Unlock()
		closeClientBestEffort(client)
		return existing, nil
	}
	m.pool[key][serverName] = &entry{client: client, usedAt: time.Now()}
	m.mu.Unlock()
	return client, nil
}

func (m *Manager) newClient(ctx context.Context, convID, serverName string) (mcpclient.Interface, error) {
	if m.prov == nil {
		return nil, errors.New("mcp manager: provider not configured")
	}
	opts, err := m.prov.Options(ctx, serverName)
	if err != nil {
		return nil, err
	}
	if opts == nil {
		return nil, errors.New("mcp manager: nil client options")
	}
	if opts.ClientOptions == nil {
		return nil, errors.New("mcp manager: missing client options")
	}
	opts.Init()
	// Select per-request jar (provider beats static) and merge provider cookies.json into it
	// (if both present) before override,
	// so the very first POST can carry previously minted session cookies.
	var effectiveJar http.CookieJar
	if m.jarProvider != nil {
		var jerr error
		effectiveJar, jerr = m.jarProvider(ctx)
		if jerr != nil {
			return nil, fmt.Errorf("cookie jar provider: %w", jerr)
		}
	} else {
		effectiveJar = m.cookieJar
	}
	if effectiveJar != nil && opts.ClientOptions != nil {
		// Determine origin from transport URL
		origin := strings.TrimSpace(opts.ClientOptions.Transport.URL)
		if origin != "" {
			if u, perr := url.Parse(origin); perr == nil {
				if pj := opts.ClientOptions.CookieJar; pj != nil && pj != effectiveJar {
					if cs := pj.Cookies(u); len(cs) > 0 {
						effectiveJar.SetCookies(u, cs)
					}
				}
			}
		}
		// Override CookieJar with selected jar to ensure reuse across conversations
		opts.ClientOptions.CookieJar = effectiveJar
	}
	handler := m.newHandler
	if handler == nil {
		handler = func() protoclient.Handler { return nil }
	}
	h := handler()
	// If handler supports setting conversation id, assign it.
	if ca, ok := h.(interface{ SetConversationID(string) }); ok {
		ca.SetConversationID(convID)
	}
	// Resolve per-user auth RoundTripper.
	var rt *authtransport.RoundTripper
	if m.authRTProvider != nil {
		rt = m.authRTProvider(ctx)
	}
	if rt == nil {
		rt = m.authRT
	}
	// Only inject the auth RT into the HTTP transport when the MCP config
	// explicitly has auth settings. For auth:null configs, the token is
	// passed via the JSON-RPC interceptor (per-request) because
	// mcp.NewClient uses context.Background() for Initialize() which has
	// no user token.
	hasExplicitAuth := opts.ClientOptions.Auth != nil
	if rt != nil && hasExplicitAuth {
		opts.ClientOptions.SetAuthTransport(rt, &http.Client{Transport: rt, Jar: effectiveJar})
	}
	cli, err := mcp.NewClient(h, opts.ClientOptions)
	if err != nil {
		return nil, err
	}
	// Attach the MCP-level auth interceptor for per-request token injection
	// and protocol-level 401 retries.
	if rt != nil {
		authorizer := auth.NewAuthorizer(rt)
		mcpclient.WithAuthInterceptor(authorizer)(cli)
	}
	return cli, nil
}

func closeClientBestEffort(client mcpclient.Interface) {
	if client == nil {
		return
	}
	if c, ok := client.(interface{ Close() error }); ok {
		_ = c.Close()
		return
	}
	if c, ok := client.(interface{ Close() }); ok {
		c.Close()
		return
	}
	if s, ok := client.(interface{ Shutdown(context.Context) error }); ok {
		_ = s.Shutdown(context.Background())
		return
	}
}

// Touch updates last-used time for (convID, serverName).
func (m *Manager) Touch(convID, serverName string) {
	m.mu.Lock()
	// Touch is called without context, so search all pool keys that end with the convID.
	for key, perServer := range m.pool {
		if key == convID || strings.HasSuffix(key, ":"+convID) {
			if e := perServer[serverName]; e != nil {
				e.usedAt = time.Now()
			}
		}
	}
	m.mu.Unlock()
}

// CloseConversation drops all clients for a conversation (across all users).
// Note: underlying transports may keep connections if the library doesn't expose Close.
func (m *Manager) CloseConversation(convID string) {
	var toClose []mcpclient.Interface
	m.mu.Lock()
	for key, perServer := range m.pool {
		if key == convID || strings.HasSuffix(key, ":"+convID) {
			for server, e := range perServer {
				if e != nil && e.client != nil {
					toClose = append(toClose, e.client)
				}
				delete(perServer, server)
			}
			delete(m.pool, key)
		}
	}
	m.mu.Unlock()
	for _, client := range toClose {
		closeClientBestEffort(client)
	}
}

// Reap closes idle clients beyond TTL by dropping references.
func (m *Manager) Reap() {
	cutoff := time.Now().Add(-m.ttl)
	var toClose []mcpclient.Interface
	m.mu.Lock()
	for convID, perServer := range m.pool {
		for server, e := range perServer {
			if e == nil || e.usedAt.Before(cutoff) {
				if e != nil && e.client != nil {
					toClose = append(toClose, e.client)
				}
				delete(perServer, server)
			}
		}
		if len(perServer) == 0 {
			delete(m.pool, convID)
		}
	}
	m.mu.Unlock()
	for _, client := range toClose {
		closeClientBestEffort(client)
	}
}

// Reconnect drops the cached client for (convID, serverName) and creates a new one.
// It returns the fresh client or an error if recreation fails.
func (m *Manager) Reconnect(ctx context.Context, convID, serverName string) (mcpclient.Interface, error) {
	if m == nil {
		return nil, errors.New("mcp manager: nil manager")
	}
	key := m.poolKey(ctx, convID)
	// Drop existing entry to force re-creation
	var toClose mcpclient.Interface
	m.mu.Lock()
	if m.pool[key] != nil {
		if e := m.pool[key][serverName]; e != nil && e.client != nil {
			toClose = e.client
		}
		delete(m.pool[key], serverName)
		if len(m.pool[key]) == 0 {
			delete(m.pool, key)
		}
	}
	m.mu.Unlock()
	closeClientBestEffort(toClose)
	// Recreate
	return m.Get(ctx, convID, serverName)
}

// StartReaper launches a background goroutine that periodically invokes Reap
// until the provided context is cancelled or the returned stop function is
// called. If interval is non-positive, ttl/2 is used with a minimum of 1 minute.
func (m *Manager) StartReaper(ctx context.Context, interval time.Duration) (stop func()) {
	min := time.Minute
	if interval <= 0 {
		interval = m.ttl / 2
		if interval < min {
			interval = min
		}
	}
	done := make(chan struct{})
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.Reap()
			case <-ctx.Done():
				return
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}
