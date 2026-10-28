// Package cohort connects configured MCP publishers to Forge artifact replicas.
// Hosts supply configuration and own startup/shutdown; this package owns the
// client, cache binding, local bootstrap, notifications and reconciliation.
// Data producers retain authorization ownership. Enabling a binding is explicit.
package cohort
