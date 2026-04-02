package executil

import (
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/pkg/agently/tool/resolver"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
)

// ApprovalView is the canonical view model built from a tool call request and
// its approval config. It is shared between queue-mode persistence and
// prompt-mode elicitation so both surfaces render consistently.
type ApprovalView struct {
	ToolName string
	Title    string
	Message  string
	Data     interface{}
}

// BuildApprovalView extracts title, message and data from the original tool
// request arguments using the selectors declared in cfg.
// When selectors are absent or do not resolve the function falls back to
// safe defaults so the approval UI always has something meaningful to show.
func BuildApprovalView(toolName string, args map[string]interface{}, cfg *llm.ApprovalConfig) ApprovalView {
	displayName := mcpname.Display(strings.TrimSpace(toolName))

	v := ApprovalView{
		ToolName: toolName,
		Title:    displayName,
		Message:  fmt.Sprintf("Tool %s requires permission to run.", displayName),
		Data:     args,
	}

	if cfg == nil {
		return v
	}

	// Resolve title
	if sel := cfg.EffectiveTitleSelector(); sel != "" {
		if raw := resolver.Select(sel, args, nil); raw != nil {
			if s := strings.TrimSpace(fmt.Sprintf("%v", raw)); s != "" {
				v.Title = s
			}
		}
	}

	// Resolve message from UI binding selector first, then Prompt.Message
	if cfg.UI != nil && strings.TrimSpace(cfg.UI.MessageSelector) != "" {
		if raw := resolver.Select(cfg.UI.MessageSelector, args, nil); raw != nil {
			if s := strings.TrimSpace(fmt.Sprintf("%v", raw)); s != "" {
				v.Message = s
			}
		}
	} else if cfg.Prompt != nil && strings.TrimSpace(cfg.Prompt.Message) != "" {
		v.Message = strings.TrimSpace(cfg.Prompt.Message)
	}

	// Resolve data
	if cfg.UI != nil && strings.TrimSpace(cfg.UI.DataSelector) != "" {
		if raw := resolver.Select(cfg.UI.DataSelector, args, nil); raw != nil {
			v.Data = raw
		}
	} else if strings.TrimSpace(cfg.DataSourceSelector) != "" {
		if raw := resolver.Select(cfg.DataSourceSelector, args, nil); raw != nil {
			v.Data = raw
		}
	}

	return v
}
