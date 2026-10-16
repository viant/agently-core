package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	config "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
)

// ToolCatalogWarmOptions controls proactive metadata preparation after the
// authentication middleware has established the caller's identity.
type ToolCatalogWarmOptions struct {
	Disabled        bool
	ExcludedServers []string
	Timeout         time.Duration
	Interval        time.Duration
}

type userToolCatalogWarmer interface {
	WarmToolCatalogs(context.Context, []string) error
}
type catalogWarmSchedule struct {
	until  time.Time
	active bool
}

type warmCatalogManager interface {
	Names(context.Context) ([]string, error)
	Options(context.Context, string) (*config.MCPClient, error)
	PreflightCredential(context.Context, string) error
	WithAuthTokenContext(context.Context, string) context.Context
}

func withAuthenticatedToolCatalogWarmup(lifetime context.Context, next http.Handler, registry any, owner warmCatalogManager, opts ToolCatalogWarmOptions) http.Handler {
	warmer, ok := registry.(userToolCatalogWarmer)
	if opts.Disabled || !ok || owner == nil {
		return next
	}
	if lifetime == nil {
		lifetime = context.Background()
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	if opts.Interval <= 0 {
		opts.Interval = time.Minute
	}
	excluded := map[string]bool{}
	for _, name := range opts.ExcludedServers {
		excluded[strings.TrimSpace(name)] = true
	}
	// This is a bounded scheduling guard, not a second metadata cache. All
	// catalog content, expiry and source singleflight stay with the manager.
	var mu sync.Mutex
	scheduled := map[[32]byte]*catalogWarmSchedule{}
	slots := make(chan struct{}, 2)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		if authctx.EffectiveUserID(ctx) != "" && lifetime.Err() == nil {
			raw, _ := json.Marshal([]string{authctx.EffectiveUserID(ctx), authctx.Provider(ctx), authctx.Bearer(ctx), authctx.IDToken(ctx), authctx.MCPAuthToken(ctx, false), authctx.MCPAuthToken(ctx, true)})
			key := sha256.Sum256(raw)
			now := time.Now()
			mu.Lock()
			held := scheduled[key]
			if held == nil || (!held.active && !held.until.After(now)) {
				for scope, held := range scheduled {
					if !held.active && !held.until.After(now) {
						delete(scheduled, scope)
					}
				}
				if len(scheduled) < 256 {
					select {
					case slots <- struct{}{}:
						scheduled[key] = &catalogWarmSchedule{until: now.Add(opts.Interval), active: true}
						// Detach HTTP cancellation while retaining verified identity/tokens.
						// Runtime cancellation and a bounded deadline own the warmup lifetime.
						warmCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), opts.Timeout)
						stop := context.AfterFunc(lifetime, cancel)
						go func() {
							defer func() {
								stop()
								cancel()
								<-slots
								mu.Lock()
								scheduled[key].active = false
								mu.Unlock()
							}()
							warmCtx = manager.WithToolCatalogReadSnapshot(manager.WithNamesMemo(warmCtx))
							names, err := owner.Names(warmCtx)
							if err != nil {
								return
							}
							eligible := eligibleWarmCatalogs(warmCtx, owner, names, excluded)
							if len(eligible) > 0 {
								_ = warmer.WarmToolCatalogs(warmCtx, eligible)
							}
						}()
					default:
					}
				}
			}
			mu.Unlock()
		}
		next.ServeHTTP(w, req)
	})
}

func eligibleWarmCatalogs(ctx context.Context, owner warmCatalogManager, names []string, excluded map[string]bool) []string {
	var result []string
	for _, name := range names {
		if ctx.Err() != nil {
			break
		}
		if excluded[name] {
			continue
		}
		options, err := owner.Options(ctx, name)
		// Browser execution requires its conversation-bound registry and must
		// never create a server-side MCP connection during general API warmup.
		if err != nil || options == nil || options.ClientOptions == nil || options.IsBrowserExecution() {
			continue
		}
		if options.IsDelegatedAuth() {
			if owner.PreflightCredential(ctx, name) != nil {
				continue
			}
		} else if options.Auth != nil {
			// Existing workspace credentials can satisfy token-forwarding/BFF
			// servers. Legacy standalone OAuth flows are left to explicit use;
			// warming must not initiate an authorization interaction.
			if len(options.Auth.OAuth2ConfigURL) > 0 || !options.Auth.ShouldPassUserToken() {
				continue
			}
			tokenCtx := owner.WithAuthTokenContext(ctx, name)
			if authctx.MCPAuthToken(tokenCtx, options.Auth.UseIdToken) == "" {
				continue
			}
		}
		result = append(result, name)
	}
	return result
}
