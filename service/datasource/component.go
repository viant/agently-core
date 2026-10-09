package datasource

import (
	"context"
	"fmt"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	windowprotocol "github.com/viant/agently-core/protocol/window"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"strings"
)

func (s *Service) validateComponentSource(ctx context.Context, ds *dsproto.DataSource) error {
	if ds == nil || ds.Backend == nil {
		return nil
	}
	backend := ds.Backend
	providerOwned := backend.Kind == dsproto.BackendDatly && backend.Ownership == "provider"
	if (backend.Kind == dsproto.BackendDatly || backend.Ownership == "provider") && !providerOwned {
		return identity.ErrResourceDenied
	}
	if providerOwned && (s.providerExecute == nil || backend.Service != "" || backend.Method == "" || backend.Component == nil) {
		return identity.ErrResourceDenied
	}
	if providerOwned {
		pin, ok := requestctx.ResolvedResourceFromContext(ctx)
		target, targetOK := requestctx.WindowTargetFromContext(ctx)
		if !ok || !targetOK || !strings.HasPrefix(pin.URI, "window://") || target.SelectionToken == "" || target.ExecutionProof == nil {
			return identity.ErrResourceDenied
		}
	}
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
	if (!providerOwned && backend.Kind != dsproto.BackendMCPTool) || backend.ProducerKind == "mcp" {
		return fmt.Errorf("component source cannot downgrade to generic backend")
	}
	pin := backend.Component
	fingerprint := func(value string) bool {
		return (identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: value}).Valid()
	}
	if pin.ID == "" || strings.TrimSpace(pin.ID) != pin.ID || pin.Revision == "" || strings.TrimSpace(pin.Revision) != pin.Revision || pin.Revision == "active" || pin.Revision == "latest" || pin.Revision == "working" || (pin.Kind != "linked" && pin.Kind != "dynamic") || !fingerprint(pin.ContentFingerprint) || !fingerprint(pin.SchemaFingerprint) {
		return fmt.Errorf("incomplete exact component pin")
	}
	if providerOwned {
		return nil
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
