package reporting

import (
	"context"
	identity "github.com/viant/agently-core/protocol/resource"
	sharedengine "github.com/viant/forge/backend/reporting/engine"
)

// ResourceResolver supplies the trusted source and policy for a reporting
// operation. The source must serve Forge working/stamped report definitions.
type ResourceResolver func(context.Context, string) (*identity.ResourceResolver, error)

func (s *Service) SetResourceResolver(resolve ResourceResolver) {
	if s != nil {
		s.resourceResolver = resolve
		if s.execution != nil {
			s.execution.SetResourceResolver(sharedengine.ResourceResolver(resolve))
		}
	}
}

func cloneResourceRef(value *identity.ResourceRef) *identity.ResourceRef {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func cloneResolvedResource(value *identity.ResolvedResource) *identity.ResolvedResource {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// UsesResourceResolution identifies explicitly configured unified report mode.
func (s *Service) UsesResourceResolution() bool { return s != nil && s.hasResourceReader() }
