package reporting

import (
	"context"
	"github.com/viant/agently-core/protocol/primitive/observe"
	identity "github.com/viant/agently-core/protocol/resource"
)

// Compile uses the same composition-time engine as synchronous execution.
// The shared engine owns metadata scopes and canonical compile validation.
func (s *Service) Compile(ctx context.Context, request *CompileRequest) (out *CompileResult, resultErr error) {
	span := observe.Start(ctx, "report.compile", observe.Labels{Kind: "report"})
	defer func() { span.End(observe.OutcomeFor(resultErr), observe.CacheUnknown) }()
	if s == nil || s.execution == nil {
		return nil, identity.ErrResourceDenied
	}
	return s.execution.Compile(ctx, request)
}
