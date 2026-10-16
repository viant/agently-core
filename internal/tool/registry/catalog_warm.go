package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	authctx "github.com/viant/agently-core/internal/auth"
	schema "github.com/viant/mcp-protocol/schema"
	authconfig "github.com/viant/mcp/client/auth/config"
)

const catalogDiscoveryConcurrency = 4

type toolCatalogWarmContextKey struct{}

func (r *Registry) noteCatalogDiscoveryFailure(ctx context.Context, server, scope string, err error) {
	if proactive, _ := ctx.Value(toolCatalogWarmContextKey{}).(bool); !proactive {
		r.noteDiscoveryFailure(server, scope, err)
	}
}

type toolCatalogResult struct {
	tools []schema.Tool
	err   error
}

// listToolCatalogs populates the manager's existing catalog cache in parallel.
// Each worker retains the caller context; results retain server inventory order.
func (r *Registry) listToolCatalogs(ctx context.Context, servers []string) []toolCatalogResult {
	results := make([]toolCatalogResult, len(servers))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for i := 0; i < min(catalogDiscoveryConcurrency, len(servers)); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if err := ctx.Err(); err != nil {
					results[index].err = err
					continue
				}
				results[index].tools, results[index].err = r.listServerTools(ctx, servers[index])
			}
		}()
	}
	for index := range servers {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	return results
}

// WarmToolCatalogs prepares metadata for the authenticated caller using the
// existing manager cache and singleflight. The host supplies only eligible
// servers whose authorization is already established, removing its configured
// eager-loading exclusions first. The host must not include connections that need a new OAuth link.
// Private schemas remain manager-owned and never enter the shared registry.
func (r *Registry) WarmToolCatalogs(ctx context.Context, servers []string) error {
	if r == nil || ctx == nil || strings.TrimSpace(authctx.EffectiveUserID(ctx)) == "" {
		return nil
	}
	// Retain normal credential forwarding/cache keys while refusing the
	// transport's interactive 401 authorization/retry path during proactive work.
	ctx = authconfig.WithNoRetry(context.WithValue(ctx, toolCatalogWarmContextKey{}, true))
	selected := make([]string, 0, len(servers))
	seen := map[string]bool{}
	for _, server := range servers {
		server = strings.TrimSpace(server)
		if server == "" || seen[server] {
			continue
		}
		seen[server] = true
		// The host's existing links are authoritative. Resolve delegated credentials
		// before transport creation so expired/missing links fail without discovery.
		if r.isDelegatedAuthServer(ctx, server) {
			if owner, ok := r.mgr.(delegatedCredentialPreflighter); ok {
				if err := owner.PreflightCredential(ctx, server); err != nil {
					continue
				}
			} else {
				continue
			}
		}
		selected = append(selected, server)
	}
	results := r.listToolCatalogs(ctx, selected)
	var failures []error
	for i, result := range results {
		if result.err != nil {
			failures = append(failures, fmt.Errorf("warm tool catalog %s: %w", selected[i], result.err))
			continue
		}
		if r.isInternalServer(selected[i]) || r.isPublicToolCatalog(ctx, selected[i]) {
			r.cacheDiscoveredServerTools(ctx, selected[i], result.tools)
		}
	}
	return errors.Join(failures...)
}
