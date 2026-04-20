package config

import (
	"github.com/viant/agently-core/protocol/binding"
	"gopkg.in/yaml.v3"
)

type Defaults struct {
	Model    string
	Embedder string
	Agent    string
	Skills   SkillsDefaults `yaml:"skills,omitempty" json:"skills,omitempty"`
	// RuntimeRoot allows separating runtime state (db, snapshots, indexes) from the workspace.
	// Supports ${workspaceRoot}. When empty, defaults to ${workspaceRoot}.
	RuntimeRoot string `yaml:"runtimeRoot,omitempty" json:"runtimeRoot,omitempty"`
	// StatePath overrides the runtime state root (used by cookies/tokens).
	// Supports ${workspaceRoot} and ${runtimeRoot}. When empty, defaults to ${runtimeRoot}/state.
	StatePath string `yaml:"statePath,omitempty" json:"statePath,omitempty"`
	// DBPath overrides the SQLite database file path when AGENTLY_DB_* is not set.
	// Supports ${workspaceRoot} and ${runtimeRoot}. When empty, defaults to ${runtimeRoot}/db/agently.db.
	DBPath string `yaml:"dbPath,omitempty" json:"dbPath,omitempty"`

	// ---- Agent routing defaults (optional) -------------------------
	// When Agent == "auto", the runtime may use these settings to pick a concrete
	// agent for the turn using an LLM-based classifier.
	AgentAutoSelection AgentAutoSelectionDefaults `yaml:"agentAutoSelection,omitempty" json:"agentAutoSelection,omitempty"`

	// ---- Tool routing defaults (optional) --------------------------
	// When enabled, the runtime may select tool bundles for the turn based on the
	// user request when the caller did not explicitly provide tools/bundles.
	ToolAutoSelection ToolAutoSelectionDefaults `yaml:"toolAutoSelection,omitempty" json:"toolAutoSelection,omitempty"`
	// ToolApproval defines global runtime tool execution approval behavior.
	// Applies when callers do not supply per-request tool allow-list overrides.
	ToolApproval ToolApprovalDefaults `yaml:"toolApproval,omitempty" json:"toolApproval,omitempty"`

	// ---- Conversation summary defaults (optional) -------------------
	// When empty the runtime falls back to hard-coded defaults.
	SummaryModel  string `yaml:"summaryModel" json:"summaryModel"`
	SummaryPrompt string `yaml:"summaryPrompt" json:"summaryPrompt"`
	SummaryLastN  int    `yaml:"summaryLastN" json:"summaryLastN"`
	// CapabilityPrompt overrides the system prompt used for capability discovery responses.
	CapabilityPrompt string `yaml:"capabilityPrompt" json:"capabilityPrompt"`

	// ---- Tool-call result controls (grouped) ---------------------
	PreviewSettings PreviewSettings `yaml:"previewSettings" json:"previewSettings"`

	// ---- Prompt-history projection -------------------------------
	Projection Projection `yaml:"projection,omitempty" json:"projection,omitempty"`

	ToolCallMaxResults int `yaml:"toolCallMaxResults" json:"toolCallMaxResults"`

	// ---- Execution timeouts -------------------------------------
	// ToolCallTimeoutSec sets the default per-tool execution timeout in seconds.
	// When zero or missing, runtime falls back to a built-in default.
	ToolCallTimeoutSec int `yaml:"toolCallTimeoutSec,omitempty" json:"toolCallTimeoutSec,omitempty"`
	// ElicitationTimeoutSec caps how long the agent waits for an elicitation
	// (assistant- or tool-originated) before auto-declining. When zero, no
	// special timeout is applied (waits until the turn/request is canceled).
	ElicitationTimeoutSec int `yaml:"elicitationTimeoutSec,omitempty" json:"elicitationTimeoutSec,omitempty"`

	// ---- Match defaults (optional) -------------------------------
	Match MatchDefaults `yaml:"match" json:"match"`

	// ---- Resources defaults (optional) ---------------------------
	Resources ResourcesDefaults `yaml:"resources,omitempty" json:"resources,omitempty"`
}

