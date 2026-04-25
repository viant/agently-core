package status

import (
	"context"
	"fmt"
	"strings"
	"sync"

	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// NarrationPairing keeps the one parked-status-call -> one interim assistant
// message mapping used by backend-authored assistant_preamble updates.
type NarrationPairing struct {
	svc *Service
	mu  sync.Mutex
	ids map[string]string
}

func NewNarrationPairing(svc *Service) *NarrationPairing {
	return &NarrationPairing{
		svc: svc,
		ids: map[string]string{},
	}
}

func (p *NarrationPairing) Upsert(ctx context.Context, parkedToolCallID string, parent runtimerequestctx.TurnMeta, toolName, role, actor, mode, preamble string) (string, error) {
	if p == nil || p.svc == nil {
		return "", fmt.Errorf("status: preamble pairing not configured")
	}
	parkedToolCallID = strings.TrimSpace(parkedToolCallID)
	if parkedToolCallID == "" {
		return "", fmt.Errorf("status: empty parkedToolCallID")
	}
	preamble = strings.TrimSpace(preamble)
	if preamble == "" {
		return "", nil
	}

	p.mu.Lock()
	msgID := strings.TrimSpace(p.ids[parkedToolCallID])
	p.mu.Unlock()

	if msgID == "" {
		created, err := p.svc.StartNarration(ctx, parent, toolName, role, actor, mode, preamble)
		if err != nil {
			return "", err
		}
		p.mu.Lock()
		p.ids[parkedToolCallID] = strings.TrimSpace(created)
		p.mu.Unlock()
		return strings.TrimSpace(created), nil
	}

	if err := p.svc.UpdateNarration(ctx, parent, msgID, preamble); err != nil {
		return "", err
	}
	return msgID, nil
}

func (p *NarrationPairing) MessageID(parkedToolCallID string) string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.TrimSpace(p.ids[strings.TrimSpace(parkedToolCallID)])
}

func (p *NarrationPairing) Release(parkedToolCallID string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.ids, strings.TrimSpace(parkedToolCallID))
}
