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
	if input == nil || input.Runtime == nil {
		return ""
	}
	return strings.TrimSpace(input.Runtime.ModelSource)
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
