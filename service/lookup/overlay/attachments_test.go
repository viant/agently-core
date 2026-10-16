package overlay

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	loproto "github.com/viant/agently-core/protocol/lookup/overlay"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

type attachmentActorKey struct{}
type attachmentSource struct{ raw json.RawMessage }

func (s attachmentSource) Candidates(context.Context, identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	return []identity.ResourceCandidate{{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(s.raw)}}, nil
}
func (s attachmentSource) ReadCandidate(_ context.Context, _ identity.ResourceURI, c identity.ResourceCandidate) (json.RawMessage, error) {
	if c.ContentFingerprint != identity.ContentFingerprint(s.raw) {
		return nil, identity.ErrResourceStale
	}
	return s.raw, nil
}

type attachmentPolicy struct{ actor identity.VerifiedActor }

func (p attachmentPolicy) SelectRevision(_ context.Context, ref identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if len(c) != 1 || ref.Revision != identity.WorkingCandidate {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: p.actor.Subject, ValidUntil: p.actor.ValidUntil}, nil
}

func TestAttachedLookupLibraryUsesCurrentOwnerAndActor(t *testing.T) {
	t.Run("legacy-template", func(t *testing.T) { testAttachedLookupLibrary(t, false) })
	t.Run("lookup-primitive", func(t *testing.T) { testAttachedLookupLibrary(t, true) })
}
func testAttachedLookupLibrary(t *testing.T, lookupPrimitive bool) {
	actor := func(ctx context.Context) (identity.VerifiedActor, error) {
		subject, _ := ctx.Value(attachmentActorKey{}).(string)
		if subject == "" {
			return identity.VerifiedActor{}, identity.ErrResourceDenied
		}
		return identity.VerifiedActor{Subject: subject, Issuer: "synthetic", TenantID: "team", AccountID: "account", IdentityRevision: "1", ValidUntil: time.Now().Add(time.Hour)}, nil
	}
	verify := func(ctx context.Context, a identity.VerifiedActor) error {
		current, err := actor(ctx)
		if err != nil || a.Subject != current.Subject {
			return identity.ErrResourceDenied
		}
		return nil
	}
	library := json.RawMessage(`{"id":"library","name":"Library","lookupOverlays":[{"id":"names","target":{"kind":"template","id":"form"},"bindings":[{"match":{"fieldName":"account","type":"string"},"lookup":{"dataSource":"datasource://team/accounts"},"named":{"name":"account"}}]}]}`)
	libraryURI := "template://team/library"
	if lookupPrimitive {
		libraryURI = "lookup://team/library"
		library = json.RawMessage(`{"id":"library","target":{"kind":"template","id":"form"},"bindings":[{"match":{"fieldName":"account","type":"string"},"lookup":{"dataSource":"datasource://team/accounts"},"named":{"name":"account"}}]}`)
	}
	reference := loproto.LibraryReference{Resource: identity.ResourceRef{URI: libraryURI, Revision: identity.WorkingCandidate}, ProviderIdentity: "library-provider", ContentFingerprint: identity.ContentFingerprint(library)}
	ownerRaw, _ := json.Marshal(map[string]any{"id": "form", "name": "Form", "lookupLibraries": []loproto.LibraryReference{reference}})
	sources := map[string]attachmentSource{"template://team/form": {raw: ownerRaw}, libraryURI: {raw: library}}
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	for _, provider := range []string{"owner-provider", "library-provider"} {
		uri := "template://team/form"
		if provider == "library-provider" {
			uri = libraryURI
		}
		local, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: provider, Actor: actor, Verify: verify, Authorize: func(_ context.Context, a identity.VerifiedActor, u identity.ResourceURI, _ string) error {
			if u.Namespace != "team" || u.Name == "library" && a.Subject != "alice" {
				return identity.ErrResourceDenied
			}
			return nil
		}, Bindings: []resources.LocalResourceBinding{{URI: uri, FormatVersion: 1, Resolver: func(_ context.Context, a identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
			return &identity.ResourceResolver{ProviderIdentity: provider, Source: sources[uri], Policy: attachmentPolicy{actor: a}}, nil
		}}}, Validators: map[string]resources.LocalResourceValidator{"lookup": resources.ValidateLookupDefinition, "template": func(_ int64, raw json.RawMessage) error {
			if !json.Valid(raw) {
				return identity.ErrResource
			}
			return nil
		}}})
		require.NoError(t, err)
		require.NoError(t, mgr.RegisterLocal(context.Background(), provider, &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: provider}, func(context.Context) (mcpclient.Interface, error) { return resources.NewLocalMCPClient(local) }))
	}
	g := resources.NewGateway(mgr, actor, verify, "host")
	defer g.Close()
	alice := context.WithValue(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), attachmentActorKey{}, "alice")
	got, err := g.GetForProvider(alice, "owner-provider", identity.ResourceRef{URI: "template://team/form", Revision: identity.WorkingCandidate}, nil)
	require.NoError(t, err)
	service, err := ReadAttachments(alice, g, *got.ResolvedResource)
	require.NoError(t, err)
	props := map[string]interface{}{"account": map[string]interface{}{"type": "string"}, "other": map[string]interface{}{"type": "string"}}
	service.Apply("template", "form", props)
	require.Equal(t, "lookup", props["account"].(map[string]interface{})["x-ui-widget"])
	require.NotContains(t, props["other"].(map[string]interface{}), "x-ui-widget")
	registry := service.Registry("template", "form")
	require.Len(t, registry, 1)
	require.Equal(t, "account", registry[0].Name)
	if lookupPrimitive {
		require.Equal(t, "library-provider", registry[0].ProviderIdentity)
		require.Equal(t, "datasource://team/accounts", registry[0].DataSourceResource.URI)
	}
	require.Empty(t, service.Registry("template", "other"), "original target matching remains active")
	bob := context.WithValue(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"}), attachmentActorKey{}, "bob")
	bobOwner, err := g.GetForProvider(bob, "owner-provider", identity.ResourceRef{URI: "template://team/form", Revision: identity.WorkingCandidate}, nil)
	require.NoError(t, err)
	_, err = ReadAttachments(bob, g, *bobOwner.ResolvedResource)
	require.ErrorIs(t, err, identity.ErrResourceDenied, "Alice's library service must not become Bob's cache")
	_, err = ReadAttachments(bob, g, *got.ResolvedResource)
	require.Error(t, err, "foreign original owner pin remains denied")
	changed := *got.ResolvedResource
	changed.ContentFingerprint = identity.ContentFingerprint(json.RawMessage(`{}`))
	_, err = ReadAttachments(alice, g, changed)
	require.Error(t, err)
}
