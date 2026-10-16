// Package maintenance runs conversation, scheduled-run, orphan, and technical
// record maintenance through an injected Data store. Policies retain ownership
// and terminal-state checks; a distributed lease and local guard prevent
// overlapping passes.
//
// Applications supply Options and a lifecycle context to Start. Environment
// parsing belongs to the embedding application. Call New(...).RunPass for a
// controlled single pass, including dry-run validation without mutations.
package maintenance
