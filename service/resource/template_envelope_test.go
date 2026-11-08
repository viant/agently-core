package resource

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	"github.com/viant/authz"
	cohort "github.com/viant/forge/backend/artifactcohort"
)

type templateEnvelopeCaller struct {
	publisher *cohort.Publisher
	fetches   int
}

func (c *templateEnvelopeCaller) Discover(context.Context) (cohort.Manifest, error) {
	return c.publisher.Snapshot()
}
func (c *templateEnvelopeCaller) Fetch(_ context.Context, input cohort.FetchInput) (*cohort.Artifact, error) {
	c.fetches++
	return c.publisher.Get(input.Root)
}

type templateEnvelopeFacts struct{}

func (templateEnvelopeFacts) Resolve(ctx context.Context) (authz.Facts, error) {
	actor, ok := ctx.Value(authoredActorKey{}).(identity.VerifiedActor)
	if !ok {
		return authz.Facts{}, authz.ErrDenied
	}
	roles := []string{"other"}
	if actor.Subject == "alice" {
		roles = []string{"reader"}
	}
	return authz.Facts{Issuer: actor.Issuer, Subject: actor.Subject, Tenant: actor.TenantID, Roles: roles, ValidUntil: actor.ValidUntil}, nil
}
func TestTemplateRepositoryConsumesPublishedEnvelopeAndLegacyShape(t *testing.T) {
	const flat = `{"id":"original-output","name":"Original Output","description":"Synthetic output contract","format":"json","appliesTo":["review"],"platforms":["web"],"formFactors":["desktop"],"surfaces":["chat"],"instructions":"Preserve the supplied evidence.","schema":{"opaqueId":9007199254740993},"fences":[{"lang":"json","required":true,"schema":{"type":"object"}}],"examples":[{"title":"Synthetic example","fences":[{"lang":"json","body":{"status":"ready"}}]}]}`
	for _, wrapped := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "envelope"}[wrapped], func(t *testing.T) {
			ctx := authoredActorContext("alice")
			raw := json.RawMessage(flat)
			if wrapped {
				raw = json.RawMessage(`{"schemaVersion":1,"template":` + flat + `}`)
			}
			publisher := cohort.NewPublisher()
			artifact, _, err := publisher.Publish(ctx, cohort.RootRef{URI: "template://example/output", Kind: "template", Version: 1}, raw, func(context.Context, json.RawMessage) error { return nil })
			require.NoError(t, err)
			policy := authz.Resource{Tenant: "tenant", Kind: "policy", ID: "template-readers", Version: "logical"}
			_, err = publisher.ReplaceCatalog(ctx, []authz.Document{{Resource: policy, Revision: 1, Policies: map[string]authz.Policy{cohort.PolicyAction: {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}, []cohort.Cohort{{Tenant: "tenant", Name: "readers", PolicyResource: policy, Roots: []cohort.RootRef{artifact.Root}}}, publisher.Generation())
			require.NoError(t, err)
			caller := &templateEnvelopeCaller{publisher: publisher}
			replica, err := cohort.NewReplica(cohort.Config{Enabled: true, Source: "template-producer", Tenant: "tenant", Account: "account", Caller: caller, Facts: templateEnvelopeFacts{}, SessionKey: func(ctx context.Context) (string, error) {
				actor, ok := ctx.Value(authoredActorKey{}).(identity.VerifiedActor)
				if !ok {
					return "", authz.ErrDenied
				}
				return actor.Subject, nil
			}})
			require.NoError(t, err)
			require.NoError(t, replica.Sync(ctx))
			source, err := cohort.NewMetadataSource(replica, []string{"example"})
			require.NoError(t, err)
			owner, err := manager.New(nil)
			require.NoError(t, err)
			actor := func(ctx context.Context) (identity.VerifiedActor, error) {
				value, ok := ctx.Value(authoredActorKey{}).(identity.VerifiedActor)
				if !ok {
					return value, identity.ErrResourceDenied
				}
				return value, nil
			}
			verify := func(ctx context.Context, expected identity.VerifiedActor) error {
				current, err := actor(ctx)
				if err != nil || current.Subject != expected.Subject {
					return identity.ErrResourceDenied
				}
				return nil
			}
			gateway := NewGateway(owner, actor, verify, "host")
			defer gateway.Close()
			require.NoError(t, gateway.RegisterPublishedMetadataSource(Connection{Name: "publication", ProviderIdentity: "template-producer"}, []string{"example"}, source))
			repo := tplrepo.NewWithReader(NewTemplateReader(gateway))
			listed, err := repo.LoadAll(ctx)
			require.NoError(t, err)
			require.Len(t, listed, 1)
			require.Equal(t, "original-output", listed[0].ID)
			require.Equal(t, "Original Output", listed[0].Name)
			require.Equal(t, "template://example/output", listed[0].ResourceURI)
			require.Empty(t, listed[0].Instructions)
			require.Nil(t, listed[0].Schema)
			require.Equal(t, []string{"review"}, listed[0].AppliesTo)
			require.Equal(t, []string{"web"}, listed[0].Platforms)
			require.Equal(t, []string{"desktop"}, listed[0].FormFactors)
			require.Equal(t, []string{"chat"}, listed[0].Surfaces)
			for _, key := range []string{"original-output", "Original Output", "template://example/output"} {
				got, err := repo.Load(ctx, key)
				require.NoError(t, err)
				require.Equal(t, "original-output", got.ID)
				require.Equal(t, "template://example/output", got.ResourceURI)
				require.Equal(t, "Preserve the supplied evidence.", got.Instructions)
				require.Equal(t, json.Number("9007199254740993"), got.Schema["opaqueId"])
				require.Len(t, got.Fences, 1)
				require.True(t, got.Fences[0].Required)
				require.Len(t, got.Examples, 1)
			}
			hidden, err := repo.LoadAll(authoredActorContext("bob"))
			require.NoError(t, err)
			require.Empty(t, hidden)
			denied, err := repo.Load(authoredActorContext("bob"), "template://example/output")
			require.Error(t, err)
			require.Nil(t, denied)
			require.Equal(t, 1, caller.fetches, "ordinary list/get reuse the existing local immutable publication")
		})
	}
}

func TestTemplateEnvelopeDecodePreservesFlatBundleMetadataAndRejectsInvalidEnvelope(t *testing.T) {
	flat := json.RawMessage(`{"id":"original","name":"Original","templateBundles":[{"id":"outputs","templates":["original"]}]}`)
	definition, err := DecodeTemplateDefinition(flat)
	require.NoError(t, err)
	require.Len(t, definition.TemplateBundles, 1)
	require.Equal(t, "outputs", definition.TemplateBundles[0].ID)
	for _, raw := range []string{`{"schemaVersion":2,"template":{"id":"original","name":"Original"}}`, `{"schemaVersion":1}`, `{"schemaVersion":1,"template":null}`, `{"schemaVersion":1,"template":[]}`} {
		_, err := DecodeTemplateDefinition(json.RawMessage(raw))
		require.Error(t, err)
	}
}
