// Package oauthprovider loads workspace OAuth provider definitions from
// <workspace>/oauth/providers/<provider>.yaml. Provider files are trust
// anchors: they carry issuer/discovery metadata and secret resource
// references (SCY configURL) only — never secret material.
package oauthprovider

import (
	"github.com/viant/afs"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/repository/base"
	authcfg "github.com/viant/mcp/client/auth/config"
)

// Document is one provider definition file. It embeds the host-neutral
// viant/mcp provider model and adds Agently administrative controls.
type Document struct {
	authcfg.OAuthProvider `yaml:",inline" json:",inline"`

	// Disabled is the provider-level delegated-auth kill switch: a disabled
	// provider blocks reuse, refresh, initiation and callback persistence for
	// every MCP referencing it.
	Disabled bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`

	// Introspection configures the trusted RFC 7662 endpoint used to validate
	// opaque access tokens, including client-supplied grants. Without this or
	// an explicit server-exchange TokenResponse policy, opaque grants fail closed.
	Introspection *Introspection `yaml:"introspection,omitempty" json:"introspection,omitempty"`

	// TokenResponse opts into opaque grants obtained by this server's bound
	// authorization-code exchange. It never permits raw token imports.
	TokenResponse *TokenResponse `yaml:"tokenResponse,omitempty" json:"tokenResponse,omitempty"`
}

// TokenResponse pins the trusted token endpoint and resource for providers
// without signed access tokens or complete RFC 7662 claims (for example Asana).
// SubjectPath selects a string in the token response extras, e.g. data.gid.
type TokenResponse struct {
	TokenURL    string `yaml:"tokenURL" json:"tokenURL"`
	Resource    string `yaml:"resource" json:"resource"`
	SubjectPath string `yaml:"subjectPath" json:"subjectPath"`
}

// Introspection declares the provider's authenticated token-introspection
// endpoint. The client credentials come from the referenced client's SCY
// configURL; no secret material appears here.
type Introspection struct {
	// URL is the introspection endpoint (HTTPS in production).
	URL string `yaml:"url,omitempty" json:"url,omitempty"`
	// ClientRef selects the confidential client whose credentials authenticate
	// the introspection call; empty resolves the provider default client.
	ClientRef string `yaml:"clientRef,omitempty" json:"clientRef,omitempty"`
}

// Repository provides CRUD over oauth/providers workspace resources.
type Repository struct {
	*base.Repository[Document]
}

// New creates a filesystem-backed provider repository.
func New(fs afs.Service) *Repository {
	return &Repository{base.New[Document](fs, workspace.KindOAuthProvider)}
}

// NewWithStore creates a store-backed provider repository.
func NewWithStore(store workspace.Store) *Repository {
	return &Repository{base.NewWithStore[Document](store, workspace.KindOAuthProvider)}
}
