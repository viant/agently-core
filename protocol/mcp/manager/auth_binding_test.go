package manager

import (
	"context"
	"testing"
	"time"

	fsstore "github.com/viant/agently-core/workspace/store/fs"
	authcfg "github.com/viant/mcp/client/auth/config"
)

func testBindingProvider() *authcfg.OAuthProvider {
	return &authcfg.OAuthProvider{
		ID:            "approved-provider",
		Issuer:        "https://idp.test/",
		DefaultClient: "web",
		Clients: map[string]*authcfg.OAuthClient{
			"web": {ConfigURL: "mem://client.json", RedirectURI: "https://app.test/cb", Confidential: true, UsePKCE: true},
		},
	}
}

func newBindingManager(t *testing.T) *Manager {
	t.Helper()
	registry, err := authcfg.NewStaticRegistry(testBindingProvider())
	if err != nil {
		t.Fatalf("NewStaticRegistry() error = %v", err)
	}
	mgr, err := New(nil,
		WithProviderRegistry(registry),
		WithAuthBindingStore(NewAuthBindingStore(fsstore.NewStateStore(t.TempDir()))),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return mgr
}

func TestAuthBindingStore_SaveLoadInvalidate(t *testing.T) {
	state := fsstore.NewStateStore(t.TempDir())
	store := NewAuthBindingStore(state)
	ctx := context.Background()
	binding := &MCPAuthBinding{
		ServerName:  "dev6",
		Origin:      "https://mcp.test",
		MetadataURL: "https://mcp.test/.well-known/oauth-protected-resource",
		ProviderRef: "approved-provider",
		ClientRef:   "web",
		Issuer:      "https://idp.test",
		Resource:    "https://mcp.test/mcp",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := store.Save(ctx, binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// A fresh store over the same state directory reloads the persisted file.
	reloaded := NewAuthBindingStore(state)
	loaded, err := reloaded.Load(ctx, "dev6")
	if err != nil || loaded == nil || loaded.ProviderRef != "approved-provider" {
		t.Fatalf("Load() = %+v, %v", loaded, err)
	}
	store.Invalidate(ctx, "dev6")
	if loaded, _ := NewAuthBindingStore(state).Load(ctx, "dev6"); loaded != nil {
		t.Fatalf("binding survived Invalidate(): %+v", loaded)
	}
}

func TestAuthBindingStore_ExpiredBindingIgnored(t *testing.T) {
	state := fsstore.NewStateStore(t.TempDir())
	store := NewAuthBindingStore(state)
	ctx := context.Background()
	if err := store.Save(ctx, &MCPAuthBinding{
		ServerName:  "dev6",
		ProviderRef: "approved-provider",
		Issuer:      "https://idp.test",
		ExpiresAt:   time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if loaded, _ := store.Load(ctx, "dev6"); loaded != nil {
		t.Fatalf("expired binding served: %+v", loaded)
	}
}
