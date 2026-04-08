package toolapproval

import (
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/pkg/agently/tool/resolver"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
)

// View is the canonical view model built from a tool call request and its approval config.
type View struct {
	ToolName string                 `json:"toolName"`
	Title    string                 `json:"title"`
	Message  string                 `json:"message"`
	Data     interface{}            `json:"data,omitempty"`
	Editors  []*EditorView          `json:"editors,omitempty"`
	Forge    *llm.ApprovalForgeView `json:"forge,omitempty"`
}

type EditorView struct {
	Name        string        `json:"name"`
	Kind        string        `json:"kind"`
	Path        string        `json:"path,omitempty"`
	Label       string        `json:"label,omitempty"`
	Description string        `json:"description,omitempty"`
	Options     []*OptionView `json:"options,omitempty"`
}

type OptionView struct {
	ID          string      `json:"id"`
	Label       string      `json:"label"`
	Description string      `json:"description,omitempty"`
	Item        interface{} `json:"item,omitempty"`
	Selected    bool        `json:"selected"`
}

func BuildView(toolName string, args map[string]interface{}, cfg *llm.ApprovalConfig) View {
	displayName := mcpname.Display(strings.TrimSpace(toolName))
	v := View{
		ToolName: toolName,
		Title:    displayName,
		Message:  fmt.Sprintf("Tool %s requires permission to run.", displayName),
		Data:     args,
	}

	if cfg == nil {
		return applyToolSpecificView(v, toolName, args)
	}
	if sel := cfg.EffectiveTitleSelector(); sel != "" {
		if raw := resolver.Select(sel, args, nil); raw != nil {
			if s := strings.TrimSpace(fmt.Sprintf("%v", raw)); s != "" {
				v.Title = s
			}
		}
	}
	if cfg.UI != nil && strings.TrimSpace(cfg.UI.MessageSelector) != "" {
		if raw := resolver.Select(cfg.UI.MessageSelector, args, nil); raw != nil {
			if s := strings.TrimSpace(fmt.Sprintf("%v", raw)); s != "" {
				v.Message = s
			}
		}
	} else if cfg.Prompt != nil && strings.TrimSpace(cfg.Prompt.Message) != "" {
		v.Message = strings.TrimSpace(cfg.Prompt.Message)
	}
	if cfg.UI != nil && strings.TrimSpace(cfg.UI.DataSelector) != "" {
		if raw := resolver.Select(cfg.UI.DataSelector, args, nil); raw != nil {
			v.Data = raw
		}
	} else if strings.TrimSpace(cfg.DataSourceSelector) != "" {
		if raw := resolver.Select(cfg.DataSourceSelector, args, nil); raw != nil {
			v.Data = raw
		}
	}
	if cfg.UI != nil && len(cfg.UI.Editable) > 0 {
		v.Editors = buildEditors(args, cfg.UI.Editable)
	}
	if cfg.UI != nil && cfg.UI.Forge != nil {
		v.Forge = cfg.UI.Forge
	}
	return applyToolSpecificView(v, toolName, args)
}

func applyToolSpecificView(v View, toolName string, args map[string]interface{}) View {
	if mcpname.Canonical(strings.TrimSpace(toolName)) != mcpname.Canonical("system/os:getEnv") {
		return v
	}
	names := envNames(args)
	v.Title = "OS Env Access"
	if len(names) == 0 {
		v.Message = "The agent wants access to your environment variables."
		return v
	}
	v.Message = formatEnvMessage(names)
	v.Data = map[string]interface{}{"names": names}
	return v
}

func buildEditors(args map[string]interface{}, fields []*llm.ApprovalEditableField) []*EditorView {
	result := make([]*EditorView, 0, len(fields))
	for _, field := range fields {
		editor := buildEditor(args, field)
		if editor != nil {
			result = append(result, editor)
		}
	}
	return result
}

