package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	windowprotocol "github.com/viant/agently-core/protocol/window"
	identity "github.com/viant/agently-core/protocol/resource"
	schema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

const componentMetaKey = "viant.datly/component"

type expectedComponentKey struct{}

func (r *Registry) SetComponentProducerClassifier(classifier func(string) bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.componentProducer = classifier
}
func (r *Registry) SetComponentAuthorityResolver(resolve func(context.Context) (gating.Principal, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.componentAuthority = resolve
}
func (r *Registry) componentRequired(server string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.componentProducer != nil && r.componentProducer(server)
}

// ObserveNativeComponent fetches actual producer metadata under the current
// MCP credential. It does not read a cached LLM definition or infer a revision.
func (r *Registry) ObserveNativeComponent(ctx context.Context, server, method string) (windowprotocol.ComponentBinding, error) {
	var cli mcpclient.Interface
	if r.mgr != nil {
		ctx = r.mgr.WithAuthTokenContext(ctx, server)
	}
	r.mu.RLock()
	internal := r.internal[server]
	r.mu.RUnlock()
	if c := internal; c != nil {
		cli = c
	} else {
		if r.mgr == nil {
			return windowprotocol.ComponentBinding{}, fmt.Errorf("native producer unavailable")
		}
		var err error
		cli, err = r.mgr.Get(ctx, requestctx.ConversationIDFromContext(ctx), server)
		if err != nil {
			return windowprotocol.ComponentBinding{}, err
		}
	}
	options := []mcpclient.RequestOption{mcpclient.WithNoRetry()}
	useID := r.mgr != nil && r.mgr.UseIDToken(ctx, server)
	if token := authctx.MCPAuthToken(ctx, useID); token != "" && !r.isDelegatedAuthServer(ctx, server) {
		options = append(options, mcpclient.WithAuthToken(token))
	}
	var cursor *string
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		listed, err := cli.ListTools(ctx, cursor, options...)
		if err != nil || listed == nil {
			return windowprotocol.ComponentBinding{}, fmt.Errorf("native component observation unavailable")
		}
		for _, tool := range listed.Tools {
			if tool.Name != method {
				continue
			}
			raw, err := json.Marshal(tool)
			if err != nil {
				return windowprotocol.ComponentBinding{}, err
			}
			var envelope struct {
				Meta map[string]json.RawMessage `json:"_meta"`
			}
			if json.Unmarshal(raw, &envelope) != nil || envelope.Meta[componentMetaKey] == nil {
				return windowprotocol.ComponentBinding{}, fmt.Errorf("producer did not declare exact component")
			}
			var binding windowprotocol.ComponentBinding
			if json.Unmarshal(envelope.Meta[componentMetaKey], &binding) != nil || completeComponentPin(binding) != nil {
				return windowprotocol.ComponentBinding{}, fmt.Errorf("producer component metadata invalid")
			}
			return binding, nil
		}
		if listed.NextCursor == nil || *listed.NextCursor == "" {
			break
		}
		if seen[*listed.NextCursor] {
			break
		}
		seen[*listed.NextCursor] = true
		cursor = listed.NextCursor
	}
	return windowprotocol.ComponentBinding{}, fmt.Errorf("native component tool unavailable")
}
func completeComponentPin(pin windowprotocol.ComponentBinding) error {
	if !(identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: pin.ContentFingerprint}).Valid() || !(identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: pin.SchemaFingerprint}).Valid() || pin.ID == "" || pin.Revision == "" || pin.Revision == "working" || pin.Revision == "active" || pin.Revision == "latest" || (pin.Kind != "linked" && pin.Kind != "dynamic") {
		return fmt.Errorf("complete component binding required")
	}
	return windowprotocol.ValidateComponentDispatch(&pin, pin)
}

