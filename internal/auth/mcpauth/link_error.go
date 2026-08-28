// Package mcpauth defines the stable Agently-typed error surface for
// delegated MCP OAuth. It maps the viant/mcp transport-level
// OAuthLinkRequiredError into a form API layers can render without importing
// transport internals, preserving issuer/resource/metadata URL for learned
// binding persistence.
package mcpauth

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	authcfg "github.com/viant/mcp/client/auth/config"
)

// APICode is the stable machine-readable code surfaced to API clients.
const APICode = "mcp_oauth_link_required"

// LinkRequiredError is the Agently-typed terminal outcome of delegated MCP
// authentication: no usable credential exists (or can be refreshed) and
// interactive (re-)linking is needed.
type LinkRequiredError struct {
	ServerName  string
	ProviderRef string
	Issuer      string
	Resource    string
	Scopes      []string
	// MetadataURL carries the protected-resource metadata URL the requirement
	// was learned from, enabling learned-binding persistence.
	MetadataURL string
	Cause       error
}

// Error implements error.
func (e *LinkRequiredError) Error() string {
	var b strings.Builder
	b.WriteString(APICode)
	if e.ServerName != "" {
		fmt.Fprintf(&b, " server=%q", e.ServerName)
	}
	if e.ProviderRef != "" {
		fmt.Fprintf(&b, " provider=%q", e.ProviderRef)
	}
	if e.Resource != "" {
		fmt.Fprintf(&b, " resource=%q", e.Resource)
	}
	if e.Cause != nil {
		fmt.Fprintf(&b, ": %v", e.Cause)
	}
	return b.String()
}

// Unwrap exposes the underlying cause.
func (e *LinkRequiredError) Unwrap() error { return e.Cause }

// ConnectURL returns the hosted initiation endpoint for this server.
func (e *LinkRequiredError) ConnectURL() string {
	if e == nil || strings.TrimSpace(e.ServerName) == "" {
		return ""
	}
	return "/v1/api/auth/mcp/" + url.PathEscape(strings.TrimSpace(e.ServerName)) + "/initiate"
}

// APIError renders the stable JSON representation. It never contains tokens,
// secrets or authorization URLs.
func (e *LinkRequiredError) APIError() map[string]interface{} {
	result := map[string]interface{}{
		"code":   APICode,
		"server": e.ServerName,
	}
	if e.ProviderRef != "" {
		result["provider"] = e.ProviderRef
	}
	if e.Resource != "" {
		result["resource"] = e.Resource
	}
	if len(e.Scopes) > 0 {
		result["scopes"] = e.Scopes
	}
	if connect := e.ConnectURL(); connect != "" {
		result["connectURL"] = connect
	}
	return result
}

// FromError maps any error chain containing a viant/mcp
// OAuthLinkRequiredError (or an already-typed LinkRequiredError) to the
// Agently form. Returns (nil, false) for unrelated errors.
func FromError(err error) (*LinkRequiredError, bool) {
	if err == nil {
		return nil, false
	}
	var typed *LinkRequiredError
	if errors.As(err, &typed) {
		return typed, true
	}
	var upstream *authcfg.OAuthLinkRequiredError
	if !errors.As(err, &upstream) {
		return nil, false
	}
	return &LinkRequiredError{
		ServerName:  upstream.ServerName,
		ProviderRef: upstream.ProviderRef,
		Issuer:      upstream.Issuer,
		Resource:    upstream.Resource,
		Scopes:      append([]string(nil), upstream.Scopes...),
		MetadataURL: upstream.MetadataURL,
		Cause:       upstream.Cause,
	}, true
}

// WrapError converts a viant/mcp link-required error into the Agently-typed
// error while passing every other error through unchanged.
func WrapError(err error) error {
	if typed, ok := FromError(err); ok {
		return typed
	}
	return err
}

// IsLinkRequired reports whether err (or anything it wraps) requires
// interactive provider linking.
func IsLinkRequired(err error) bool {
	_, ok := FromError(err)
	return ok
}
