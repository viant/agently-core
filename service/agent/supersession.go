package agent

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/protocol/tool"
)

// supersessionKey computes a stable key for a tool call based on tool name
// and canonical (sorted) JSON args. Two calls with the same name and args
// produce the same key regardless of map iteration order.
func supersessionKey(toolName string, args map[string]interface{}) string {
	name := strings.TrimSpace(toolName)
	canonical := canonicalArgsJSON(args)
	raw := name + "|" + canonical
	sum := md5.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// canonicalArgsJSON serializes args to JSON with sorted keys for deterministic
// hashing. Returns "{}" for nil or empty args.
func canonicalArgsJSON(args map[string]interface{}) string {
	if len(args) == 0 {
		return "{}"
	}
	sorted := sortedMapJSON(args)
	data, err := json.Marshal(sorted)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// sortedMapJSON recursively builds an ordered representation of a map for
// canonical JSON marshaling. Go's json.Marshal sorts keys by default since
// Go 1.12, but nested maps inside interface{} may not be map[string]interface{}.
func sortedMapJSON(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if nested, ok := v.(map[string]interface{}); ok {
			out[k] = sortedMapJSON(nested)
		} else {
			out[k] = v
		}
	}
	return out
}

// applyToolCallSupersession filters normalized messages to suppress older
// cacheable tool outputs that are superseded by newer calls with the same
// supersession key. It checks tool.Registry to resolve cacheable status.
//
// Rules:
//   - Prior turns T(0)..T(N-1): per key, keep last `historyLimit` results
//   - Current turn TN: per key, keep last `turnLimit` results
func applyToolCallSupersession(
	normalized []normalizedMsg,
	currentTurnIdx int,
	reg tool.Registry,
	compaction *config.Compaction,
) []normalizedMsg {
	if !compaction.IsSupersessionEnabled() {
		return normalized
	}
	historyLimit := compaction.SupersessionHistoryLimit()
	turnLimit := compaction.SupersessionTurnLimit()

	// Collect supersession keys for cacheable tool-result messages.
	var historyEntries []keyEntry // entries from prior turns
	var turnEntries []keyEntry    // entries from current turn

	for i, item := range normalized {
		tc := messageToolCall(item.msg)
		if tc == nil {
			continue
		}
		toolName := strings.TrimSpace(tc.ToolName)
		if toolName == "" {
			continue
		}
		if !isToolCacheable(reg, toolName) {
			continue
		}
		args := toolCallArgs(item.msg)
		key := supersessionKey(toolName, args)
		entry := keyEntry{idx: i, key: key}
		if item.turnIdx == currentTurnIdx {
			turnEntries = append(turnEntries, entry)
		} else {
			historyEntries = append(historyEntries, entry)
		}
	}

	// Build the set of indices to suppress.
	suppress := map[int]bool{}

	// History: per key, keep only the last `historyLimit` entries.
	historyByKey := groupByKey(historyEntries)
	for _, entries := range historyByKey {
		if len(entries) <= historyLimit {
			continue
		}
		// Sort by index (chronological order) — entries are already in order
		// since we iterated normalized sequentially.
		sort.Slice(entries, func(i, j int) bool { return entries[i].idx < entries[j].idx })
		// Suppress all but the last `historyLimit`
		cutoff := len(entries) - historyLimit
		for _, e := range entries[:cutoff] {
			suppress[e.idx] = true
		}
	}

	// Current turn: per key, keep only the last `turnLimit` entries.
	turnByKey := groupByKey(turnEntries)
	for _, entries := range turnByKey {
		if len(entries) <= turnLimit {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].idx < entries[j].idx })
		cutoff := len(entries) - turnLimit
		for _, e := range entries[:cutoff] {
			suppress[e.idx] = true
		}
	}

	if len(suppress) == 0 {
		return normalized
	}

	// Filter out suppressed entries.
	result := make([]normalizedMsg, 0, len(normalized)-len(suppress))
	for i, item := range normalized {
		if suppress[i] {
			continue
		}
		result = append(result, item)
	}
	return result
}

// toolCallArgs extracts parsed arguments from a tool-call message using the
// same path as binding_history (RequestPayload.InlineBody).
func toolCallArgs(msg *apiconv.Message) map[string]interface{} {
	if msg == nil {
		return nil
	}
	return msg.ToolCallArguments()
}

// isToolCacheable checks whether a tool is marked as cacheable via the
// registry's tool definition.
func isToolCacheable(reg tool.Registry, toolName string) bool {
	if reg == nil {
		return false
	}
	def, ok := reg.GetDefinition(toolName)
	if !ok || def == nil {
		return false
	}
	return def.Cacheable
}

type keyEntry struct {
	idx int
	key string
}

func groupByKey(entries []keyEntry) map[string][]keyEntry {
	m := map[string][]keyEntry{}
	for _, e := range entries {
		m[e.key] = append(m[e.key], e)
	}
	return m
}
