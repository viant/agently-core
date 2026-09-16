package intake

// Profile is a scenario configuration unit pairing instruction messages,
// tool bundles, and one default plus optional additional output templates.
type Profile struct {
	ID               string            `yaml:"id"                       json:"id"`
	Name             string            `yaml:"name,omitempty"           json:"name,omitempty"`
	Description      string            `yaml:"description,omitempty"    json:"description,omitempty"`
	AppliesTo        []string          `yaml:"appliesTo,omitempty"      json:"appliesTo,omitempty"`
	EvidenceContract *EvidenceContract `yaml:"evidenceContract,omitempty" json:"evidenceContract,omitempty"`
	Messages         []Message         `yaml:"messages,omitempty"       json:"messages,omitempty"`
	Instructions     string            `yaml:"instructions,omitempty"   json:"instructions,omitempty"`
	MCP              *MCPSource        `yaml:"mcp,omitempty"            json:"mcp,omitempty"`
	// ToolBundles lists the tool-bundle ids that are activated for the worker
	// when this profile is applied.  Runtime enforcement, not access control.
	ToolBundles    []string `yaml:"toolBundles,omitempty"    json:"toolBundles,omitempty"`
	PreferredTools []string `yaml:"preferredTools,omitempty" json:"preferredTools,omitempty"`
	Template       string   `yaml:"template,omitempty"       json:"template,omitempty"`
	Templates      []string `yaml:"templates,omitempty"      json:"templates,omitempty"`
	Resources      []string `yaml:"resources,omitempty"      json:"resources,omitempty"`
	// Knowledge declares semantic resource matches that activate only when this
	// profile is selected for the current turn. The roots must already be
	// authorized on the selected agent. This keeps large knowledge corpora out
	// of unrelated turns while retaining the agent resource boundary.
	Knowledge []KnowledgeMatch `yaml:"knowledge,omitempty" json:"knowledge,omitempty"`
	// ParallelToolCalls optionally overrides the selected agent's default
	// parallel tool execution preference for turns using this profile.
	ParallelToolCalls *bool      `yaml:"parallelToolCalls,omitempty" json:"parallelToolCalls,omitempty"`
	Expansion         *Expansion `yaml:"expansion,omitempty"      json:"expansion,omitempty"`
	Execution         *Execution `yaml:"execution,omitempty" json:"execution,omitempty"`
}

// KnowledgeMatch configures one profile-scoped semantic resource lookup.
type KnowledgeMatch struct {
	RootIDs []string `yaml:"rootIds" json:"rootIds"`
	Path    string   `yaml:"path,omitempty" json:"path,omitempty"`
	// QueryMode may be "single" (default) or "multi". Multi mode decomposes
	// explicit comma/and topic lists and interleaves their ranked matches.
	QueryMode  string `yaml:"queryMode,omitempty" json:"queryMode,omitempty"`
	MaxQueries int    `yaml:"maxQueries,omitempty" json:"maxQueries,omitempty"`
	// QueryCatalog optionally expands shorthand queries with titles from a
	// corpus-owned JSON manifest. The original query is always retained.
	QueryCatalog *QueryCatalog `yaml:"queryCatalog,omitempty" json:"queryCatalog,omitempty"`
	// MaxFragments controls semantic candidate depth before URI deduplication.
	MaxFragments int `yaml:"maxFragments,omitempty" json:"maxFragments,omitempty"`
	// MaxDocuments controls the final number of distinct documents injected.
	MaxDocuments int      `yaml:"maxDocuments,omitempty" json:"maxDocuments,omitempty"`
	MinScore     *float64 `yaml:"minScore,omitempty" json:"minScore,omitempty"`
	LimitBytes   int      `yaml:"limitBytes,omitempty" json:"limitBytes,omitempty"`
	Exclude      []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	// DocumentMode controls fragment expansion: fragment (default), full, or
	// explicit (full only when the query explicitly requests the whole article).
	DocumentMode string `yaml:"documentMode,omitempty" json:"documentMode,omitempty"`
	// NeighborFragmentsBefore/After include adjacent same-document fragments
	// when those fragments are present in the semantic candidate pool.
	NeighborFragmentsBefore int `yaml:"neighborFragmentsBefore,omitempty" json:"neighborFragmentsBefore,omitempty"`
	NeighborFragmentsAfter  int `yaml:"neighborFragmentsAfter,omitempty" json:"neighborFragmentsAfter,omitempty"`
	// MaxDocumentBytes bounds each full document. Zero means no additional cap.
	MaxDocumentBytes int `yaml:"maxDocumentBytes,omitempty" json:"maxDocumentBytes,omitempty"`
	// MaxTotalBytes bounds the combined injected content for this match entry.
	// It is applied after full-document loading and includes canonical citations.
	MaxTotalBytes int `yaml:"maxTotalBytes,omitempty" json:"maxTotalBytes,omitempty"`
	// CanonicalSourceOnly removes incidental HTTP links from document content
	// and retains only the canonical sourceUrl supplied by the document.
	CanonicalSourceOnly bool `yaml:"canonicalSourceOnly,omitempty" json:"canonicalSourceOnly,omitempty"`
	// Required makes retrieval infrastructure failures fail the turn. An empty
	// match after MinScore filtering is still a valid result.
	Required bool `yaml:"required,omitempty" json:"required,omitempty"`
}

// QueryCatalog describes a JSON table of contents. The current manifest
// contract is {"articles":[{"title":"..."}]}.
type QueryCatalog struct {
	Path       string `yaml:"path" json:"path"`
	MaxMatches int    `yaml:"maxMatches,omitempty" json:"maxMatches,omitempty"`
}

// Execution applies turn-scoped runtime restrictions after this profile is
// selected. Restrictions only narrow the agent's normal capabilities.
type Execution struct {
	DisablePlanner    bool `yaml:"disablePlanner,omitempty" json:"disablePlanner,omitempty"`
	DisableDelegation bool `yaml:"disableDelegation,omitempty" json:"disableDelegation,omitempty"`
}

// Message is a single role+content instruction, aligned with MCP PromptMessage.
type Message struct {
	Role string `yaml:"role"           json:"role"`
	Text string `yaml:"text,omitempty" json:"text,omitempty"`
	URI  string `yaml:"uri,omitempty"  json:"uri,omitempty"`
}

// MCPSource fetches instructions from an MCP server prompt.
type MCPSource struct {
	Server string            `yaml:"server"         json:"server"`
	Prompt string            `yaml:"prompt"         json:"prompt"`
	Args   map[string]string `yaml:"args,omitempty" json:"args,omitempty"`
}

// Expansion configures optional sidecar LLM synthesis at delegation time.
type Expansion struct {
	Mode      string `yaml:"mode"                json:"mode"`
	Model     string `yaml:"model,omitempty"     json:"model,omitempty"`
	MaxTokens int    `yaml:"maxTokens,omitempty" json:"maxTokens,omitempty"`
}

type EvidenceContract struct {
	Required   []string `yaml:"required,omitempty" json:"required,omitempty"`
	Optional   []string `yaml:"optional,omitempty" json:"optional,omitempty"`
	Forbidden  []string `yaml:"forbidden,omitempty" json:"forbidden,omitempty"`
	Completion []string `yaml:"completion,omitempty" json:"completion,omitempty"`
}

// EffectiveMessages returns the messages to render.
// Messages takes priority over Instructions. Returns nil for MCP-only profiles.
func (p *Profile) EffectiveMessages() []Message {
	if len(p.Messages) > 0 {
		return p.Messages
	}
	if p.Instructions != "" {
		return []Message{{Role: "system", Text: p.Instructions}}
	}
	return nil
}