// UnmarshalYAML supports both the current and legacy router keys:
// - agentAutoSelection (preferred), agentRouter (legacy)
// - toolAutoSelection (preferred), toolRouter (legacy)
func (d *Defaults) UnmarshalYAML(value *yaml.Node) error {
	hasKey := func(want string) bool {
		if value == nil || value.Kind != yaml.MappingNode {
			return false
		}
		for i := 0; i+1 < len(value.Content); i += 2 {
			if value.Content[i].Value == want {
				return true
			}
		}
		return false
	}

	type raw struct {
		Model       string         `yaml:"model"`
		Embedder    string         `yaml:"embedder"`
		Agent       string         `yaml:"agent"`
		Skills      SkillsDefaults `yaml:"skills,omitempty"`
		RuntimeRoot string         `yaml:"runtimeRoot,omitempty"`
		StatePath   string         `yaml:"statePath,omitempty"`
		DBPath      string         `yaml:"dbPath,omitempty"`

		AgentAutoSelection AgentAutoSelectionDefaults `yaml:"agentAutoSelection,omitempty"`
		ToolAutoSelection  ToolAutoSelectionDefaults  `yaml:"toolAutoSelection,omitempty"`
		ToolApproval       ToolApprovalDefaults       `yaml:"toolApproval,omitempty"`

		// Legacy keys (deprecated)
		AgentRouter AgentAutoSelectionDefaults `yaml:"agentRouter,omitempty"`
		ToolRouter  ToolAutoSelectionDefaults  `yaml:"toolRouter,omitempty"`

		SummaryModel     string `yaml:"summaryModel,omitempty"`
		SummaryPrompt    string `yaml:"summaryPrompt,omitempty"`
		SummaryLastN     int    `yaml:"summaryLastN,omitempty"`
		CapabilityPrompt string `yaml:"capabilityPrompt,omitempty"`

		PreviewSettings PreviewSettings `yaml:"previewSettings,omitempty"`
		Projection      Projection      `yaml:"projection,omitempty"`
		Compaction      Projection      `yaml:"compaction,omitempty"` // legacy alias

		ToolCallMaxResults    int `yaml:"toolCallMaxResults,omitempty"`
		ToolCallTimeoutSec    int `yaml:"toolCallTimeoutSec,omitempty"`
		ElicitationTimeoutSec int `yaml:"elicitationTimeoutSec,omitempty"`

		Match     MatchDefaults     `yaml:"match,omitempty"`
		Resources ResourcesDefaults `yaml:"resources,omitempty"`
	}

	var tmp raw
	if err := value.Decode(&tmp); err != nil {
		return err
	}

	*d = Defaults{
		Model:       tmp.Model,
		Embedder:    tmp.Embedder,
		Agent:       tmp.Agent,
		Skills:      tmp.Skills,
		RuntimeRoot: tmp.RuntimeRoot,
		StatePath:   tmp.StatePath,
		DBPath:      tmp.DBPath,

		SummaryModel:     tmp.SummaryModel,
		SummaryPrompt:    tmp.SummaryPrompt,
		SummaryLastN:     tmp.SummaryLastN,
		CapabilityPrompt: tmp.CapabilityPrompt,

		PreviewSettings: tmp.PreviewSettings,
		Projection:      tmp.Projection,

		ToolCallMaxResults:    tmp.ToolCallMaxResults,
		ToolCallTimeoutSec:    tmp.ToolCallTimeoutSec,
		ElicitationTimeoutSec: tmp.ElicitationTimeoutSec,
		ToolApproval:          tmp.ToolApproval,

		Match:     tmp.Match,
		Resources: tmp.Resources,
	}

	if hasKey("agentAutoSelection") {
		d.AgentAutoSelection = tmp.AgentAutoSelection
	} else if hasKey("agentRouter") {
		d.AgentAutoSelection = tmp.AgentRouter
	}

	if hasKey("toolAutoSelection") {
		d.ToolAutoSelection = tmp.ToolAutoSelection
	} else if hasKey("toolRouter") {
		d.ToolAutoSelection = tmp.ToolRouter
	}
	if !hasKey("projection") && hasKey("compaction") {
		d.Projection = tmp.Compaction
	}

	return nil
}

type SkillsDefaults struct {
	Roots []string `yaml:"roots,omitempty" json:"roots,omitempty"`
	Model string   `yaml:"model,omitempty" json:"model,omitempty"`
}

// ToolApprovalDefaults defines global tool approval behavior.
type ToolApprovalDefaults struct {
	// Mode: auto | ask | deny | best_path
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
	// AllowList optionally constrains executable tools to this set.
	AllowList []string `yaml:"allowList,omitempty" json:"allowList,omitempty"`
	// BlockList blocks specific tool names regardless of mode.
	BlockList []string `yaml:"blockList,omitempty" json:"blockList,omitempty"`
}

