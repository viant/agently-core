package planner

type Service struct{}

func New() *Service {
	return &Service{}
}

func (s *Service) Run(raw string, vctx ValidationContext) (*Output, []ValidationError, error) {
	out, err := Parse(raw)
	if err != nil {
		return nil, nil, err
	}
	return out, Validate(out, vctx), nil
}

func NewContext(trigger Trigger, attempt int, out *Output) *PlannerContext {
	if out == nil {
		return &PlannerContext{Trigger: trigger, Attempt: attempt}
	}
	return &PlannerContext{
		Trigger:             trigger,
		Attempt:             attempt,
		StrategyFamily:      out.StrategyFamily,
		BaseProfiles:        append([]string(nil), out.BaseProfiles...),
		ToolBundles:         append([]string(nil), out.ToolBundles...),
		TemplateID:          out.TemplateID,
		ExecutionOrder:      append([]string(nil), out.ExecutionOrder...),
		RequiredEvidence:    append([]string(nil), out.RequiredEvidence...),
		Guards:              append([]string(nil), out.FinalizationGuards...),
		NarrationPolicy:     cloneMap(out.NarrationPolicy),
		WorkspaceExtensions: cloneMap(out.WorkspaceExtensions),
		ParallelToolCalls:   out.ParallelToolCalls,
	}
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
