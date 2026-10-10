package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	approvalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
)

const toolApprovalGuidanceURI = "agently://runtime/tool-approval-guidance"

// appendToolApprovalGuidance explains runtime review only for tools both declared
// to this model and configured for approval in this execution context. It does
// not grant permissions or assert approval support for arbitrary MCP servers.
func appendToolApprovalGuidance(ctx context.Context, defs []*llm.ToolDefinition, docs *binding.Documents) {
	if docs == nil {
		return
	}
	names := map[string]bool{}
	for _, def := range defs {
		if def == nil || strings.TrimSpace(def.Name) == "" {
			continue
		}
		cfg, configured := approvalqueue.ConfigFor(ctx, def.Name)
		if configured && (cfg.IsPrompt() || cfg.IsQueue()) {
			names[def.Name] = true
		}
	}
	// Replace our own document when the active tool surface changes.
	kept := docs.Items[:0]
	for _, doc := range docs.Items {
		if doc == nil || doc.SourceURI != toolApprovalGuidanceURI {
			kept = append(kept, doc)
		}
	}
	docs.Items = kept
	if len(names) == 0 {
		return
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	encodedNames, _ := json.Marshal(ordered)
	docs.Items = append(docs.Items, &binding.Document{
		Title:     "Runtime tool approval",
		SourceURI: toolApprovalGuidanceURI,
		MimeType:  "text/markdown",
		PageContent: "Configured runtime approval tool names (JSON data): " + string(encodedNames) + "\n" +
			"When the user requests an action with one of these tools and its required arguments and scope are clear, invoke the declared tool to initiate the platform approval flow. The runtime suspends execution before any effects, presents the approval UI, and returns the decision or tool result. Do not replace this flow with a manual chat approval question or request the same approval again after a decision. Ask the user when required arguments or material scope remain unresolved. Continue from the actual tool result; a pending approval is not successful execution, and output must not be fabricated.",
	})
}
