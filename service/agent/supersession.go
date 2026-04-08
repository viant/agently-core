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
	runtimeprojection "github.com/viant/agently-core/runtime/projection"
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

// collectToolCallSupersessionHiddenMessageIDs returns message IDs that should be
// hidden from prompt history because newer cacheable tool calls with the same
// supersession key exist in the relevant scope.
//
// Rules:
//   - Prior turns T(0)..T(N-1): per key, keep last `historyLimit` results
//   - Current turn TN: per key, keep last `turnLimit` results
func collectToolCallSupersessionHiddenMessageIDs(
	normalized []normalizedMsg,
	currentTurnIdx int,
	reg tool.Registry,
	projection *config.Projection,
) ([]string, int) {
	suppress := collectToolCallSupersessionSuppressedIndices(normalized, currentTurnIdx, reg, projection)
	if len(suppress) == 0 {
		return nil, 0
	}

	hidden := make([]string, 0, len(suppress))
	tokensFreed := 0
	for i := range normalized {
		if !suppress[i] {
			continue
		}
		msgID := strings.TrimSpace(normalized[i].msg.Id)
		if msgID == "" {
			continue
		}
		hidden = append(hidden, msgID)
		tokensFreed += estimateProjectionTokens(normalized[i].msg)
	}
	return hidden, tokensFreed
}

// applyToolCallSupersession is a compatibility helper used by tests and any
// legacy call sites. It computes superseded message IDs, then filters the
// normalized slice via projection semantics.
func applyToolCallSupersession(
	normalized []normalizedMsg,
	currentTurnIdx int,
	reg tool.Registry,
	projection *config.Projection,
) []normalizedMsg {
	suppress := collectToolCallSupersessionSuppressedIndices(normalized, currentTurnIdx, reg, projection)
	if len(suppress) == 0 {
		return normalized
	}
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

func collectToolCallSupersessionSuppressedIndices(
	normalized []normalizedMsg,
	currentTurnIdx int,
	reg tool.Registry,
	projection *config.Projection,
) map[int]bool {
	if !projection.IsSupersessionEnabled() {
		return nil
	}
	historyLimit := projection.SupersessionHistoryLimit()
	turnLimit := projection.SupersessionTurnLimit()

	var historyEntries []keyEntry
	var turnEntries []keyEntry

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

	suppress := map[int]bool{}
	historyByKey := groupByKey(historyEntries)
	for _, entries := range historyByKey {
		if len(entries) <= historyLimit {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].idx < entries[j].idx })
		cutoff := len(entries) - historyLimit
		for _, e := range entries[:cutoff] {
			suppress[e.idx] = true
		}
	}
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
	return suppress
}

func groupByKey(entries []keyEntry) map[string][]keyEntry {
	m := map[string][]keyEntry{}
	for _, e := range entries {
		m[e.key] = append(m[e.key], e)
	}
	return m
}

func estimateProjectionTokens(msg *apiconv.Message) int {
	if msg == nil {
		return 0
	}
	text := strings.TrimSpace(msg.GetContent())
	if text == "" {
		return 0
	}
	if len(text) < 8 {
		return 1
	}
	return (len(text) + 3) / 4
}

func applyProjectionToNormalized(normalized []normalizedMsg, projection runtimeprojection.ContextProjection) []normalizedMsg {
	if len(normalized) == 0 {
		return normalized
	}
	hiddenTurns := make(map[string]struct{}, len(projection.HiddenTurnIDs))
	for _, turnID := range projection.HiddenTurnIDs {
		turnID = strings.TrimSpace(turnID)
		if turnID == "" {
			continue
		}
		hiddenTurns[turnID] = struct{}{}
	}
	hiddenMessages := make(map[string]struct{}, len(projection.HiddenMessageIDs))
	for _, messageID := range projection.HiddenMessageIDs {
		messageID = strings.TrimSpace(messageID)
		if messageID == "" {
			continue
		}
		hiddenMessages[messageID] = struct{}{}
	}
	if len(hiddenTurns) == 0 && len(hiddenMessages) == 0 {
		return normalized
	}
	result := make([]normalizedMsg, 0, len(normalized))
	for _, item := range normalized {
		msg := item.msg
		if msg == nil {
			continue
		}
		if msg.TurnId != nil {
			if turnID := strings.TrimSpace(*msg.TurnId); turnID != "" {
				if _, ok := hiddenTurns[turnID]; ok {
					continue
				}
			}
		}
		if msgID := strings.TrimSpace(msg.Id); msgID != "" {
			if _, ok := hiddenMessages[msgID]; ok {
				continue
			}
		}
		result = append(result, item)
	}
	return result
}
