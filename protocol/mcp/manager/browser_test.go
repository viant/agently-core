package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	iauth "github.com/viant/agently-core/internal/auth"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpclient "github.com/viant/mcp/client"
)

type browserProvider struct{ config *cfg.MCPClient }

func (p browserProvider) Options(context.Context, string) (*cfg.MCPClient, error) {
	return p.config, nil
}
func (p browserProvider) Names(context.Context) ([]string, error) { return []string{"device"}, nil }
func TestBrowserMCPNeverCreatesBackendClientOrAttachesCredentials(t *testing.T) {
	config := &cfg.MCPClient{ExecutionLocation: "browser", BrowserTransport: &cfg.BrowserTransport{Type: "chrome-extension", ExtensionID: strings.Repeat("a", 32), PortName: "generic-v1"}}
	var created, authTouched bool
	manager, err := New(browserProvider{config}, WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) {
		created = true
		return &stubClient{}, nil
	}), WithCookieJarProvider(func(context.Context) (http.CookieJar, error) { authTouched = true; return nil, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.Get(context.Background(), "conversation", "device"); !errors.Is(err, ErrBrowserExecutionRequired) {
		t.Fatal(err)
	}
	if err = manager.PreflightCredential(context.Background(), "device"); !errors.Is(err, ErrBrowserExecutionRequired) {
		t.Fatal(err)
	}
	if created || authTouched {
		t.Fatal("browser config touched backend transport/auth")
	}
	if names, err := manager.Names(context.Background()); err != nil || len(names) != 0 {
		t.Fatal("browser server entered backend discovery")
	}
	ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "owner"})
	descriptors, err := manager.BrowserDescriptors(ctx)
	if err != nil || len(descriptors) != 1 {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(descriptors)
	for _, forbidden := range []string{"credential", "authorization", "command", "cookie", "token"} {
		if strings.Contains(strings.ToLower(string(raw)), forbidden) {
			t.Fatal("private config field projected")
		}
	}
	if _, err = manager.BrowserDescriptors(context.Background()); err == nil {
		t.Fatal("anonymous descriptor access")
	}
}

func TestBrowserMCPRepoProviderDoesNotCreateCredentialState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	t.Setenv("AGENTLY_RUNTIME_ROOT", root+"/runtime")
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
	if err := os.MkdirAll(filepath.Join(root, "mcp"), 0700); err != nil {
		t.Fatal(err)
	}
	config := "name: device\nexecutionLocation: browser\nbrowserTransport:\n  type: chrome-extension\n  extensionId: " + strings.Repeat("a", 32) + "\n  portName: generic-v1\n"
	if err := os.WriteFile(filepath.Join(root, "mcp", "device.yaml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	provider := NewRepoProvider()
	value, err := provider.Options(context.Background(), "device")
	if err != nil {
		t.Fatal(err)
	}
	if value.ClientOptions != nil && value.ClientOptions.Auth != nil {
		t.Fatal("browser config received auth store")
	}
	if _, err = os.Stat(filepath.Join(root, "runtime")); !os.IsNotExist(err) {
		t.Fatal("browser provider created credential/runtime state")
	}
}

type exactBrowserProvider struct {
	config *cfg.MCPClient
	reads  int
}

func (p *exactBrowserProvider) Options(_ context.Context, name string) (*cfg.MCPClient, error) {
	if name != "device" {
		return nil, errors.New("unexpected config")
	}
	p.reads++
	return p.config, nil
}
func (p *exactBrowserProvider) Names(context.Context) ([]string, error) {
	return nil, errors.New("unrelated enumeration must not run")
}
func TestBrowserDescriptorRevalidatesExactConfigWithoutEnumeration(t *testing.T) {
	p := &exactBrowserProvider{config: &cfg.MCPClient{ExecutionLocation: "browser", BrowserTransport: &cfg.BrowserTransport{Type: "chrome-extension", ExtensionID: strings.Repeat("a", 32), PortName: "fixture"}}}
	m, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.BrowserDescriptor(context.Background(), "device"); err == nil || p.reads != 0 {
		t.Fatal("anonymous config read")
	}
	ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "owner"})
	for i := 0; i < 3; i++ {
		d, e := m.BrowserDescriptor(ctx, "device")
		if e != nil || d.Name != "device" {
			t.Fatal(d, e)
		}
	}
	if p.reads != 3 {
		t.Fatal("config validation was cached")
	}
	p.config = nil
	if _, err = m.BrowserDescriptor(ctx, "device"); !errors.Is(err, ErrBrowserExecutionRequired) {
		t.Fatal("removed config admitted", err)
	}
}
