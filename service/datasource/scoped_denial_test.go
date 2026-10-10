package datasource

import (
	"context"
	"encoding/json"
	"errors"
	ds "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	rc "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/forge/backend/types"
	"testing"
	"time"
)

func TestScopedMCPDenialRequiresFreshHostRootAndOriginalBoundary(t *testing.T) {
	for _, mode := range []string{"allow", "allow-large-entity", "default closed", "401", "root revoke", "pin changed", "descriptor changed", "actor change on callback", "context cancelled", "untyped 403", "pin expires before recheck", "pin expires during root", "cancel during root"} {
		t.Run(mode, func(t *testing.T) {
			rootCalls, executions := 0, 0
			now := time.Now()
			expected := json.Number("42")
			if mode == "allow-large-entity" {
				expected = json.Number("9007199254740993")
			}
			selection := []json.Number{expected}
			current := true
			pin := identity.ResolvedResource{URI: "window://owned/editor", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte("definition"))}, ProviderIdentity: "owned", AuthorityBinding: "actor", ValidUntil: now.Add(time.Minute)}
			target := &types.WindowTarget{Platform: "web", SelectionToken: "held"}
			d := &ds.DataSource{ID: "rows", Backend: &ds.Backend{Kind: ds.BackendMCPTool, Service: "fixture", Method: "Rows"}}
			descriptor, _ := json.Marshal(d)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			options := Options{Now: func() time.Time { return now }, DisableCache: true, ResolveResource: func(_ context.Context, in identity.ResolvedResource) (*identity.ResolvedResource, error) {
				if !current {
					return nil, identity.ErrResourceDenied
				}
				copy := in
				if mode == "pin changed" && executions > 0 {
					copy.AuthorityBinding = "other"
				}
				return &copy, nil
			}, ResolveDefinition: func(_ context.Context, p identity.ResolvedResource, tgt *types.WindowTarget, id string) (*ds.DataSource, error) {
				if p != pin || id != "rows" || !types.SameWindowTarget(tgt, target) {
					return nil, identity.ErrResourceDenied
				}
				var copy ds.DataSource
				json.Unmarshal(descriptor, &copy)
				if mode == "descriptor changed" && executions > 0 {
					copy.Backend.Method = "Other"
				}
				return &copy, nil
			}, AuthorizeDefinition: func(context.Context, *ds.DataSource, map[string]interface{}) error {
				if !current {
					return identity.ErrResourceDenied
				}
				return nil
			}, DenialRootRead: func(ctx context.Context, _ *ds.DataSource, in map[string]interface{}) error {
				rootCalls++
				if mode == "pin expires during root" {
					now = now.Add(2 * time.Minute)
				}
				if mode == "cancel during root" {
					cancel()
				}
				if rc.WindowParametersFromContext(ctx)["Entity"].([]any)[0] != expected {
					t.Fatal("selection changed after dispatch")
				}
				if in["Entity"].([]any)[0] != expected {
					t.Fatal("inputs changed after dispatch")
				}
				if mode == "root revoke" {
					return identity.ErrResourceDenied
				}
				if mode == "actor change on callback" {
					current = false
				}
				return nil
			}, Executor: permissionResolverTestExecutor(func(context.Context, string, map[string]interface{}) (string, error) {
				executions++
				if mode == "pin expires before recheck" {
					now = now.Add(2 * time.Minute)
				}
				selection[0] = json.Number("99")
				if mode == "context cancelled" {
					cancel()
				}
				if mode == "untyped 403" {
					return "", errors.New("403 forbidden")
				}
				status := 403
				if mode == "401" {
					status = 401
				}
				return "", &ds.UpstreamAuthorizationError{Status: status}
			})}
			if mode == "default closed" {
				options.DenialRootRead = nil
			}
			service := New(options)
			out, err := service.Fetch(ctx, "rows", map[string]interface{}{"Entity": selection}, FetchOptions{Resource: &pin, Target: target, WindowParameters: map[string]any{"Entity": selection}})
			if out != nil || err == nil || executions != 1 {
				t.Fatal(out, err, executions)
			}
			if mode == "allow" || mode == "allow-large-entity" {
				if !errors.Is(err, ds.ErrDatasourceDenied) || rootCalls != 1 {
					t.Fatal(err, rootCalls)
				}
			} else if errors.Is(err, ds.ErrDatasourceDenied) {
				t.Fatal("global denial downgraded", mode, err)
			}
		})
	}
}

type permissionResolverTestExecutor func(context.Context, string, map[string]interface{}) (string, error)

func (f permissionResolverTestExecutor) Execute(c context.Context, n string, a map[string]interface{}) (string, error) {
	return f(c, n, a)
}
