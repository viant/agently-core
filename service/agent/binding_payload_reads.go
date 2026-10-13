package agent

import (
	"context"
	"sync"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
)

type bindingPayloadReadsKey struct{}
type bindingPayloadRead struct {
	done    chan struct{}
	payload *apiconv.Payload
	err     error
}
type bindingPayloadReads struct {
	owner     *Service
	principal [2]string
	active    bool
	mu        sync.Mutex
	reads     map[string]*bindingPayloadRead
}

// A fresh scope is installed for every BuildBinding, including nested calls.
// It shares only successful payload reads during this single prompt assembly;
// it neither caches authority nor changes tool invocation or result identity.
func (s *Service) withBindingPayloadReads(ctx context.Context) context.Context {
	return context.WithValue(ctx, bindingPayloadReadsKey{}, &bindingPayloadReads{owner: s, principal: bindingPayloadPrincipal(ctx), active: true, reads: map[string]*bindingPayloadRead{}})
}
func bindingPayloadPrincipal(ctx context.Context) [2]string {
	return [2]string{authctx.CanonicalUserID(ctx), authctx.EffectiveUserID(ctx)}
}
func closeBindingPayloadReads(ctx context.Context) {
	if scope, ok := ctx.Value(bindingPayloadReadsKey{}).(*bindingPayloadReads); ok {
		scope.mu.Lock()
		scope.active = false
		scope.reads = nil
		scope.mu.Unlock()
	}
}
func (s *Service) bindingPayload(ctx context.Context, id string) (*apiconv.Payload, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scope, _ := ctx.Value(bindingPayloadReadsKey{}).(*bindingPayloadReads)
	if scope == nil || scope.owner != s || scope.principal != bindingPayloadPrincipal(ctx) {
		return s.conversation.GetPayload(ctx, id)
	}
	scope.mu.Lock()
	if !scope.active {
		scope.mu.Unlock()
		return s.conversation.GetPayload(ctx, id)
	}
	if read := scope.reads[id]; read != nil {
		scope.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-read.done:
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return cloneBindingPayload(read.payload), read.err
	}
	read := &bindingPayloadRead{done: make(chan struct{})}
	scope.reads[id] = read
	scope.mu.Unlock()
	payload, err := s.conversation.GetPayload(ctx, id)
	if ctx.Err() != nil {
		payload = nil
		err = ctx.Err()
	}
	scope.mu.Lock()
	read.payload = cloneBindingPayload(payload)
	read.err = err
	if err != nil || payload == nil || payload.Id != id {
		delete(scope.reads, id)
	}
	close(read.done)
	scope.mu.Unlock()
	return cloneBindingPayload(read.payload), read.err
}
func cloneBindingField[T any](v *T) *T {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}
func cloneBindingPayload(p *apiconv.Payload) *apiconv.Payload {
	if p == nil {
		return nil
	}
	copy := *p
	copy.TenantID = cloneBindingField(p.TenantID)
	copy.Subtype = cloneBindingField(p.Subtype)
	copy.Digest = cloneBindingField(p.Digest)
	copy.URI = cloneBindingField(p.URI)
	copy.EncryptionKMSKeyID = cloneBindingField(p.EncryptionKMSKeyID)
	copy.RedactionPolicyVersion = cloneBindingField(p.RedactionPolicyVersion)
	copy.Redacted = cloneBindingField(p.Redacted)
	copy.CreatedAt = cloneBindingField(p.CreatedAt)
	copy.SchemaRef = cloneBindingField(p.SchemaRef)
	if p.InlineBody != nil {
		body := append([]byte(nil), (*p.InlineBody)...)
		copy.InlineBody = &body
	}
	return &copy
}
