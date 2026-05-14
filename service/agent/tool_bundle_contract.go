package agent

import (
	"context"
	"strings"
)

type requiredResolvedToolBundlesContextKey struct{}

// WithRequiredResolvedToolBundles marks a context as requiring the supplied
// bundle ids to resolve to at least one live tool definition. This is used for
// delegated child turns whose prompt profiles explicitly selected tool bundles:
// if those bundles resolve to zero tools, the run must fail instead of
// proceeding tool-less.
func WithRequiredResolvedToolBundles(ctx context.Context, bundles []string) context.Context {
	if len(bundles) == 0 {
		return ctx
	}
	normalized := normalizeRequiredToolBundles(bundles)
	if len(normalized) == 0 {
		return ctx
	}
	return context.WithValue(ctx, requiredResolvedToolBundlesContextKey{}, normalized)
}

func requiredResolvedToolBundlesFromContext(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	bundles, _ := ctx.Value(requiredResolvedToolBundlesContextKey{}).([]string)
	if len(bundles) == 0 {
		return nil
	}
	cloned := make([]string, len(bundles))
	copy(cloned, bundles)
	return cloned
}

func normalizeRequiredToolBundles(bundles []string) []string {
	if len(bundles) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, raw := range bundles {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}
