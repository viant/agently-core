package planner

import (
	"context"
	"fmt"
	"strings"

	agentmdl "github.com/viant/agently-core/protocol/agent"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	tplbundlerepo "github.com/viant/agently-core/workspace/repository/templatebundle"
	toolbundlerepo "github.com/viant/agently-core/workspace/repository/toolbundle"
)

type ValidationError struct {
	Code    string `json:"code,omitempty"`
	Field   string `json:"field,omitempty"`
	Value   string `json:"value,omitempty"`
	Message string `json:"message,omitempty"`
}

func (e ValidationError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	parts := []string{strings.TrimSpace(e.Code), strings.TrimSpace(e.Field), strings.TrimSpace(e.Value)}
	return strings.TrimSpace(strings.Join(parts, " "))
}

type ValidationContext struct {
	ProfileRepo        *promptrepo.Repository
	ToolBundleRepo     *toolbundlerepo.Repository
	TemplateRepo       *tplrepo.Repository
	TemplateBundleRepo *tplbundlerepo.Repository
	Agent              *agentmdl.Agent
}

func Validate(out *Output, vctx ValidationContext) []ValidationError {
	if out == nil {
		return nil
	}
	var result []ValidationError
	result = append(result, validateProfiles(context.Background(), out, vctx)...)
	result = append(result, validateToolBundles(context.Background(), out, vctx)...)
	result = append(result, validateTemplate(context.Background(), out, vctx)...)
	result = append(result, validateExecutionOrder(out)...)
	return result
}

func validateProfiles(ctx context.Context, out *Output, vctx ValidationContext) []ValidationError {
	if len(out.BaseProfiles) == 0 {
		return nil
	}
	if vctx.ProfileRepo == nil {
		return []ValidationError{{
			Code:    "profile_repo_missing",
			Field:   "baseProfiles",
			Message: "profile repository not configured",
		}}
	}
	all, err := vctx.ProfileRepo.LoadAll(ctx)
	if err != nil {
		return []ValidationError{{
			Code:    "profile_repo_error",
			Field:   "baseProfiles",
			Message: err.Error(),
		}}
	}
	known := map[string]struct{}{}
	for _, profile := range all {
		if profile == nil {
			continue
		}
		if id := strings.TrimSpace(profile.ID); id != "" {
			known[strings.ToLower(id)] = struct{}{}
		}
	}
	allow := map[string]struct{}{}
	restrict := vctx.Agent != nil && len(vctx.Agent.Prompts.Bundles) > 0
	if restrict {
		for _, id := range vctx.Agent.Prompts.Bundles {
			if trimmed := strings.ToLower(strings.TrimSpace(id)); trimmed != "" {
				allow[trimmed] = struct{}{}
			}
		}
	}
	var result []ValidationError
	for _, raw := range out.BaseProfiles {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := known[key]; !ok {
			result = append(result, ValidationError{
				Code:    "unknown_profile",
				Field:   "baseProfiles",
				Value:   value,
				Message: fmt.Sprintf("unknown profile %q", value),
			})
			continue
		}
		if restrict {
			if _, ok := allow[key]; !ok {
				result = append(result, ValidationError{
					Code:    "profile_not_allowed",
					Field:   "baseProfiles",
					Value:   value,
					Message: fmt.Sprintf("profile %q is not allowed for this agent", value),
				})
			}
		}
	}
	return result
}

