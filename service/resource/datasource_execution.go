package resource

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	windowprotocol "github.com/viant/agently-core/protocol/window"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

func datasourceDefinition(raw json.RawMessage) (*dsproto.DataSource, error) {
	var envelope struct {
		SchemaVersion int64           `json:"schemaVersion"`
		DataSource    json.RawMessage `json:"dataSource"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || decoder.Decode(new(any)) != io.EOF || envelope.SchemaVersion != 1 {
		return nil, identity.ErrResourceDenied
	}
	canonical, err := types.CanonicalWindowDescriptor(envelope.DataSource)
	if err != nil {
		return nil, err
	}
	var descriptor dsproto.DataSource
	if json.Unmarshal(canonical, &descriptor) != nil || descriptor.ID == "" {
		return nil, identity.ErrResourceDenied
	}
	return &descriptor, nil
}
func sameDatasourceDescriptor(expected, actual *dsproto.DataSource) bool {
	if expected == nil || actual == nil {
		return false
	}
	// A parent attachment can assign a local alias. Every other descriptor
	// field is exact, and dispatch always uses the provider's canonical ID.
	copy := *actual
	copy.ID = expected.ID
	a, aErr := json.Marshal(expected)
	b, bErr := json.Marshal(&copy)
	if aErr != nil || bErr != nil {
		return false
	}
	aFP, aErr := types.WindowDescriptorFingerprint(a)
	bFP, bErr := types.WindowDescriptorFingerprint(b)
	return aErr == nil && bErr == nil && aFP == bFP
}

// ResolveDatasource reads the exact original standalone candidate. The proof
// remains opaque provider provenance and never replaces authorization or the
// consumer's original deadline.
func (g *Gateway) ResolveDatasource(ctx context.Context, pin identity.ResolvedResource) (*dsproto.DataSource, *primitive.ExecutionProof, error) {
	uri, err := identity.ParseResourceURI(pin.URI)
	if err != nil || uri.Kind != "datasource" {
		return nil, nil, identity.ErrResourceDenied
	}
	connection, err := g.ConnectionForProvider(ctx, pin.ProviderIdentity)
	if err != nil {
		return nil, nil, err
	}
	result, err := g.Get(ctx, connection, identity.ResourceRef{URI: pin.URI, Revision: pin.Selector()}, &pin)
	if err != nil {
		return nil, nil, err
	}
	if result == nil || result.Resource == nil || result.ResolvedResource == nil || result.ResolvedResource.ValidUntil.Before(pin.ValidUntil) {
		return nil, nil, identity.ErrResourceDenied
	}
	descriptor, err := datasourceDefinition(result.Resource.Definition)
	if err != nil {
		return nil, nil, err
	}
	if descriptor.Backend != nil && descriptor.Backend.Kind == dsproto.BackendDatly && descriptor.Backend.Ownership == "provider" {
		if err = matchProviderProof(pin, result.ExecutionProof); err != nil {
			return nil, nil, err
		}
	}
	return descriptor, result.ExecutionProof, nil
}

// FetchDatasource executes one provider-owned standalone datasource through
// the advertised owning connection. Pinned reads, descriptor/component checks
// and original leases run before and after dispatch; no generic fallback exists.
func (g *Gateway) FetchDatasource(ctx context.Context, pin identity.ResolvedResource, proof *primitive.ExecutionProof, expected *dsproto.DataSource, inputs map[string]interface{}) (json.RawMessage, error) {
	if proof != nil {
		copy := *proof
		proof = &copy
	}
	uri, err := identity.ParseResourceURI(pin.URI)
	if err != nil || uri.Kind != "datasource" || expected == nil || expected.Backend == nil || expected.Backend.Kind != dsproto.BackendDatly || expected.Backend.Ownership != "provider" || expected.Backend.Service != "" || expected.Backend.Component == nil {
		return nil, identity.ErrResourceDenied
	}
	if err = matchProviderProof(pin, proof); err != nil {
		return nil, err
	}
	actor, err := g.actor(ctx)
	if err != nil {
		return nil, err
	}
	deadline := pin.ValidUntil
	if proof.Resource.ValidUntil.Before(deadline) {
		deadline = proof.Resource.ValidUntil
	}
	if actor.ValidUntil.Before(deadline) {
		deadline = actor.ValidUntil
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	connection, err := g.ConnectionForProvider(ctx, pin.ProviderIdentity)
	if err != nil {
		return nil, err
	}
	check := func() (*dsproto.DataSource, error) {
		actual, _, err := g.ResolveDatasource(ctx, pin)
		if err != nil {
			return nil, err
		}
		if !sameDatasourceDescriptor(expected, actual) || actual.Backend == nil || actual.Backend.Component == nil {
			return nil, identity.ErrResourceStale
		}
		if err = windowprotocol.ValidateComponentDispatch(expected.Backend.Component, *actual.Backend.Component); err != nil {
			return nil, err
		}
		if err = matchProviderProof(pin, proof); err != nil {
			return nil, err
		}
		return actual, nil
	}
	actual, err := check()
	if err != nil {
		return nil, err
	}
	caps, err := g.discover(ctx, actor, connection.Name)
	if err != nil {
		return nil, err
	}
	method := operationMethod(caps, uri.Namespace, "datasource", "fetch")
	if caps.connection != connection || method == "" || method != expected.Backend.Method || method != actual.Backend.Method {
		return nil, identity.ErrResourceDenied
	}
	client, err := g.client(ctx, connection.Name)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(struct {
		Resource       identity.ResolvedResource `json:"resource"`
		ExecutionProof *primitive.ExecutionProof `json:"executionProof"`
		DataSourceID   string                    `json:"dataSourceId"`
		Inputs         map[string]interface{}    `json:"inputs"`
	}{pin, proof, actual.ID, inputs})
	if err != nil {
		return nil, err
	}
	var args map[string]interface{}
	if json.Unmarshal(raw, &args) != nil {
		return nil, identity.ErrResourceDenied
	}
	result, err := client.CallTool(ctx, &schema.CallToolRequestParams{Name: method, Arguments: args}, mcpclient.WithNoRetry())
	if err != nil {
		return nil, err
	}
	if result == nil || result.IsError != nil && *result.IsError {
		return nil, identity.ErrResourceDenied
	}
	var body json.RawMessage
	if result.StructuredContent != nil {
		body, err = json.Marshal(result.StructuredContent)
	} else {
		for _, content := range result.Content {
			if text, ok := content.(schema.TextContent); ok && json.Valid([]byte(text.Text)) {
				body = json.RawMessage(text.Text)
				break
			}
		}
	}
	if err != nil || len(body) == 0 {
		return nil, identity.ErrResourceDenied
	}
	if _, err = check(); err != nil {
		return nil, err
	}
	if err = g.final(ctx, actor); err != nil {
		return nil, err
	}
	if ctx.Err() != nil || !actor.Valid(g.now()) || !pin.ValidUntil.After(g.now()) || !proof.Resource.ValidUntil.After(time.Now()) {
		return nil, identity.ErrResourceDenied
	}
	return body, nil
}
