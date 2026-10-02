package policy

import (
	"context"
	"fmt"
	"github.com/viant/authz"
	"strings"
	"time"
)

// AuthzResolver uses the shared resource/role/exposure/entity model without
// importing Datly or Studio. The deployment owns candidate-to-resource mapping
// and trusted fact resolution. Request.Context is never an authority source.
type AuthzResolver struct {
	Service       *authz.Service
	PolicyVersion string
	Resource      func(context.Context, string, Candidate) (authz.Resource, string, error)
}

func (r *AuthzResolver) Resolve(ctx context.Context, request *Request) (*Decision, error) {
	if r == nil || r.Service == nil || r.Service.Provider == nil || r.Resource == nil || strings.TrimSpace(r.PolicyVersion) == "" || request == nil || ctx.Err() != nil {
		return nil, ErrDenied
	}
	facts, err := r.Service.Provider.Resolve(ctx)
	if err != nil || facts.Subject == "" || facts.Tenant == "" || facts.Issuer == "" || !facts.ValidUntil.After(time.Now()) {
		return nil, ErrDenied
	}
	result := &Decision{PolicyVersion: r.PolicyVersion, ExpiresAt: facts.ValidUntil, AllowedIDs: []string{}}
	seen := map[string]bool{}
	for _, candidate := range request.Candidates {
		if candidate.ID == "" || seen[candidate.ID] {
			return nil, fmt.Errorf("authorization candidates must have distinct nonempty IDs")
		}
		seen[candidate.ID] = true
		resource, action, err := r.Resource(ctx, request.Operation, candidate)
		if err != nil {
			return nil, err
		}
		if resource.ID != candidate.ID {
			return nil, ErrDenied
		}
		decision, current, err := r.Service.AuthorizeWithFacts(ctx, authz.Request{Resource: resource, Action: action})
		if err != nil {
			continue
		}
		// This consumer authorizes whole candidate resources, not data rows. Never
		// discard entity bounds to turn a scoped grant into whole-resource access.
		if decision.Bounded {
			continue
		}
		if current.Subject != "" {
			if current.Subject != facts.Subject || current.Tenant != facts.Tenant || current.Issuer != facts.Issuer || !current.ValidUntil.After(time.Now()) {
				return nil, ErrDenied
			}
			if current.ValidUntil.Before(result.ExpiresAt) {
				result.ExpiresAt = current.ValidUntil
			}
		}
		result.AllowedIDs = append(result.AllowedIDs, candidate.ID)
	}
	if ctx.Err() != nil || !result.ExpiresAt.After(time.Now()) {
		return nil, ErrDenied
	}
	result.Allow = len(result.AllowedIDs) > 0
	return result, nil
}
