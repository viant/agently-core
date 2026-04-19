package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	execconfig "github.com/viant/agently-core/app/executor/config"
	mcpexpose "github.com/viant/agently-core/protocol/mcp/expose"
	"github.com/viant/agently-core/workspace"
	"gopkg.in/yaml.v3"
)

// Root represents the root workspace config.yaml in a reusable decoded form.
// Package-specific consumers can decode sections they own from the stored YAML nodes.
type Root struct {
	DefaultNode yaml.Node               `yaml:"default"`
	AuthNode    yaml.Node               `yaml:"auth"`
	MCPServer   *mcpexpose.ServerConfig `yaml:"mcpServer"`
	Raw         map[string]interface{}  `yaml:",inline"`
}

// Load reads the workspace config.yaml. Missing config returns (nil, nil).
func Load(root string) (*Root, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(root, "config.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read workspace config: %w", err)
	}
	cfg := &Root{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse workspace config: %w", err)
	}
	return cfg, nil
}

// DecodeAuth decodes the auth section into the supplied destination.
func (r *Root) DecodeAuth(out interface{}) error {
	if r == nil || out == nil || isZeroNode(&r.AuthNode) {
		return nil
	}
	return r.AuthNode.Decode(out)
}

// DefaultsWithFallback merges the workspace default section over the supplied fallback.
func (r *Root) DefaultsWithFallback(fallback *execconfig.Defaults) *execconfig.Defaults {
	base := &execconfig.Defaults{
		Model:    "openai_gpt-5.2",
		Embedder: "openai_text",
		Agent:    "chatter",
	}
	if fallback != nil {
		*base = *fallback
	}
	if r == nil || isZeroNode(&r.DefaultNode) {
		return base
	}
	var cfg execconfig.Defaults
	if err := r.DefaultNode.Decode(&cfg); err != nil {
		return base
	}
	mergeDefaults(base, &cfg)
	return base
}

// InternalServiceList returns configured internal MCP service names when present.
func (r *Root) InternalServiceList() ([]string, bool) {
	if r == nil || r.Raw == nil {
		return nil, false
	}
	out := valuesToServices(r.Raw["internalMCPServices"])
	if hasKey(r.Raw, "internalMCPServices") {
		return out, true
	}
	internal := mapLookup(r.Raw, "internalMCP")
	if len(internal) == 0 {
		internal = mapLookup(r.Raw, "internal_mcp")
	}
	if internal == nil {
		return nil, false
	}
	if _, ok := internal["services"]; !ok {
		return nil, false
	}
	return valuesToServices(internal["services"]), true
}

// ApplyPathDefaults applies runtime/state/db path defaults from workspace config
// when explicit environment overrides are absent.
func ApplyPathDefaults(defaults *execconfig.Defaults) {
	if defaults == nil {
		return
	}
	if strings.TrimSpace(os.Getenv("AGENTLY_RUNTIME_ROOT")) == "" && strings.TrimSpace(defaults.RuntimeRoot) != "" {
		_ = os.Setenv("AGENTLY_RUNTIME_ROOT", defaults.RuntimeRoot)
		workspace.SetRuntimeRoot(defaults.RuntimeRoot)
	}
	if strings.TrimSpace(os.Getenv("AGENTLY_STATE_PATH")) == "" && strings.TrimSpace(defaults.StatePath) != "" {
		_ = os.Setenv("AGENTLY_STATE_PATH", defaults.StatePath)
		workspace.SetStateRoot(defaults.StatePath)
	}
	if strings.TrimSpace(os.Getenv("AGENTLY_DB_PATH")) == "" {
		dbPath := strings.TrimSpace(defaults.DBPath)
		if dbPath == "" {
			dbPath = filepath.Join(workspace.RuntimeRoot(), "db", "agently.db")
		}
		_ = os.Setenv("AGENTLY_DB_PATH", dbPath)
	}
}

