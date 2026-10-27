package reporting

import (
	"context"

	"github.com/viant/forge/backend/reporting/materializer"
)

// PrepareRuntime initializes only the code-owned reporting bundle before the
// host accepts interactive work. It does not load authored reports or data.
func (s *Service) PrepareRuntime(ctx context.Context) error {
	return materializer.PrepareTrustedRuntime(ctx)
}
