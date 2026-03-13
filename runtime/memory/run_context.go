package memory

import "context"

// RunMeta captures the active persisted run identity and loop iteration.
type RunMeta struct {
	RunID     string
	Iteration int
}

type runMetaKeyT string

var runMetaKey = runMetaKeyT("runMeta")

// WithRunMeta stores run metadata on the context for downstream persistence.
func WithRunMeta(ctx context.Context, meta RunMeta) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, runMetaKey, meta)
}

// RunMetaFromContext returns run metadata when available.
func RunMetaFromContext(ctx context.Context) (RunMeta, bool) {
	if ctx == nil {
		return RunMeta{}, false
	}
	if v := ctx.Value(runMetaKey); v != nil {
		if meta, ok := v.(RunMeta); ok {
			return meta, true
		}
	}
	return RunMeta{}, false
}
