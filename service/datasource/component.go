package datasource

import (
	"context"
	"fmt"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	windowprotocol "github.com/viant/agently-core/protocol/window"
	identity "github.com/viant/agently-core/protocol/resource"
	"strings"
)

func (s *Service) validateComponentSource(ctx context.Context, ds *dsproto.DataSource) error {
	if ds == nil || ds.Backend == nil {
		return nil
	}
	backend := ds.Backend
	if backend.ProducerKind != "" && backend.ProducerKind != "datly" && backend.ProducerKind != "mcp" {
		return fmt.Errorf("unknown datasource producer kind")
	}
	classified := backend.ProducerKind == "datly" || s.components != nil && s.components.IsComponentProducer(backend.Service)
	if backend.Component == nil {
		if classified {
			return fmt.Errorf("Datly producer requires an exact component pin")
		}
		return nil
	}
	if backend.Kind != dsproto.BackendMCPTool || backend.ProducerKind == "mcp" {
		return fmt.Errorf("component source cannot downgrade to generic backend")
	}
	pin := backend.Component
	fingerprint := func(value string) bool {
		return (identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: value}).Valid()
	}
	if pin.ID == "" || strings.TrimSpace(pin.ID) != pin.ID || pin.Revision == "" || strings.TrimSpace(pin.Revision) != pin.Revision || pin.Revision == "active" || pin.Revision == "latest" || pin.Revision == "working" || (pin.Kind != "linked" && pin.Kind != "dynamic") || !fingerprint(pin.ContentFingerprint) || !fingerprint(pin.SchemaFingerprint) {
		return fmt.Errorf("incomplete exact component pin")
	}
	if s.components == nil || !s.components.IsComponentProducer(backend.Service) {
		return fmt.Errorf("component producer exact transport unavailable")
	}
	observed, err := s.components.ObserveComponent(ctx, backend.Service, backend.Method, *pin)
	if err != nil {
		return err
	}
	return windowprotocol.ValidateComponentDispatch(pin, observed)
}