// ExecuteNativeComponent retains normal registry action admission and carries
// the expected binding in the producer-supported request _meta, not arguments.
func (r *Registry) ExecuteNativeComponent(ctx context.Context, server, method string, pin windowprotocol.ComponentBinding, args map[string]interface{}) (json.RawMessage, error) {
	if err := completeComponentPin(pin); err != nil {
		return nil, err
	}
	out, err := r.Execute(context.WithValue(ctx, expectedComponentKey{}, pin), server+":"+method, args)
	if err != nil {
		return nil, err
	}
	if !json.Valid([]byte(out)) {
		return nil, fmt.Errorf("native producer returned invalid JSON")
	}
	return json.RawMessage(out), nil
}
func componentRequestMeta(pin windowprotocol.ComponentBinding) (schema.RequestMetaObject, error) {
	var meta schema.RequestMetaObject
	// The compatibility client uses this dialect. The stateless July client
	// replaces protocol metadata with its configured transport version and
	// capabilities in withProtocolMeta before sending the request.
	raw, err := json.Marshal(map[string]interface{}{
		"io.modelcontextprotocol/protocolVersion": schema.LegacyProtocolVersion,
		componentMetaKey: pin,
	})
	if err != nil {
		return meta, err
	}
	if err = json.Unmarshal(raw, &meta); err != nil {
		return meta, err
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return meta, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &fields)
	var preserved windowprotocol.ComponentBinding
	if json.Unmarshal(fields[componentMetaKey], &preserved) != nil || !reflect.DeepEqual(pin, preserved) {
		return meta, fmt.Errorf("MCP request metadata carrier cannot preserve exact component binding")
	}
	return meta, nil
}
func (r *Registry) componentAuthorityBefore(ctx context.Context) (gating.Principal, error) {
	if err := ctx.Err(); err != nil {
		return gating.Principal{}, err
	}
	r.mu.RLock()
	resolve := r.componentAuthority
	r.mu.RUnlock()
	if resolve == nil {
		return gating.Principal{}, fmt.Errorf("native component authority source unavailable")
	}
	principal, err := resolve(ctx)
	if err != nil || principal.Facts.Subject == "" || principal.AccountID == "" || principal.IdentityRevision == "" || !principal.Facts.ValidUntil.After(time.Now()) {
		return gating.Principal{}, fmt.Errorf("native component identity denied")
	}
	// A resolver may reuse slices/maps. Preserve the admitted value independently
	// so an in-place role or verified-context change cannot alter both snapshots.
	snapshot := principal
	snapshot.MembershipGroups = slices.Clone(principal.MembershipGroups)
	snapshot.Facts.Roles = slices.Clone(principal.Facts.Roles)
	snapshot.Facts.Exposures = slices.Clone(principal.Facts.Exposures)
	snapshot.Facts.GrantedScopes = slices.Clone(principal.Facts.GrantedScopes)
	snapshot.Facts.Entities = slices.Clone(principal.Facts.Entities)
	snapshot.Facts.EntityPermissions = slices.Clone(principal.Facts.EntityPermissions)
	for i := range snapshot.Facts.EntityPermissions {
		snapshot.Facts.EntityPermissions[i].Permissions = slices.Clone(principal.Facts.EntityPermissions[i].Permissions)
	}
	if principal.Facts.EntityGroups != nil {
		snapshot.Facts.EntityGroups = make(authz.EntityGroups, len(principal.Facts.EntityGroups))
		for kind, ids := range principal.Facts.EntityGroups {
			snapshot.Facts.EntityGroups[kind] = slices.Clone(ids)
		}
	}
	return snapshot, nil
}
func (r *Registry) componentAuthorityAfter(ctx context.Context, initial gating.Principal) error {
	current, err := r.componentAuthorityBefore(ctx)
	if err != nil || !initial.Facts.ValidUntil.After(time.Now()) || !gating.SamePrincipalAuthority(initial, current) {
		return fmt.Errorf("native component authority changed")
	}
	return nil
}
