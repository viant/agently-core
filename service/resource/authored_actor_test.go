package resource

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	tplsvc "github.com/viant/agently-core/protocol/tool/service/template"
	auth "github.com/viant/agently-core/service/auth"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	bundlerepo "github.com/viant/agently-core/workspace/repository/templatebundle"
	"github.com/viant/mcp"
	protoclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type authoredActorKey struct{}

func authoredActorContext(subject string) context.Context {
	ctx := authctx.WithCanonicalUserID(authctx.WithProvider(authctx.WithUserInfo(auth.InjectUser(context.Background(), subject), &authctx.UserInfo{Subject: subject}), "fixture"), subject)
	return context.WithValue(authctx.WithBearer(ctx, "owned-"+subject), authoredActorKey{}, identity.VerifiedActor{Subject: subject, Issuer: "owned-idp", TenantID: "tenant", AccountID: "account", IdentityRevision: "1", ValidUntil: time.Now().Add(time.Minute)})
}

type authoredPartialClient struct{ mcpclient.Interface }

func (c *authoredPartialClient) SetNotificationHandler(h protoclient.Handler) {
	c.Interface.(interface{ SetNotificationHandler(protoclient.Handler) }).SetNotificationHandler(h)
}
func (c *authoredPartialClient) Close() error {
	return c.Interface.(interface{ Close() error }).Close()
}
func (c *authoredPartialClient) ListTools(ctx context.Context, cursor *string, opts ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	got, err := c.Interface.ListTools(ctx, cursor, opts...)
	if err != nil {
		return nil, err
	}
	copy := *got
	copy.Tools = nil
	for _, t := range got.Tools {
		if strings.HasPrefix(t.Name, "namespaces/") || strings.HasPrefix(t.Name, "templates/") || strings.HasPrefix(t.Name, "intents/") {
			copy.Tools = append(copy.Tools, t)
		}
	}
	return &copy, nil
}
func TestAuthoredReadersPrivateActorsPartialProviderAliasesAndDenial(t *testing.T) {
	for _, transport := range []string{"memory", "ordinary-http"} {
		t.Run(transport, func(t *testing.T) {
			root := t.TempDir()
			var dirs []ExtensionDirectory
			var compilers []*AuthoredCompilation
			for _, ns := range []string{"alice", "bob", "shared"} {
				dir := ns + "/templates"
				require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0700))
				require.NoError(t, os.WriteFile(filepath.Join(root, dir, "output.yaml"), []byte("id: original\nname: Original\ninstructions: private-"+ns+"\n"), 0600))
				c := NewAuthoredCompilation(dir)
				compilers = append(compilers, c)
				dirs = append(dirs, c.TemplateDirectory(ns))
			}
			defs, err := SnapshotExtensionDefinitions(context.Background(), root, dirs)
			require.NoError(t, err)
			snap, err := NewNativeAssetSnapshot(context.Background(), root, defs, NativeSnapshotOptions{BeforeCompile: func(ctx context.Context, r *ExtensionReader) error {
				for _, c := range compilers {
					if err := c.Prepare(ctx, r); err != nil {
						return err
					}
				}
				return nil
			}})
			require.NoError(t, err)
			for _, c := range compilers {
				c.Release()
			}
			actor := func(ctx context.Context) (identity.VerifiedActor, error) {
				a, ok := ctx.Value(authoredActorKey{}).(identity.VerifiedActor)
				if !ok {
					return a, identity.ErrResourceDenied
				}
				return a, nil
			}
			verify := func(ctx context.Context, a identity.VerifiedActor) error {
				fresh, err := actor(ctx)
				if err != nil || fresh.Subject != a.Subject || !a.Valid(time.Now()) {
					return identity.ErrResourceDenied
				}
				return nil
			}
			bindings, err := snap.Bindings("owned-template-provider", func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
				return authoredTestPolicy{a}, nil
			})
			require.NoError(t, err)
			var deny atomic.Bool
			var shared atomic.Bool
			p, err := NewLocalProvider(LocalConfig{ProviderIdentity: "owned-template-provider", Actor: actor, Verify: verify, Authorize: func(_ context.Context, a identity.VerifiedActor, u identity.ResourceURI, op string) error {
				if u.Namespace != a.Subject && !(shared.Load() && u.Namespace == "shared") {
					return identity.ErrResourceDenied
				}
				if op == "resource.get" && deny.Load() {
					return identity.ErrResourceDenied
				}
				return nil
			}, Bindings: bindings, Validators: map[string]LocalResourceValidator{"template": ValidateTemplateDefinition}})
			require.NoError(t, err)

			var mgr *manager.Manager
			if transport == "memory" {
				mgr, err = manager.New(nil)
				require.NoError(t, err)
				require.NoError(t, mgr.RegisterLocal(context.Background(), "partial", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: p.ProviderIdentity()}, func(context.Context) (mcpclient.Interface, error) {
					local, err := NewLocalMCPClient(p)
					if err != nil {
						return nil, err
					}
					return &authoredPartialClient{local}, nil
				}))
			} else {
				handler, err := NewLocalMCPHandler(p, func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						subject := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer owned-")
						if subject != "alice" && subject != "bob" {
							http.Error(w, "denied", 401)
							return
						}
						next.ServeHTTP(w, r.WithContext(authoredActorContext(subject)))
					})
				})
				require.NoError(t, err)
				server := httptest.NewServer(handler)
				defer server.Close()
				pass := true
				options := fixtureOptions{"partial": {PrimitiveProviderIdentity: p.ProviderIdentity(), ClientOptions: &mcp.ClientOptions{Name: "ordinary", Version: "1", ProtocolVersion: schema.LatestProtocolVersion, Auth: &mcp.ClientAuth{PassUserToken: &pass}, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL + "/mcp"}}}}}
				mgr, err = manager.New(options)
				require.NoError(t, err)
				defer mgr.CloseConversation("")
			}
			g := NewGateway(mgr, actor, verify, "")
			defer g.Close()
			r := NewTemplateReader(g)
			for _, subject := range []string{"alice", "bob", "alice"} {
				ctx := authoredActorContext(subject)
				items, err := r.LoadAll(ctx)
				require.NoError(t, err)
				require.Len(t, items, 1)
				require.Empty(t, items[0].Instructions)
				got, err := r.Load(ctx, "Original")
				require.NoError(t, err)
				require.Equal(t, "private-"+subject, got.Instructions)
				require.Equal(t, "template://"+subject+"/output", got.ResourceURI)
			}
			foreign, err := r.Load(authoredActorContext("alice"), "template://bob/output")
			require.Error(t, err)
			require.Nil(t, foreign)
			tool := tplsvc.New(tplrepo.NewWithReader(r), bundlerepo.NewWithReader(NewTemplateBundleReader(g)))
			get, err := tool.Method("get")
			require.NoError(t, err)
			var response tplsvc.GetOutput
			require.NoError(t, get(authoredActorContext("alice"), &tplsvc.GetInput{Name: "Original"}, &response))
			require.Equal(t, "private-alice", response.Instructions)
			deny.Store(true)
			alice := authoredActorContext("alice")
			if transport == "ordinary-http" {
				// This bearer-only fixture has no resumable authenticated event stream;
				// closing its pooled client forces reconnection under the next read's
				// original verified transport context.
				mgr.EvictUserServer("alice", "partial")
			} else {
				beforeEvent := mgr.ResourceCatalogGeneration(alice, "partial")
				require.NoError(t, snap.Reload(context.Background()))
				require.NotEqual(t, beforeEvent, mgr.ResourceCatalogGeneration(alice, "partial"))
			}

			got, err := r.Load(authoredActorContext("alice"), "original")
			require.Error(t, err)
			require.Nil(t, got, "cached metadata is never body authority")
			response = tplsvc.GetOutput{}
			require.Error(t, get(authoredActorContext("alice"), &tplsvc.GetInput{Name: "Original"}, &response))
			require.Empty(t, response.Instructions)
			deny.Store(false)
			shared.Store(true)
			g.Invalidate("partial")
			mgr.EvictUserServer("alice", "partial")
			got, err = r.Load(authoredActorContext("alice"), "Original")
			require.ErrorIs(t, err, ErrCollision)
			require.Nil(t, got)
			got, err = r.Load(authoredActorContext("alice"), "template://alice/output")
			require.NoError(t, err)
			require.Equal(t, "private-alice", got.Instructions)
			raw, _ := json.Marshal(got)
			require.NotContains(t, string(raw), "ResourceURI", "routing metadata never changes original DTO serialization")
		})
	}
}
