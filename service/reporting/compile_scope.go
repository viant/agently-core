package reporting

import (
	"context"
	"errors"
	"github.com/viant/agently-core/protocol/primitive/observe"
	identity "github.com/viant/agently-core/protocol/resource"
)

// Compile is a metadata-only phase. Its explicit host scope ends before the
// original execution context proceeds to datasource admission or dispatch.
func (s *Service) Compile(ctx context.Context, request *CompileRequest) (out *CompileResult, resultErr error) {
	span := observe.Start(ctx, "report.compile", observe.Labels{Kind: "report"})
	defer func() { span.End(observe.OutcomeFor(resultErr), observe.CacheUnknown) }()
	if s.reportCatalog != nil && s.hasResourceReader() {
		scoped, finish, err := s.reportCatalog.MetadataRead(ctx)
		if err != nil {
			if finish != nil {
				err = errors.Join(err, finish())
			}
			return nil, err
		}
		if finish == nil {
			return nil, identity.ErrResourceDenied
		}
		defer func() {
			if err := finish(); err != nil {
				out = nil
				resultErr = errors.Join(resultErr, err)
			}
		}()
		if scoped == nil {
			return nil, identity.ErrResourceDenied
		}
		ctx = scoped
	}
	return s.compileUnscoped(ctx, request)
}
