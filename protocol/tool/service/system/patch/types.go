package patch

// ApplyInput is the payload for Service.Apply
type ApplyInput struct {
	Patch   string `json:"patch" description:"Patch text to apply (either unified-diff or simplified patch format)"`
	Workdir string `json:"workdir" description:"Required. Base directory for patch paths; absolute patch paths are accepted only when they resolve inside workdir"`
}

// ReplaceInput is the payload for Service.Replace.
type ReplaceInput struct {
	Workdir             string `json:"workdir" description:"Required. Base directory for path; absolute path is accepted only when it resolves inside workdir"`
	Path                string `json:"path" description:"File path to edit. Relative paths are resolved against workdir; absolute paths must resolve inside workdir"`
	Old                 string `json:"old" description:"Exact text to replace. Must be non-empty and must already exist in the file"`
	New                 string `json:"new" description:"Replacement text"`
	ReplaceAll          bool   `json:"replaceAll,omitempty" description:"When true, replace all exact occurrences of old"`
	ExpectedOccurrences int    `json:"expectedOccurrences,omitempty" description:"Optional exact occurrence count required before replacing"`
}

// DiffInput is the payload for Service.Diff
type DiffInput struct {
	OldContent   string `json:"old" description:"Original content."`
	NewContent   string `json:"new" description:"Updated content."`
	Path         string `json:"path,omitempty" description:"Optional logical path used in patch headers (e.g., src/file.go)."`
	ContextLines int    `json:"contextLines,omitempty" description:"Patch context lines (default 3)."`
}

// DiffStats summarizes additions/removals in a patch string.
type DiffStats struct {
	Added   int `json:"added,omitempty"`
	Removed int `json:"removed,omitempty"`
}

// ApplyOutput summarises changes applied.
type ApplyOutput struct {
	Stats  DiffStats `json:"stats,omitempty"`
	Status string    `json:"status,omitempty"`
	Error  string    `json:"error,omitempty"`
}

// ReplaceOutput summarises an exact replacement staged in the active session.
type ReplaceOutput struct {
	Path         string    `json:"path,omitempty"`
	Replacements int       `json:"replacements,omitempty"`
	Stats        DiffStats `json:"stats,omitempty"`
	Status       string    `json:"status,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// DiffOutput mirrors DiffResult for JSON tags.
type DiffOutput struct {
	Patch string    `json:"patch"`
	Stats DiffStats `json:"stats"`
}

// EmptyInput/Output used by commit/rollback/snapshot
type EmptyInput struct{}
type EmptyOutput struct{}

// Change represents a single tracked change in the active session.
type Change struct {
	Kind    string `json:"kind"`
	OrigURL string `json:"origUrl,omitempty"`
	URL     string `json:"url,omitempty"`
	Diff    string `json:"diff,omitempty"`
}

// SnapshotOutput lists the current uncommitted changes captured by the active session.
// Each change already carries resolved file locations in OrigURL/URL, so snapshot
// does not repeat workdir at the top level.
type SnapshotOutput struct {
	Changes []Change `json:"changes,omitempty"`
	Status  string   `json:"status,omitempty"`
	Error   string   `json:"error,omitempty"`
}
