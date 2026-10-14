package manager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

type definitionReaderFixture struct {
	mu           sync.Mutex
	definitions  map[string]*cfg.MCPClient
	failure      error
	loaded       []string
	reserved     map[string]bool
	lists        int
	reserveCalls int
}

func (r *definitionReaderFixture) List(context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lists++
	if r.failure != nil {
		return nil, r.failure
	}
	out := []string{}
	for name := range r.definitions {
		if r.reserved[name] {
			return nil, errors.New("reserved local alias collision")
		}
		out = append(out, name)
	}
	return out, nil
}
func (r *definitionReaderFixture) Load(_ context.Context, name string) (*cfg.MCPClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loaded = append(r.loaded, name)
	if r.failure != nil {
		return nil, r.failure
	}
	if r.reserved[name] {
		return nil, errors.New("reserved local alias collision")
	}
	return r.definitions[name], nil
}
func (r *definitionReaderFixture) ReserveLocalNames(names []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reserveCalls++
	for _, name := range names {
		if r.definitions[name] != nil {
			return errors.New("reserved local alias collision")
		}
	}
	r.reserved = map[string]bool{}
	for _, name := range names {
		r.reserved[name] = true
	}
	return nil
}
func (r *definitionReaderFixture) set(name string, definition *cfg.MCPClient) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if definition == nil {
		delete(r.definitions, name)
	} else {
		r.definitions[name] = definition
	}
}

func TestRepoDefinitionReaderSharedDetachedPreparationAndNoFallback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	t.Setenv("AGENTLY_RUNTIME_ROOT", filepath.Join(root, "state"))
	t.Setenv("MCP_READER_HOST", "http://fixture.test")
	name := "mcp://workspace/original"
	original := &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "streaming", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "${MCP_READER_HOST}/mcp"}}}, Metadata: map[string]any{"exact": json.Number("9007199254740993")}}
	reader := &definitionReaderFixture{definitions: map[string]*cfg.MCPClient{name: original}}
	provider := NewRepoProvider(WithRepoDefinitionReader(reader))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	route, err := provider.BrowserOptions(ctx, name)
	require.NoError(t, err)
	require.Equal(t, "streamable", route.Transport.Type)
	require.Equal(t, "http://fixture.test/mcp", route.Transport.URL)
	require.Nil(t, route.Auth)
	options, err := provider.Options(ctx, name)
	require.NoError(t, err)
	require.NotNil(t, options.Auth.Store)
	require.Equal(t, json.Number("9007199254740993"), options.Metadata["exact"])
	options.Metadata["exact"] = "caller"
	require.Nil(t, original.Auth)
	require.Equal(t, "streaming", original.Transport.Type)
	require.Equal(t, "${MCP_READER_HOST}/mcp", original.Transport.URL)
	require.Equal(t, json.Number("9007199254740993"), original.Metadata["exact"])
	require.Equal(t, []string{name, name}, reader.loaded)
	denial := errors.New("owning reader denied")
	reader.failure = denial
	_, err = provider.Options(ctx, "mcp/slash/name")
	require.ErrorIs(t, err, denial)
	require.Equal(t, "mcp/slash/name", reader.loaded[len(reader.loaded)-1])
	require.Len(t, reader.loaded, 3, "reader denial must not try a repository/flat alias")
	_, err = provider.BrowserOptions(ctx, name)
	require.ErrorIs(t, err, denial)
}

func TestManagerDefinitionReaderBindingReservesLocalWithoutRecursiveInventory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "mcp"), 0700))
	provider := NewRepoProvider()
	mgr, err := New(provider)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	config := &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "local-owner"}
	require.NoError(t, mgr.RegisterLocal(context.Background(), "captured-local", config, func(context.Context) (mcpclient.Interface, error) { return &stubClient{}, nil }))
	reader := &definitionReaderFixture{definitions: map[string]*cfg.MCPClient{}}
	bound, err := mgr.BindDefinitionReader(reader)
	require.NoError(t, err)
	require.True(t, bound)
	replacement := &definitionReaderFixture{definitions: map[string]*cfg.MCPClient{}}
	_, err = mgr.BindDefinitionReader(replacement)
	require.Error(t, err)
	require.Zero(t, replacement.reserveCalls, "rebind must fail before mutating another reader")
	_, err = mgr.Get(context.Background(), "", "captured-local")
	require.NoError(t, err)
	require.Zero(t, reader.lists, "local bootstrap cannot enumerate its own gateway reader")
	require.Error(t, mgr.RegisterLocal(context.Background(), "late", config, func(context.Context) (mcpclient.Interface, error) { return &stubClient{}, nil }))
	reader.set("captured-local", config)
	_, err = provider.Names(context.Background())
	require.Error(t, err, "owning inventory must reject reserved alias publication")
	custom, _ := New(&concurrencyProviderStub{})
	bound, err = custom.BindDefinitionReader(reader)
	require.NoError(t, err)
	require.False(t, bound, "custom provider ownership must not silently reset")
}

func TestLegacyRepoAliasNeverRewritesCanonicalURI(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	t.Setenv("AGENTLY_RUNTIME_ROOT", filepath.Join(root, "state"))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "mcp"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "mcp", "flat_nested.yaml"), []byte("name: original\ntransport: {type: streamable, url: http://fixture.test/mcp}\n"), 0600))
	provider := NewRepoProvider()
	got, err := provider.Options(context.Background(), "flat/nested")
	require.NoError(t, err)
	require.Equal(t, "original", got.Name)
	_, err = provider.Options(context.Background(), "mcp://workspace/missing")
	require.Error(t, err)
	require.False(t, strings.Contains(got.Name, "://"))
}