func validateToolBundles(ctx context.Context, out *Output, vctx ValidationContext) []ValidationError {
	if len(out.ToolBundles) == 0 {
		return nil
	}
	if vctx.ToolBundleRepo == nil {
		return []ValidationError{{
			Code:    "tool_bundle_repo_missing",
			Field:   "toolBundles",
			Message: "tool bundle repository not configured",
		}}
	}
	all, err := vctx.ToolBundleRepo.LoadAll(ctx)
	if err != nil {
		return []ValidationError{{
			Code:    "tool_bundle_repo_error",
			Field:   "toolBundles",
			Message: err.Error(),
		}}
	}
	known := map[string]struct{}{}
	for _, bundle := range all {
		if bundle == nil {
			continue
		}
		if id := strings.TrimSpace(bundle.ID); id != "" {
			known[strings.ToLower(id)] = struct{}{}
		}
	}
	var result []ValidationError
	for _, raw := range out.ToolBundles {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, ok := known[strings.ToLower(value)]; !ok {
			result = append(result, ValidationError{
				Code:    "unknown_bundle",
				Field:   "toolBundles",
				Value:   value,
				Message: fmt.Sprintf("unknown tool bundle %q", value),
			})
		}
	}
	return result
}

func validateTemplate(ctx context.Context, out *Output, vctx ValidationContext) []ValidationError {
	value := strings.TrimSpace(out.TemplateID)
	if value == "" {
		return nil
	}
	if vctx.TemplateRepo == nil {
		return []ValidationError{{
			Code:    "template_repo_missing",
			Field:   "templateId",
			Value:   value,
			Message: "template repository not configured",
		}}
	}
	templates, err := vctx.TemplateRepo.LoadAll(ctx)
	if err != nil {
		return []ValidationError{{
			Code:    "template_repo_error",
			Field:   "templateId",
			Value:   value,
			Message: err.Error(),
		}}
	}
	templateKey := strings.ToLower(value)
	found := false
	for _, tpl := range templates {
		if tpl == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(tpl.ID), value) || strings.EqualFold(strings.TrimSpace(tpl.Name), value) {
			found = true
			break
		}
	}
	if !found {
		return []ValidationError{{
			Code:    "unknown_template",
			Field:   "templateId",
			Value:   value,
			Message: fmt.Sprintf("unknown template %q", value),
		}}
	}
	if vctx.Agent == nil || len(vctx.Agent.Template.Bundles) == 0 {
		return nil
	}
	if vctx.TemplateBundleRepo == nil {
		return []ValidationError{{
			Code:    "template_bundle_repo_missing",
			Field:   "templateId",
			Value:   value,
			Message: "template bundle repository not configured",
		}}
	}
	allBundles, err := vctx.TemplateBundleRepo.LoadAll(ctx)
	if err != nil {
		return []ValidationError{{
			Code:    "template_bundle_repo_error",
			Field:   "templateId",
			Value:   value,
			Message: err.Error(),
		}}
	}
	allowed := map[string]struct{}{}
	for _, bundleID := range vctx.Agent.Template.Bundles {
		for _, bundle := range allBundles {
			if bundle == nil || !strings.EqualFold(strings.TrimSpace(bundle.ID), strings.TrimSpace(bundleID)) {
				continue
			}
			for _, name := range bundle.Templates {
				if trimmed := strings.ToLower(strings.TrimSpace(name)); trimmed != "" {
					allowed[trimmed] = struct{}{}
				}
			}
		}
	}
	if _, ok := allowed[templateKey]; ok {
		return nil
	}
	return []ValidationError{{
		Code:    "template_not_allowed",
		Field:   "templateId",
		Value:   value,
		Message: fmt.Sprintf("template %q is not allowed for this agent", value),
	}}
}

func validateExecutionOrder(out *Output) []ValidationError {
	if len(out.ExecutionOrder) == 0 {
		return nil
	}
	declared := map[string]struct{}{}
	for _, raw := range out.RequiredEvidence {
		if trimmed := strings.ToLower(strings.TrimSpace(raw)); trimmed != "" {
			declared[trimmed] = struct{}{}
		}
	}
	var result []ValidationError
	for _, raw := range out.ExecutionOrder {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, ok := declared[strings.ToLower(value)]; !ok {
			result = append(result, ValidationError{
				Code:    "execution_order_undeclared",
				Field:   "executionOrder",
				Value:   value,
				Message: fmt.Sprintf("execution order step %q is not declared in requiredEvidence", value),
			})
		}
	}
	return result
}