// PreviewSettings groups tool-call result presentation and processing settings.
type PreviewSettings struct {
	Limit int `yaml:"limit" json:"limit"`

	AgedLimit int `yaml:"agedLimit" json:"agedLimit"`

	// ToolResultLimit optionally caps preview size specifically for tool-result
	// messages. When zero or negative, the general Limit/AgedLimit rules apply.
	ToolResultLimit int `yaml:"toolResultLimit,omitempty" json:"toolResultLimit,omitempty"`

	// How far back until we switch the UI to an aged preview.
	AgedAfterSteps int `yaml:"agedAfterSteps" json:"agedAfterSteps"`

	SummarizeChunk int    `yaml:"summarizeChunk" json:"summarizeChunk"`
	MatchChunk     int    `yaml:"matchChunk" json:"matchChunk"`
	SummaryModel   string `yaml:"summaryModel" json:"summaryModel"`
	EmbeddingModel string `yaml:"embeddingModel" json:"embeddingModel"`
	// Optional system guide document (path or URL) injected when overflow occurs.
	SystemGuidePath string `yaml:"systemGuidePath" json:"systemGuidePath"`
	// SummaryThresholdBytes controls when message:summarize is
	// exposed for overflowed messages. When zero or negative, any
	// overflowed message may use summarize.
	SummaryThresholdBytes int `yaml:"summaryThresholdBytes,omitempty" json:"summaryThresholdBytes,omitempty"`
}

// Projection groups prompt-history projection settings.
type Projection struct {
	Relevance            *RelevanceProjection  `yaml:"relevance,omitempty" json:"relevance,omitempty"`
	ToolCallSupersession *ToolCallSupersession `yaml:"toolCallSupersession,omitempty" json:"toolCallSupersession,omitempty"`
}

