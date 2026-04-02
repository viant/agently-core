package bundle

import (
	"sort"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
)

type ResolveResult struct {
	Definitions  []llm.ToolDefinition
	ApprovalByID map[string]*llm.ApprovalConfig
}

// ResolveDefinitions expands bundle match rules into concrete tool definitions using matchFn.
// It applies rule-level excludes and de-duplicates by canonical tool name.
func ResolveDefinitions(b *Bundle, matchFn func(pattern string) []*llm.ToolDefinition) []llm.ToolDefinition {
	res := ResolveDefinitionsWithOptions(b, matchFn)
	return res.Definitions
}

// ResolveDefinitionsWithOptions expands bundle match rules and returns both
// definitions and per-tool approval config.
func ResolveDefinitionsWithOptions(b *Bundle, matchFn func(pattern string) []*llm.ToolDefinition) ResolveResult {
	if b == nil || matchFn == nil {
		return ResolveResult{}
	}
	selected := map[string]llm.ToolDefinition{}
	approvalByKey := map[string]*llm.ApprovalConfig{}
	for _, r := range b.Match {
		namePattern := strings.TrimSpace(r.Name)
		if namePattern == "" {
			continue
		}
		excluded := map[string]struct{}{}
		for _, ex := range r.Exclude {
			ex = strings.TrimSpace(ex)
			if ex == "" {
				continue
			}
			for _, pattern := range patternVariants(ex) {
				for _, d := range matchFn(pattern) {
					if d == nil {
						continue
					}
					excluded[canonicalKey(d.Name)] = struct{}{}
				}
			}
		}
		for _, pattern := range patternVariants(namePattern) {
			for _, d := range matchFn(pattern) {
				if d == nil {
					continue
				}
				key := canonicalKey(d.Name)
				if _, ok := excluded[key]; ok {
					continue
				}
				if _, ok := selected[key]; ok {
					if r.Approval.IsQueue() {
						approvalByKey[key] = r.Approval
					}
					continue
				}
				selected[key] = *d
				if r.Approval.IsQueue() {
					approvalByKey[key] = r.Approval
				}
			}
		}
	}
	if len(selected) == 0 {
		return ResolveResult{}
	}
	out := make([]llm.ToolDefinition, 0, len(selected))
	for _, d := range selected {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return ResolveResult{
		Definitions:  out,
		ApprovalByID: approvalByKey,
	}
}

func canonicalKey(name string) string {
	return mcpname.Canonical(strings.TrimSpace(name))
}

func patternVariants(name string) []string {
	raw := strings.TrimSpace(name)
	if raw == "" {
		return nil
	}
	variants := map[string]struct{}{
		raw: {},
	}
	canonical := canonicalKey(raw)
	if canonical != "" {
		variants[canonical] = struct{}{}
		n := mcpname.Name(canonical)
		service := strings.TrimSpace(n.Service())
		method := strings.TrimSpace(n.Method())
		if service != "" && method != "" {
			variants[service+":"+method] = struct{}{}
			variants[service+"."+method] = struct{}{}
			variants[service+"/"+method] = struct{}{}
		}
	}
	result := make([]string, 0, len(variants))
	for variant := range variants {
		result = append(result, variant)
	}
	sort.Strings(result)
	return result
}
