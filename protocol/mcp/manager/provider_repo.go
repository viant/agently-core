package manager

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/viant/afs"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/workspace"
	mcprepo "github.com/viant/agently-core/workspace/repository/mcp"
	"github.com/viant/mcp"
	mcpstore "github.com/viant/mcp/client/auth/store"
)

// RepoProvider loads MCP client options from the Agently workspace repo ($AGENTLY_WORKSPACE/mcp).
type RepoProvider struct {
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

func (p *RepoProvider) Options(ctx context.Context, name string) (*mcpcfg.MCPClient, error) {
	cfg, err := p.repo.Load(ctx, name)
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
	// Derive per-user state dir for tokens/cookies
	userID := authctx.EffectiveUserID(ctx)
	if userID == "" {
		userID = "anonymous"
	}
	safe := sanitize(userID)
	var stateDir string
	if p.stateStore != nil {
		stateDir, _ = p.stateStore.StatePath(ctx, filepath.Join("mcp", name, safe))
	} else {
		stateDir = filepath.Join(workspace.StateRoot(), "mcp", name, safe)
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

var nonWord = regexp.MustCompile(`[^A-Za-z0-9_.@-]+`)

func sanitize(s string) string {
	if s == "" {
		return "anonymous"
	}
	return nonWord.ReplaceAllString(s, "_")
}
