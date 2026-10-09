package resource

import (
	"context"
	"encoding/base64"
	"encoding/json"
	identity "github.com/viant/agently-core/protocol/resource"
	service "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/forge/backend/types"
	"strings"
	"time"
)

// WindowAdmission is an explicit host intersection with delegated provider
// authorization. It receives only the server-read exact definition and pin.
type WindowAdmission func(context.Context, identity.ResolvedResource, *types.Window) error
type WindowCatalog struct {
	Gateway     *Gateway
	Admission   WindowAdmission
	TargetProof types.WindowTargetProof
}
type windowLocator struct {
	Connection Connection `json:"connection"`
	URI        string     `json:"uri"`
}

const locatorPrefix = "primitive:"

func encodeWindowLocator(connection Connection, uri string) string {
	raw, _ := json.Marshal(windowLocator{connection, uri})
	return locatorPrefix + base64.RawURLEncoding.EncodeToString(raw)
}
func decodeWindowLocator(value string) (windowLocator, error) {
	var out windowLocator
	if !strings.HasPrefix(value, locatorPrefix) || len(value) > 4096 {
		return out, identity.ErrResource
	}
	raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, locatorPrefix))
	if e != nil || json.Unmarshal(raw, &out) != nil {
		return out, identity.ErrResource
	}
	uri, e := identity.ParseResourceURI(out.URI)
	if e != nil || uri.Kind != "window" || out.Connection.Name == "" || out.Connection.ProviderIdentity == "" {
		return out, identity.ErrResource
	}
	return out, nil
}
func (c *WindowCatalog) AuthzReady() bool {
	return c != nil && c.Gateway != nil && c.Admission != nil && c.TargetProof != nil
}
func (c *WindowCatalog) UsesResourceResolution() bool { return true }
func (c *WindowCatalog) List(ctx context.Context, input *service.WindowDefinitionListInput) (*service.WindowDefinitionListOutput, error) {
	if !c.AuthzReady() {
		return nil, identity.ErrResourceDenied
	}
	if input == nil {
		input = &service.WindowDefinitionListInput{}
	}
	if input.Offset < 0 || input.Limit < 0 || input.Limit > 100 {
		return nil, identity.ErrResource
	}
	rows, e := c.Gateway.List(ctx, "window", "")
	if e != nil {
		return nil, e
	}
	summaries := []service.WindowDefinitionSummary{}
	for _, row := range rows {
		if input.Query != "" && !strings.Contains(strings.ToLower(row.Resource.Title+" "+row.Resource.Name+" "+row.Resource.URI), strings.ToLower(input.Query)) {
			continue
		}
		// List is metadata only. Provider list admission decides visibility; opening
		// always intersects the exact selected definition with host admission.
		summaries = append(summaries, service.WindowDefinitionSummary{ProviderIdentity: row.Connection.ProviderIdentity, ResourceURI: row.Resource.URI, Name: row.Resource.Name, WindowID: encodeWindowLocator(row.Connection, row.Resource.URI), Title: row.Resource.Title, Namespace: row.Resource.Namespace})
	}
	offset := input.Offset
	if offset > len(summaries) {
		offset = len(summaries)
	}
	limit := input.Limit
	if limit == 0 {
		limit = 100
	}
	end := offset + limit
	if end > len(summaries) {
		end = len(summaries)
	}
	return &service.WindowDefinitionListOutput{Windows: summaries[offset:end], HasMore: end < len(summaries)}, nil
}
func (c *WindowCatalog) locate(ctx context.Context, key, owner string) (windowLocator, error) {
	if strings.HasPrefix(key, locatorPrefix) {
		return decodeWindowLocator(key)
	}
	uri, e := identity.ParseResourceURI(key)
	if e != nil || uri.Kind != "window" {
		return windowLocator{}, identity.ErrResource
	}
	connection, e := c.Gateway.ConnectionForResource(ctx, uri, owner)
	if e != nil {
		return windowLocator{}, e
	}
	return windowLocator{connection, key}, nil
}
func (c *WindowCatalog) ResourceReference(ctx context.Context, key string) (identity.ResourceRef, error) {
	loc, e := c.locate(ctx, key, "")
	if e != nil {
		return identity.ResourceRef{}, e
	}
	return identity.ResourceRef{URI: loc.URI}, nil
}
func (c *WindowCatalog) Get(ctx context.Context, input *service.WindowDefinitionGetInput) (*service.WindowDefinitionGetOutput, error) {
	if !c.AuthzReady() || input == nil {
		return nil, identity.ErrResourceDenied
	}
	owner := ""
	if input.ResolvedResource != nil {
		owner = input.ResolvedResource.ProviderIdentity
	}
	loc, e := c.locate(ctx, input.WindowID, owner)
	if e != nil {
		return nil, e
	}
	ref := identity.ResourceRef{URI: loc.URI}
	if input.Resource != nil {
		if input.Resource.URI != loc.URI {
			return nil, identity.ErrResourceDenied
		}
		ref = *input.Resource
	}
	result, e := c.Gateway.Get(ctx, loc.Connection, ref, input.ResolvedResource)
	if e != nil {
		return nil, e
	}
	selected, e := types.SelectWindowResourceWithReferences(result.Resource.Definition, input.Target)
	if e != nil || selected == nil {
		return nil, identity.ErrResourceDenied
	}
	pin := *result.ResolvedResource
	definition := selected.Window
	var dependencyPins map[string]identity.ResolvedResource
	if len(selected.DataSourceResources) > 0 {
		var existing map[string]identity.ResolvedResource
		if input.Target != nil && input.Target.SelectionToken != "" {
			if e = c.TargetProof.Verify(ctx, pin, *input.Target, selected.Fingerprint, input.Target.SelectionToken); e != nil {
				return nil, e
			}
			existing = input.Target.DependencyPins
			if existing == nil {
				return nil, identity.ErrResourceDenied
			}
		} else if input.Target != nil && len(input.Target.DependencyPins) > 0 {
			return nil, identity.ErrResourceDenied
		}
		dependencyPins, e = c.Gateway.ResolveDependencyPins(ctx, loc.Connection, pin, selected.DataSourceResources, selected.DataSources, existing)
		if e != nil {
			return nil, e
		}
		for _, child := range dependencyPins {
			if child.ValidUntil.Before(pin.ValidUntil) {
				pin.ValidUntil = child.ValidUntil
			}
		}
	} else if input.Target != nil && len(input.Target.DependencyPins) > 0 {
		return nil, identity.ErrResourceDenied
	}
	if e = c.Admission(ctx, pin, definition); e != nil {
		return nil, e
	}
	target := types.WindowTarget{}
	if input.Target != nil {
		target = *input.Target
	}
	target.DependencyPins = dependencyPins
	target, e = target.Normalize()
	if e != nil {
		return nil, e
	}
	// No source fallback: only the exact provider bytes above enter the window.
	final, e := c.Gateway.Get(ctx, loc.Connection, identity.ResourceRef{URI: loc.URI}, &pin)
	if e != nil {
		return nil, e
	}
	if e = c.Admission(ctx, pin, definition); e != nil {
		return nil, e
	}
	if len(dependencyPins) > 0 {
		freshChildren, err := c.Gateway.ResolveDependencyPins(ctx, loc.Connection, pin, selected.DataSourceResources, selected.DataSources, dependencyPins)
		if err != nil {
			return nil, err
		}
		for _, child := range freshChildren {
			if child.ValidUntil.Before(pin.ValidUntil) {
				pin.ValidUntil = child.ValidUntil
			}
		}
	}
	if final.ResolvedResource.ValidUntil.Before(pin.ValidUntil) {
		pin.ValidUntil = final.ResolvedResource.ValidUntil
	}
	if !pin.ValidUntil.After(time.Now()) {
		return nil, identity.ErrResourceDenied
	}
	if target.SelectionToken != "" {
		if e = c.TargetProof.Verify(ctx, pin, target, selected.Fingerprint, target.SelectionToken); e != nil {
			return nil, e
		}
	} else {
		target.SelectionToken, e = c.TargetProof.Sign(ctx, pin, target, selected.Fingerprint)
		if e != nil {
			return nil, e
		}
	}
	definition.Resource = &pin
	definition.ResourceTarget = &target
	return &service.WindowDefinitionGetOutput{WindowID: input.WindowID, Definition: definition}, nil
}
func (c *WindowCatalog) CheckWindowAdmission(ctx context.Context, key string) (bool, error) {
	_, e := c.Get(ctx, &service.WindowDefinitionGetInput{WindowID: key})
	return e == nil, e
}
func (c *WindowCatalog) VerifyWindowTarget(ctx context.Context, pin identity.ResolvedResource, target types.WindowTarget, variant string) error {
	if !c.AuthzReady() {
		return identity.ErrResourceDenied
	}
	return c.TargetProof.Verify(ctx, pin, target, variant, target.SelectionToken)
}