// RelevanceProjection controls optional selector-based hiding of irrelevant
// prior turns before prompt-history construction.
type RelevanceProjection struct {
	Enabled              *bool           `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	ProtectedRecentTurns *int            `yaml:"protectedRecentTurns,omitempty" json:"protectedRecentTurns,omitempty"`
	TokenThreshold       *int            `yaml:"tokenThreshold,omitempty" json:"tokenThreshold,omitempty"`
	ChunkSize            *int            `yaml:"chunkSize,omitempty" json:"chunkSize,omitempty"`
	MaxConcurrency       *int            `yaml:"maxConcurrency,omitempty" json:"maxConcurrency,omitempty"`
	Model                *string         `yaml:"model,omitempty" json:"model,omitempty"`
	Prompt               *binding.Prompt `yaml:"prompt,omitempty" json:"prompt,omitempty"`
}

// IsEnabled reports whether relevance projection is enabled. Default: true.
func (r *RelevanceProjection) IsEnabled() bool {
	if r == nil || r.Enabled == nil {
		return true
	}
	return *r.Enabled
}

// ProtectedTurns returns the protected recent-tail size. Default: 1.
func (r *RelevanceProjection) ProtectedTurns() int {
	if r == nil || r.ProtectedRecentTurns == nil || *r.ProtectedRecentTurns <= 0 {
		return 1
	}
	return *r.ProtectedRecentTurns
}

// Threshold returns the approximate token threshold that must be exceeded
// before selector-based relevance projection runs. Default: 20000.
func (r *RelevanceProjection) Threshold() int {
	if r == nil || r.TokenThreshold == nil || *r.TokenThreshold < 0 {
		return 20000
	}
	return *r.TokenThreshold
}

// Chunk returns the candidate chunk size for concurrent selector runs.
// Default: 0 (no chunking).
func (r *RelevanceProjection) Chunk() int {
	if r == nil || r.ChunkSize == nil || *r.ChunkSize <= 0 {
		return 0
	}
	return *r.ChunkSize
}

// Concurrency returns the maximum concurrent selector calls for chunked
// relevance projection. Default: 1.
func (r *RelevanceProjection) Concurrency() int {
	if r == nil || r.MaxConcurrency == nil || *r.MaxConcurrency <= 0 {
		return 1
	}
	return *r.MaxConcurrency
}

// ToolCallSupersession controls how repeated cacheable tool outputs are
// superseded during prompt-history construction.
type ToolCallSupersession struct {
	// Enabled controls whether supersession is applied. Default: true.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Limit controls how many matching results per supersession key are
	// retained in each scope.
	Limit *SupersessionLimit `yaml:"limit,omitempty" json:"limit,omitempty"`
}

// SupersessionLimit specifies per-scope retention caps.
type SupersessionLimit struct {
	// History is the max matching results per key across all prior turns
	// T(LastCheckpoint)..T(N-1). Default: 1 (only the newest survives).
	History *int `yaml:"history,omitempty" json:"history,omitempty"`
	// Turn is the max matching results per key within the current turn TN.
	// Default: 2 (keep last 2, suppress K-2 earliest).
	Turn *int `yaml:"turn,omitempty" json:"turn,omitempty"`
}

// IsSupersessionEnabled returns whether tool-call supersession is active.
func (p *Projection) IsSupersessionEnabled() bool {
	if p == nil || p.ToolCallSupersession == nil {
		return true // default enabled
	}
	if p.ToolCallSupersession.Enabled != nil {
		return *p.ToolCallSupersession.Enabled
	}
	return true
}

// SupersessionHistoryLimit returns the per-key limit for prior turns.
func (p *Projection) SupersessionHistoryLimit() int {
	if p == nil || p.ToolCallSupersession == nil || p.ToolCallSupersession.Limit == nil || p.ToolCallSupersession.Limit.History == nil {
		return 1
	}
	return *p.ToolCallSupersession.Limit.History
}

// SupersessionTurnLimit returns the per-key limit for the current turn.
func (p *Projection) SupersessionTurnLimit() int {
	if p == nil || p.ToolCallSupersession == nil || p.ToolCallSupersession.Limit == nil || p.ToolCallSupersession.Limit.Turn == nil {
		return 2
	}
	return *p.ToolCallSupersession.Limit.Turn
}

// MatchDefaults groups retrieval/matching defaults
type MatchDefaults struct {
	// MaxFiles is the default per-location cap used by auto/full decision
	// when a knowledge/MCP entry does not specify MaxFiles. When zero,
	// the runtime falls back to hard-coded default (5).
	MaxFiles int `yaml:"maxFiles" json:"maxFiles"`
}

// ResourceRoot defines a non-MCP resource root with optional upstream binding.
type ResourceRoot struct {
	ID          string `yaml:"id,omitempty" json:"id,omitempty"`
	URI         string `yaml:"uri,omitempty" json:"uri,omitempty"`
	UpstreamRef string `yaml:"upstreamRef,omitempty" json:"upstreamRef,omitempty"`
	SyncEnabled *bool  `yaml:"syncEnabled,omitempty" json:"syncEnabled,omitempty"`
	MinInterval int    `yaml:"minIntervalSeconds,omitempty" json:"minIntervalSeconds,omitempty"`
	Batch       int    `yaml:"batch,omitempty" json:"batch,omitempty"`
	Shadow      string `yaml:"shadow,omitempty" json:"shadow,omitempty"`
	Force       *bool  `yaml:"force,omitempty" json:"force,omitempty"`
}

// ResourceUpstream defines an upstream database used for local resource sync.
type ResourceUpstream struct {
	Name               string `yaml:"name,omitempty" json:"name,omitempty"`
	Driver             string `yaml:"driver,omitempty" json:"driver,omitempty"`
	DSN                string `yaml:"dsn,omitempty" json:"dsn,omitempty"`
	Shadow             string `yaml:"shadow,omitempty" json:"shadow,omitempty"`
	Batch              int    `yaml:"batch,omitempty" json:"batch,omitempty"`
	Force              bool   `yaml:"force,omitempty" json:"force,omitempty"`
	Enabled            *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	MinIntervalSeconds int    `yaml:"minIntervalSeconds,omitempty" json:"minIntervalSeconds,omitempty"`
}

// ResourceUpstreamStore defines a default upstream used when no upstreamRef is set.
type ResourceUpstreamStore struct {
	Driver             string `yaml:"driver,omitempty" json:"driver,omitempty"`
	DSN                string `yaml:"dsn,omitempty" json:"dsn,omitempty"`
	Shadow             string `yaml:"shadow,omitempty" json:"shadow,omitempty"`
	Batch              int    `yaml:"batch,omitempty" json:"batch,omitempty"`
	Force              bool   `yaml:"force,omitempty" json:"force,omitempty"`
	MinIntervalSeconds int    `yaml:"minIntervalSeconds,omitempty" json:"minIntervalSeconds,omitempty"`
}

// ResourcesDefaults defines default resource roots and presentation hints.
type ResourcesDefaults struct {
	// Locations are root URIs or paths (relative to workspace) such as
	// "documents/", "file:///abs/path", or "mcp:server:/prefix".
	Locations []string `yaml:"locations,omitempty" json:"locations,omitempty"`
	// Roots define resource roots with optional upstream bindings for local/workspace locations.
	Roots []ResourceRoot `yaml:"roots,omitempty" json:"roots,omitempty"`
	// Upstreams define upstream databases for local/workspace resource sync.
	Upstreams []ResourceUpstream `yaml:"upstreams,omitempty" json:"upstreams,omitempty"`
	// UpstreamStore defines the default upstream used when roots omit upstreamRef.
	UpstreamStore *ResourceUpstreamStore `yaml:"upstreamStore,omitempty" json:"upstreamStore,omitempty"`
	// IndexPath controls where Embedius stores local indexes. Supports
	// ${workspaceRoot} and ${user} macros. If empty, defaults to
	// ${workspaceRoot}/index/${user}.
	IndexPath string `yaml:"indexPath,omitempty" json:"indexPath,omitempty"`
	// SnapshotPath controls where MCP snapshot caches are stored. Supports
	// ${workspaceRoot} and ${user} macros. If empty, defaults to
	// ${workspaceRoot}/snapshots.
	SnapshotPath string `yaml:"snapshotPath,omitempty" json:"snapshotPath,omitempty"`
	// TrimPath optionally trims this prefix from presented URIs.
	TrimPath string `yaml:"trimPath,omitempty" json:"trimPath,omitempty"`
	// SummaryFiles lookup order for root descriptions.
	SummaryFiles []string `yaml:"summaryFiles,omitempty" json:"summaryFiles,omitempty"`
	// DescribeMCP enables MCP description fetches for resources.roots when
	// no description is provided by config/metadata.
	DescribeMCP bool `yaml:"describeMCP,omitempty" json:"describeMCP,omitempty"`
	// UpstreamSyncConcurrency controls parallel root syncs for MCP upstreams.
	UpstreamSyncConcurrency int `yaml:"upstreamSyncConcurrency,omitempty" json:"upstreamSyncConcurrency,omitempty"`
	// MatchConcurrency controls parallel match execution across roots.
	MatchConcurrency int `yaml:"matchConcurrency,omitempty" json:"matchConcurrency,omitempty"`
	// IndexAsync enables background indexing for resource matches.
	IndexAsync *bool `yaml:"indexAsync,omitempty" json:"indexAsync,omitempty"`
}

// AgentAutoSelectionDefaults controls the LLM-based agent classifier used for auto routing.
type AgentAutoSelectionDefaults struct {
	// Model is the model used for routing decisions. When empty, runtime falls back
	// to the conversation default model or Defaults.Model.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
	// Prompt optionally overrides the default system prompt used by the router.
	Prompt string `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	// OutputKey controls the JSON field name the classifier should output.
	// Examples: "agentId" (default), "agent_id".
	OutputKey string `yaml:"outputKey,omitempty" json:"outputKey,omitempty"`
	// TimeoutSec caps how long agent auto-selection classification may run.
	// When zero, runtime applies a conservative default.
	TimeoutSec int `yaml:"timeoutSec,omitempty" json:"timeoutSec,omitempty"`
}

// ToolAutoSelectionDefaults controls the optional tool bundle selector.
type ToolAutoSelectionDefaults struct {
	// Enabled turns on auto tool selection when the caller did not specify tools.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Model is the model used for routing decisions. When empty, runtime falls back
	// to the conversation default model or Defaults.Model.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
	// Prompt optionally overrides the default system prompt used by the router.
	Prompt string `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	// OutputKey controls the JSON field name the classifier should output.
	// Example: "toolBundles" (default).
	OutputKey string `yaml:"outputKey,omitempty" json:"outputKey,omitempty"`
	// MaxBundles caps the number of bundles the router may select.
	// When zero, a small default is applied.
	MaxBundles int `yaml:"maxBundles,omitempty" json:"maxBundles,omitempty"`
	// TimeoutSec caps how long tool auto-selection classification may run.
	// When zero, runtime applies a conservative default.
	TimeoutSec int `yaml:"timeoutSec,omitempty" json:"timeoutSec,omitempty"`
}
