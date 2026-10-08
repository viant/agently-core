package resource

// WorkspaceListScope contains only verified current-account memberships used
// to narrow SQL discovery candidates before page construction.
type WorkspaceListScope struct {
	Actor  VerifiedActor
	Roles  []string
	Groups []string
}
