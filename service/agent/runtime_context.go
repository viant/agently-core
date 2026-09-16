package agent

import (
	"strings"

	agruntime "github.com/viant/agently-core/runtime"
)

func ensureVisibleContext(input *QueryInput) map[string]interface{} {
	if input == nil {
		return nil
	}
	if input.Context == nil {
		input.Context = map[string]interface{}{}
	}
	return input.Context
}

func ensureRuntimeContext(input *QueryInput) *agruntime.Context {
	if input == nil {
		return nil
	}
	if input.Runtime == nil {
		input.Runtime = &agruntime.Context{}
	}
	return input.Runtime
}

func runtimeModelSource(input *QueryInput) string {
	if input == nil {
		return ""
	}
	if source := strings.TrimSpace(input.ModelSource); source != "" {
		return source
	}
	if input.Runtime != nil {
		if source := strings.TrimSpace(input.Runtime.ModelSource); source != "" {
			return source
		}
	}
	if input.Context != nil {
		if source, _ := input.Context["modelSource"].(string); strings.TrimSpace(source) != "" {
			return strings.TrimSpace(source)
		}
	}
	return ""
}

// modelSourcePriority returns the precedence of a model selection source.
// A model without provenance is deliberately treated as a caller override for
// backward compatibility with clients that only send `model`.
func modelSourcePriority(input *QueryInput) int {
	if input == nil {
		return 0
	}
	source := strings.TrimSpace(runtimeModelSource(input))
	if source == "" {
		if strings.TrimSpace(input.ModelOverride) != "" {
			return 100 // legacy model field: explicit caller override
		}
		return 0
	}
	switch {
	case source == "caller", source == "query.modelOverride":
		return 100
	case source == "intake.activationRule" || strings.HasPrefix(source, "intake.activationRule."):
		return 90
	case source == "intent.profile":
		return 80
	case strings.HasPrefix(source, "skill"):
		return 70
	case source == "conversation.defaultModel":
		return 60
	case source == "agent.model":
		return 50
	default:
		return 100
	}
}

func setRuntimeModelSource(input *QueryInput, source string) {
	if input == nil {
		return
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return
	}
	rt := ensureRuntimeContext(input)
	rt.ModelSource = source
	input.ModelSource = source
}

func runtimeResolvedWorkdir(input *QueryInput) string {
	if input == nil || input.Runtime == nil {
		return ""
	}
	return input.Runtime.EffectiveWorkdir()
}

func setRuntimeResolvedWorkdir(input *QueryInput, workdir string) {
	if input == nil {
		return
	}
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		return
	}
	rt := ensureRuntimeContext(input)
	if rt.Workdir == "" {
		rt.Workdir = workdir
	}
	rt.ResolvedWorkdir = workdir
}

func runtimeBearerToken(input *QueryInput) string {
	if input == nil || input.Runtime == nil {
		return ""
	}
	return strings.TrimSpace(input.Runtime.BearerToken)
}

func setRuntimeBearerToken(input *QueryInput, token string) {
	if input == nil {
		return
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	rt := ensureRuntimeContext(input)
	rt.BearerToken = token
}

func setVisibleResolvedWorkdir(input *QueryInput, workdir string) {
	ctx := ensureVisibleContext(input)
	if ctx == nil {
		return
	}
	rt := &agruntime.Context{Workdir: workdir, ResolvedWorkdir: workdir}
	input.Context = agruntime.ProjectVisibleContext(ctx, rt, false)
}
