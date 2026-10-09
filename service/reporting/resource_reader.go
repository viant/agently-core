package reporting

import (
	"context"
	"encoding/json"
	identity "github.com/viant/agently-core/protocol/resource"
	"reflect"
)

// ResourceReader preserves the original provider pin and lease across report
// retrieval, compilation, execution and export. Concrete ResourceResolver still
// implements this contract; alternate carriers must retain operation admission.
type ResourceReader interface {
	Resolve(context.Context, identity.ResourceRef) (*identity.ResolvedResource, error)
	ReadResolved(context.Context, identity.ResolvedResource) (json.RawMessage, *identity.ResolvedResource, error)
}

// ProviderResourceReader refreshes a durable bookmark only at its stored provider.
type ProviderResourceReader interface {
	ResourceReader
	ResolveForProvider(context.Context, identity.ResourceRef, string) (*identity.ResolvedResource, error)
}
type ResourceReaderFactory func(context.Context, string) (ResourceReader, error)

// SetResourceReaderFactory is a pre-serving host composition seam. It overrides
// only resource delivery; actionAuthorize and metadata scopes remain intact.
func (s *Service) SetResourceReaderFactory(factory ResourceReaderFactory) {
	if s != nil {
		s.resourceReaderFactory = factory
	}
}
func (s *Service) hasResourceReader() bool {
	return s != nil && (s.resourceReaderFactory != nil || s.resourceResolver != nil)
}
func (s *Service) resourceReaderFor(ctx context.Context, operation string) (ResourceReader, error) {
	if s == nil || ctx == nil || ctx.Err() != nil {
		return nil, identity.ErrResourceDenied
	}
	if s.resourceReaderFactory != nil {
		return checkedResourceReader(s.resourceReaderFactory(ctx, operation))
	}
	if s.resourceResolver == nil {
		return nil, identity.ErrResourceDenied
	}
	return checkedResourceReader(s.resourceResolver(ctx, operation))
}

func checkedResourceReader(reader ResourceReader, err error) (ResourceReader, error) {
	if err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, identity.ErrResourceDenied
	}
	// A factory may accidentally return a typed nil pointer as an interface.
	value := reflect.ValueOf(reader)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil, identity.ErrResourceDenied
	}
	return reader, nil
}
