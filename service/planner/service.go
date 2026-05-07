package planner

type Service struct{}

func New() *Service {
	return &Service{}
}

func (s *Service) Run(raw string, vctx ValidationContext) (Output, []ValidationError, error) {
	out, err := Parse(raw)
	if err != nil {
		return nil, nil, err
	}
	return out, Validate(out, vctx), nil
}

func NewContext(trigger Trigger, attempt int, out Output) *PlannerContext {
	if len(out) == 0 {
		return &PlannerContext{Trigger: trigger, Attempt: attempt}
	}
	return &PlannerContext{
		Trigger: trigger,
		Attempt: attempt,
		Data:    CloneOutput(out),
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