func mergeDefaults(dst, src *execconfig.Defaults) {
	if dst == nil || src == nil {
		return
	}
	if strings.TrimSpace(src.Agent) != "" {
		dst.Agent = strings.TrimSpace(src.Agent)
	}
	if strings.TrimSpace(src.Model) != "" {
		dst.Model = strings.TrimSpace(src.Model)
	}
	if strings.TrimSpace(src.Embedder) != "" {
		dst.Embedder = strings.TrimSpace(src.Embedder)
	}
	if strings.TrimSpace(src.RuntimeRoot) != "" {
		dst.RuntimeRoot = strings.TrimSpace(src.RuntimeRoot)
	}
	if strings.TrimSpace(src.StatePath) != "" {
		dst.StatePath = strings.TrimSpace(src.StatePath)
	}
	if strings.TrimSpace(src.DBPath) != "" {
		dst.DBPath = strings.TrimSpace(src.DBPath)
	}
	if strings.TrimSpace(src.SummaryModel) != "" {
		dst.SummaryModel = strings.TrimSpace(src.SummaryModel)
	}
	if strings.TrimSpace(src.SummaryPrompt) != "" {
		dst.SummaryPrompt = strings.TrimSpace(src.SummaryPrompt)
	}
	if src.SummaryLastN > 0 {
		dst.SummaryLastN = src.SummaryLastN
	}
	if strings.TrimSpace(src.AgentAutoSelection.Model) != "" {
		dst.AgentAutoSelection.Model = strings.TrimSpace(src.AgentAutoSelection.Model)
	}
	if strings.TrimSpace(src.AgentAutoSelection.Prompt) != "" {
		dst.AgentAutoSelection.Prompt = strings.TrimSpace(src.AgentAutoSelection.Prompt)
	}
	if strings.TrimSpace(src.AgentAutoSelection.OutputKey) != "" {
		dst.AgentAutoSelection.OutputKey = strings.TrimSpace(src.AgentAutoSelection.OutputKey)
	}
	if src.AgentAutoSelection.TimeoutSec > 0 {
		dst.AgentAutoSelection.TimeoutSec = src.AgentAutoSelection.TimeoutSec
	}
	if src.ToolAutoSelection.Enabled {
		dst.ToolAutoSelection.Enabled = true
	}
	if strings.TrimSpace(src.ToolAutoSelection.Model) != "" {
		dst.ToolAutoSelection.Model = strings.TrimSpace(src.ToolAutoSelection.Model)
	}
	if strings.TrimSpace(src.ToolAutoSelection.Prompt) != "" {
		dst.ToolAutoSelection.Prompt = strings.TrimSpace(src.ToolAutoSelection.Prompt)
	}
	if strings.TrimSpace(src.ToolAutoSelection.OutputKey) != "" {
		dst.ToolAutoSelection.OutputKey = strings.TrimSpace(src.ToolAutoSelection.OutputKey)
	}
	if src.ToolAutoSelection.MaxBundles > 0 {
		dst.ToolAutoSelection.MaxBundles = src.ToolAutoSelection.MaxBundles
	}
	if src.ToolAutoSelection.TimeoutSec > 0 {
		dst.ToolAutoSelection.TimeoutSec = src.ToolAutoSelection.TimeoutSec
	}
	if strings.TrimSpace(src.CapabilityPrompt) != "" {
		dst.CapabilityPrompt = strings.TrimSpace(src.CapabilityPrompt)
	}
	if src.PreviewSettings.Limit > 0 {
		dst.PreviewSettings.Limit = src.PreviewSettings.Limit
	}
	if src.PreviewSettings.AgedLimit > 0 {
		dst.PreviewSettings.AgedLimit = src.PreviewSettings.AgedLimit
	}
	if src.PreviewSettings.AgedAfterSteps > 0 {
		dst.PreviewSettings.AgedAfterSteps = src.PreviewSettings.AgedAfterSteps
	}
	if src.PreviewSettings.SummarizeChunk > 0 {
		dst.PreviewSettings.SummarizeChunk = src.PreviewSettings.SummarizeChunk
	}
	if src.PreviewSettings.MatchChunk > 0 {
		dst.PreviewSettings.MatchChunk = src.PreviewSettings.MatchChunk
	}
	if strings.TrimSpace(src.PreviewSettings.SummaryModel) != "" {
		dst.PreviewSettings.SummaryModel = strings.TrimSpace(src.PreviewSettings.SummaryModel)
	}
	if strings.TrimSpace(src.PreviewSettings.EmbeddingModel) != "" {
		dst.PreviewSettings.EmbeddingModel = strings.TrimSpace(src.PreviewSettings.EmbeddingModel)
	}
	if strings.TrimSpace(src.PreviewSettings.SystemGuidePath) != "" {
		dst.PreviewSettings.SystemGuidePath = strings.TrimSpace(src.PreviewSettings.SystemGuidePath)
	}
	if src.PreviewSettings.SummaryThresholdBytes > 0 {
		dst.PreviewSettings.SummaryThresholdBytes = src.PreviewSettings.SummaryThresholdBytes
	}
	if src.Projection.Relevance != nil {
		dst.Projection.Relevance = src.Projection.Relevance
	}
	if src.Projection.ToolCallSupersession != nil {
		dst.Projection.ToolCallSupersession = src.Projection.ToolCallSupersession
	}
	if src.ToolCallMaxResults > 0 {
		dst.ToolCallMaxResults = src.ToolCallMaxResults
	}
	if src.ToolCallTimeoutSec > 0 {
		dst.ToolCallTimeoutSec = src.ToolCallTimeoutSec
	}
	if src.ElicitationTimeoutSec > 0 {
		dst.ElicitationTimeoutSec = src.ElicitationTimeoutSec
	}
	if src.ToolApproval.Mode != "" {
		dst.ToolApproval = src.ToolApproval
	}
	if src.Match.MaxFiles > 0 {
		dst.Match.MaxFiles = src.Match.MaxFiles
	}
	if len(src.Resources.Locations) > 0 ||
		len(src.Resources.Roots) > 0 ||
		len(src.Resources.Upstreams) > 0 ||
		src.Resources.UpstreamStore != nil ||
		strings.TrimSpace(src.Resources.IndexPath) != "" ||
		strings.TrimSpace(src.Resources.SnapshotPath) != "" ||
		strings.TrimSpace(src.Resources.TrimPath) != "" ||
		len(src.Resources.SummaryFiles) > 0 ||
		src.Resources.DescribeMCP ||
		src.Resources.UpstreamSyncConcurrency > 0 ||
		src.Resources.MatchConcurrency > 0 ||
		src.Resources.IndexAsync != nil {
		dst.Resources = src.Resources
	}
	if src.AsyncReinforcementPrompt != nil {
		dst.AsyncReinforcementPrompt = src.AsyncReinforcementPrompt
	}
}

func isZeroNode(node *yaml.Node) bool {
	return node == nil || node.Kind == 0
}

func hasKey(source map[string]interface{}, key string) bool {
	if source == nil {
		return false
	}
	_, ok := source[key]
	return ok
}

func mapLookup(source map[string]interface{}, key string) map[string]interface{} {
	value, ok := source[key]
	if !ok || value == nil {
		return nil
	}
	result, _ := value.(map[string]interface{})
	return result
}

func valuesToServices(value interface{}) []string {
	switch actual := value.(type) {
	case []interface{}:
		out := make([]string, 0, len(actual))
		for _, item := range actual {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	case []string:
		return actual
	case string:
		if strings.TrimSpace(actual) == "" {
			return nil
		}
		parts := strings.Split(actual, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if text := strings.TrimSpace(part); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}
