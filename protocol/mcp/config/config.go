package config

import (
	"fmt"
	"strings"

	mcp "github.com/viant/mcp"
	authcfg "github.com/viant/mcp/client/auth/config"

	asynccfg "github.com/viant/agently-core/protocol/async"
)

// Group is a simple list wrapper used by config with an optional URL root.
type Group[T any] struct {
	URL   string `yaml:"url,omitempty" json:"url,omitempty"`
	Items []T    `yaml:"items,omitempty" json:"items,omitempty"`
}

// MCPClient augments mcp.ClientOptions with optional discovery descriptions and metadata.
type MCPClient struct {
	*mcp.ClientOptions `yaml:",inline" json:",inline"`
	Async              []*asynccfg.Config     `yaml:"async,omitempty" json:"async,omitempty"`
	Descriptions       map[string]string      `yaml:"descriptions,omitempty" json:"descriptions,omitempty"`
	Metadata           map[string]interface{} `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	// Cacheable marks specific tools on this MCP server as eligible for
	// prompt-history supersession. Keys are tool names (exact match);
	// values indicate cacheability.
	Cacheable map[string]bool `yaml:"cacheable,omitempty" json:"cacheable,omitempty"`
	// ToolTimeoutSec overrides the default tool execution timeout when invoking
	// tools on this MCP server. When zero, a system default applies.
	ToolTimeoutSec int `yaml:"toolTimeoutSec,omitempty" json:"toolTimeoutSec,omitempty"`
	// DisableDelegatedAuth is the MCP-level delegated-auth kill switch. When
	// set on a server whose auth.mode is oauth, client creation fails with a
	// typed provider-disabled error; it never silently falls back to
	// workspace credentials.
	DisableDelegatedAuth bool `yaml:"disableDelegatedAuth,omitempty" json:"disableDelegatedAuth,omitempty"`
}

// IsDelegatedAuth reports whether this MCP definition selects the delegated
// OAuth path (auth.mode=oauth with providerRef/inlineProvider).
func (c *MCPClient) IsDelegatedAuth() bool {
	if c == nil || c.ClientOptions == nil || c.ClientOptions.Auth == nil {
		return false
	}
	return c.ClientOptions.Auth.IsDelegated()
}

// NormalizeDelegatedAuth applies backward-compatible flag handling for
// delegated configs before viant/mcp requirement compilation: a legacy
// useIdToken=true with an empty tokenType normalizes to tokenType=idToken; an
// explicit conflicting tokenType (accessToken) combined with useIdToken=true
// fails validation. Non-delegated configs are untouched — legacy useIdToken
// behaviour stays unchanged for them.
func (c *MCPClient) NormalizeDelegatedAuth() error {
	if !c.IsDelegatedAuth() {
		return nil
	}
	auth := c.ClientOptions.Auth
	if !auth.UseIdToken {
		return nil
	}
	switch authcfg.TokenType(strings.TrimSpace(auth.TokenType)) {
	case "":
		auth.TokenType = string(authcfg.TokenTypeIDToken)
	case authcfg.TokenTypeIDToken:
		// Explicit and consistent.
	default:
		return fmt.Errorf("mcp auth: useIdToken=true conflicts with tokenType %q; drop useIdToken or set tokenType=%q", auth.TokenType, authcfg.TokenTypeIDToken)
	}
	return nil
}
