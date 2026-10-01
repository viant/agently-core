package manager

import mcpauth "github.com/viant/agently-core/internal/auth/mcpauth"

// LinkRequiredError is the stable delegated-MCP re-link outcome. The alias
// makes Agently's typed result available to embedding hosts without giving
// them access to the internal auth package or a transport-specific error.
type LinkRequiredError = mcpauth.LinkRequiredError

// LinkRequired returns the typed re-link outcome from an error chain.
func LinkRequired(err error) (*LinkRequiredError, bool) {
	return mcpauth.FromError(err)
}