func buildEditor(args map[string]interface{}, field *llm.ApprovalEditableField) *EditorView {
	if field == nil {
		return nil
	}
	name := strings.TrimSpace(field.Name)
	if name == "" {
		return nil
	}
	kind := strings.ToLower(strings.TrimSpace(field.Kind))
	if kind == "" {
		kind = "checkbox_list"
	}
	editor := &EditorView{
		Name:        name,
		Kind:        kind,
		Path:        normalizePath(field.Selector, name),
		Label:       strings.TrimSpace(field.Label),
		Description: strings.TrimSpace(field.Description),
	}
	raw := resolver.Select(field.Selector, args, nil)
	if items, ok := raw.([]interface{}); ok {
		for index, item := range items {
			if option := buildOption(item, index, field); option != nil {
				editor.Options = append(editor.Options, option)
			}
		}
	}
	if len(editor.Options) == 0 {
		return nil
	}
	return editor
}

func buildOption(item interface{}, index int, field *llm.ApprovalEditableField) *OptionView {
	id := optionValue(item, field, index)
	label := optionLabel(item, field)
	if label == "" {
		label = id
	}
	if id == "" || label == "" {
		return nil
	}
	return &OptionView{
		ID:          id,
		Label:       label,
		Description: optionDescription(item, field),
		Item:        item,
		Selected:    true,
	}
}

func optionValue(item interface{}, field *llm.ApprovalEditableField, index int) string {
	if field != nil && strings.TrimSpace(field.ItemValueSelector) != "" {
		if value := selectItemField(item, strings.TrimSpace(field.ItemValueSelector)); value != nil {
			if text := strings.TrimSpace(fmt.Sprintf("%v", value)); text != "" {
				return text
			}
		}
	}
	if text := strings.TrimSpace(fmt.Sprintf("%v", item)); text != "" && !strings.HasPrefix(text, "map[") {
		return text
	}
	return fmt.Sprintf("__index_%d", index)
}

func optionLabel(item interface{}, field *llm.ApprovalEditableField) string {
	if field != nil && strings.TrimSpace(field.ItemLabelSelector) != "" {
		if value := selectItemField(item, strings.TrimSpace(field.ItemLabelSelector)); value != nil {
			if text := strings.TrimSpace(fmt.Sprintf("%v", value)); text != "" {
				return text
			}
		}
	}
	return strings.TrimSpace(fmt.Sprintf("%v", item))
}

func optionDescription(item interface{}, field *llm.ApprovalEditableField) string {
	if field == nil || strings.TrimSpace(field.ItemDescriptionSelector) == "" {
		return ""
	}
	value := selectItemField(item, strings.TrimSpace(field.ItemDescriptionSelector))
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func selectItemField(item interface{}, selector string) interface{} {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil
	}
	if strings.HasPrefix(selector, "input.") || strings.HasPrefix(selector, "output.") || selector == "input" || selector == "output" {
		return resolver.Select(selector, item, item)
	}
	return resolver.Select("input."+selector, item, item)
}

func normalizePath(selector, fallback string) string {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return strings.TrimSpace(fallback)
	}
	selector = strings.TrimPrefix(selector, "input.")
	selector = strings.TrimPrefix(selector, "output.")
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return strings.TrimSpace(fallback)
	}
	return selector
}

func envNames(args map[string]interface{}) []string {
	if len(args) == 0 {
		return nil
	}
	raw, ok := args["names"]
	if !ok || raw == nil {
		return nil
	}
	values, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, item := range values {
		name := strings.TrimSpace(fmt.Sprintf("%v", item))
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func formatEnvMessage(names []string) string {
	if len(names) == 0 {
		return "The agent wants access to your environment variables."
	}
	if len(names) == 1 {
		return fmt.Sprintf("The agent wants access to your %s environment variable.", names[0])
	}
	last := names[len(names)-1]
	rest := strings.Join(names[:len(names)-1], ", ")
	if len(names) == 2 {
		rest = names[0]
	}
	return fmt.Sprintf("The agent wants access to your %s and %s environment variables.", rest, last)
}
