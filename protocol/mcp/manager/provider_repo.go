package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	shared "github.com/viant/forge/backend/clientpool"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/viant/afs"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/workspace"
	mcprepo "github.com/viant/agently-core/workspace/repository/mcp"
	"github.com/viant/mcp"
	mcpstore "github.com/viant/mcp/client/auth/store"
)

// RepoProvider loads MCP client options from the Agently workspace repo ($AGENTLY_WORKSPACE/mcp).
// DefinitionReader supplies authored definitions through the same catalog path.
type DefinitionReader = shared.DefinitionReader

type localNameReservationReader interface{ ReserveLocalNames([]string) error }

type RepoProvider struct {
	sourceMu   sync.RWMutex
	reader     DefinitionReader
	reserved   map[string]bool
	bound      bool
	repo       *mcprepo.Repository
	stateStore workspace.StateStore
}

// RepoProviderOption configures RepoProvider.
type RepoProviderOption func(*RepoProvider)

// WithRepoStateStore injects a StateStore for resolving state directories.
func WithRepoStateStore(ss workspace.StateStore) RepoProviderOption {
	return func(p *RepoProvider) { p.stateStore = ss }
}

func NewRepoProvider(opts ...RepoProviderOption) *RepoProvider {
	p := &RepoProvider{repo: mcprepo.New(afs.New())}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// WithRepoDefinitionReader selects one reader; supplied-reader errors never fall
// back to the legacy repository. Manager binding reserves local names first.
func WithRepoDefinitionReader(reader DefinitionReader) RepoProviderOption {
	return func(p *RepoProvider) { p.reader = reader; p.bound = reader != nil }
}

func (p *RepoProvider) bindDefinitionReader(reader DefinitionReader, names []string) error {
	p.sourceMu.Lock()
	defer p.sourceMu.Unlock()
	if p.bound {
		return fmt.Errorf("MCP definition reader already bound")
	}
	if reader == nil {
		return fmt.Errorf("MCP definition reader unavailable")
	}
	reserved := map[string]bool{}
	for _, name := range names {
		reserved[name] = true
	}
	if len(names) > 0 {
		reservation, ok := reader.(localNameReservationReader)
		if !ok {
			return fmt.Errorf("MCP definition reader cannot reserve local connections")
		}
		if err := reservation.ReserveLocalNames(append([]string(nil), names...)); err != nil {
			return err
		}
	}
	p.reader = reader
	p.reserved = reserved
	p.bound = true
	return nil
}
func (p *RepoProvider) definitionReaderBound() bool {
	p.sourceMu.RLock()
	defer p.sourceMu.RUnlock()
	return p.bound
}

func (p *RepoProvider) definitionGeneration(ctx context.Context) string {
	p.sourceMu.RLock()
	reader := p.reader
	p.sourceMu.RUnlock()
	if versioned, ok := reader.(interface{ DefinitionGeneration(context.Context) string }); ok {
		return versioned.DefinitionGeneration(ctx)
	}
	return ""
}
func (p *RepoProvider) reservedLocalName(name string) bool {
	p.sourceMu.RLock()
	defer p.sourceMu.RUnlock()
	return p.bound && p.reserved[name]
}

func (p *RepoProvider) loadDefinition(ctx context.Context, name string) (*mcpcfg.MCPClient, error) {
	p.sourceMu.RLock()
	reader := p.reader
	p.sourceMu.RUnlock()
	memo := namesMemoFromContext(ctx)
	var memoKey definitionMemoEntry
	if memo != nil && reader != nil {
		if versioned, ok := reader.(interface{ DefinitionContentGeneration(context.Context) string }); ok {
			memoKey = definitionMemoEntry{provider: p, name: name, scope: definitionMemoScope(ctx), generation: versioned.DefinitionContentGeneration(ctx)}
		} else if versioned, ok := reader.(interface{ DefinitionGeneration(context.Context) string }); ok {
			memoKey = definitionMemoEntry{provider: p, name: name, scope: definitionMemoScope(ctx), generation: versioned.DefinitionGeneration(ctx)}
		}
		if memoKey.provider != nil {
			if held, found := memo.getDefinition(memoKey); found {
				var detached mcpcfg.MCPClient
				decoder := json.NewDecoder(bytes.NewReader(held.raw))
				decoder.UseNumber()
				if err := decoder.Decode(&detached); err != nil {
					return nil, err
				}
				detached.ResolvedIdentity = held.identity
				return &detached, nil
			}
		}
	}
	var cfg *mcpcfg.MCPClient
	resolvedName := name
	var err error
	if reader != nil {
		cfg, err = reader.Load(ctx, name)
	} else {
		cfg, err = p.repo.Load(ctx, name)
		// Flat aliases apply to legacy repository names, never canonical references.
		if (err != nil || cfg == nil || cfg.ClientOptions == nil) && !strings.Contains(name, "://") && strings.Contains(strings.TrimSpace(name), "/") {
			alias := strings.ReplaceAll(strings.TrimSpace(name), "/", "_")
			if alias != "" && alias != name {
				cfg, err = p.repo.Load(ctx, alias)
				resolvedName = alias
			}
		}
	}
	if err != nil || cfg == nil {
		return cfg, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if memoKey.provider != nil {
		value := definitionMemoValue{raw: raw}
		if cfg.ResolvedIdentity != nil {
			copy := *cfg.ResolvedIdentity
			value.identity = &copy
		}
		memo.putDefinition(memoKey, value)
	}
	var detached mcpcfg.MCPClient
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&detached); err != nil {
		return nil, err
	}
	if reader == nil {
		detached.ResolvedIdentity = &mcpcfg.ResolvedServiceIdentity{ServiceName: resolvedName}
	} else if cfg.ResolvedIdentity != nil {
		identity := *cfg.ResolvedIdentity
		detached.ResolvedIdentity = &identity
	}
	return &detached, nil
}

func (p *RepoProvider) Options(ctx context.Context, name string) (*mcpcfg.MCPClient, error) {
	return p.preparedOptions(ctx, name, false)
}

func (p *RepoProvider) preparedOptions(ctx context.Context, name string, routingOnly bool) (*mcpcfg.MCPClient, error) {
	cfg, err := p.loadDefinition(ctx, name)
	if err == nil && cfg != nil {
		if e := cfg.ValidateExecutionLocation(); e != nil {
			return nil, e
		}
		if cfg.IsBrowserExecution() {
			canonical := strings.TrimSpace(name)
			if !strings.Contains(canonical, "://") {
				canonical = strings.ReplaceAll(canonical, "/", "_")
			}
			if _, e := cfg.BrowserDescriptor(canonical); e != nil {
				return nil, e
			}
			return cfg, nil
		}
	}
	if err != nil || cfg == nil || cfg.ClientOptions == nil {
		return cfg, err
	}
	// Normalize transport type aliases for backwards/forwards compatibility.
	// The MCP client expects "streamable"; coerce common synonyms to it.
	if cfg.ClientOptions != nil && cfg.ClientOptions.Transport.Type != "" {
		t := strings.ToLower(strings.TrimSpace(cfg.ClientOptions.Transport.Type))
		switch t {
		case "streaming", "streamablehttp":
			cfg.ClientOptions.Transport.Type = "streamable"
		}
	}
	expandEnvTemplates(cfg)
	if routingOnly {
		return cfg, nil
	}
	// Explicitly public, anonymous catalogs must remain anonymous. Attaching a
	// per-user OAuth store manufactures an Auth configuration and prevents the
	// shared manager from warming or sharing their public metadata.
	if cfg.ToolsListVisibility == mcpcfg.ToolsListVisibilityPublic && cfg.Auth == nil {
		for _, warning := range mcpcfg.ValidateResourceRoots(cfg.Metadata) {
			log.Printf("mcp config %q: %s", name, warning)
		}
		return cfg, nil
	}

	if cfg.IsDelegatedAuth() {
		// Delegated OAuth credentials live in the encrypted canonical token
		// store owned by the credential resolver; never attach a per-user
		// file token store for these servers.
		for _, warning := range mcpcfg.ValidateResourceRoots(cfg.Metadata) {
			log.Printf("mcp config %q: %s", name, warning)
		}
		return cfg, nil
	}
	storageName := name
	if cfg.ResolvedIdentity != nil && cfg.ResolvedIdentity.ServiceName != "" {
		storageName = cfg.ResolvedIdentity.ServiceName
	}
	// Derive per-user state dir for tokens/cookies
	userID := authctx.EffectiveUserID(ctx)
	if userID == "" {
		userID = "anonymous"
	}
	safe := sanitize(userID)
	var stateDir string
	if p.stateStore != nil {
		stateDir, _ = p.stateStore.StatePath(ctx, filepath.Join("mcp", storageName, safe))
	} else {
		stateDir = filepath.Join(workspace.StateRoot(), "mcp", storageName, safe)
		_ = os.MkdirAll(stateDir, 0o700)
	}

	// Attach persistent token store; preserve existing Auth config
	if cfg.ClientOptions.Auth == nil {
		cfg.ClientOptions.Auth = &mcp.ClientAuth{}
	}
	tokensPath := filepath.Join(stateDir, "tokens.json")
	cfg.ClientOptions.Auth.Store = mcpstore.NewFileStore(tokensPath)

	for _, warning := range mcpcfg.ValidateResourceRoots(cfg.Metadata) {
		log.Printf("mcp config %q: %s", name, warning)
	}

	return cfg, nil
}

// Names lists configured MCP server definitions without performing discovery.
func (p *RepoProvider) Names(ctx context.Context) ([]string, error) {
	if p == nil {
		return nil, nil
	}
	p.sourceMu.RLock()
	reader := p.reader
	p.sourceMu.RUnlock()
	if reader != nil {
		return reader.List(ctx)
	}
	if p.repo == nil {
		return nil, nil
	}
	names, err := p.repo.List(ctx)
	if err != nil && ctx.Err() == nil {
		// The legacy MCP directory is optional. Do not treat other repository or
		// supplied-reader failures as an empty external connection inventory.
		exists, existsErr := p.repo.DirExists(ctx)
		if existsErr == nil && !exists {
			return nil, nil
		}
	}
	return names, err
}

var nonWord = regexp.MustCompile(`[^A-Za-z0-9_.@-]+`)
var envTemplate = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

func sanitize(s string) string {
	if s == "" {
		return "anonymous"
	}
	return nonWord.ReplaceAllString(s, "_")
}

func expandEnvTemplates(cfg *mcpcfg.MCPClient) {
	if cfg == nil || cfg.ClientOptions == nil {
		return
	}
	cfg.Name = expandEnvString(cfg.Name)
	cfg.Version = expandEnvString(cfg.Version)
	cfg.ProtocolVersion = expandEnvString(cfg.ProtocolVersion)
	cfg.Namespace = expandEnvString(cfg.Namespace)
	cfg.Transport.Type = expandEnvString(cfg.Transport.Type)
	cfg.Transport.URL = expandEnvString(cfg.Transport.URL)
	cfg.Transport.Command = expandEnvString(cfg.Transport.Command)
	for i := range cfg.Transport.Arguments {
		cfg.Transport.Arguments[i] = expandEnvString(cfg.Transport.Arguments[i])
	}
	if cfg.Auth != nil {
		cfg.Auth.EncryptionKey = expandEnvString(cfg.Auth.EncryptionKey)
		for i := range cfg.Auth.OAuth2ConfigURL {
			cfg.Auth.OAuth2ConfigURL[i] = expandEnvString(cfg.Auth.OAuth2ConfigURL[i])
		}
	}
}

func expandEnvString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.Contains(value, "${") {
		return value
	}
	return envTemplate.ReplaceAllStringFunc(value, func(match string) string {
		parts := envTemplate.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		if current, ok := os.LookupEnv(parts[1]); ok && current != "" {
			return current
		}
		if len(parts) >= 4 {
			return parts[3]
		}
		return ""
	})
}

// BrowserOptions reads public execution routing before token/cookie state or
// environment/resource providers are attached by ordinary server Options.
func (p *RepoProvider) BrowserOptions(ctx context.Context, name string) (*mcpcfg.MCPClient, error) {
	return p.preparedOptions(ctx, name, true)
}

// These methods expose only provider lifecycle/configuration to the shared pool.
func (p *RepoProvider) BindDefinitionReader(reader DefinitionReader, names []string) error {
	return p.bindDefinitionReader(reader, names)
}
func (p *RepoProvider) DefinitionReaderBound() bool        { return p.definitionReaderBound() }
func (p *RepoProvider) ReservedLocalName(name string) bool { return p.reservedLocalName(name) }
func (p *RepoProvider) DefinitionGeneration(ctx context.Context) string {
	return p.definitionGeneration(ctx)
}
