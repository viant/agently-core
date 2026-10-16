package resource

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/reporting"
	reportcatalog "github.com/viant/agently-core/service/reporting/catalog"
	userprovider "github.com/viant/agently-core/service/resource/provider/user"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

func zeroMetadataUserContext(subject string) context.Context {
	return authctx.WithProvider(authctx.WithCanonicalUserID(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: subject + "@fixture"}), subject), "verified-workspace")
}
func TestUserReportSaveReadAndPhasesWithDefaultPublicActorZeroMetadata(t *testing.T) {
	ctx := zeroMetadataUserContext("alice")
	bob := zeroMetadataUserContext("bob")
	source, err := userprovider.NewReports(reporting.NewMemoryStore(), "user-source")
	require.NoError(t, err)
	actor, verify := PublicReadActors()
	a, err := actor(ctx)
	require.NoError(t, err)
	require.True(t, a.PublicRead)
	require.Empty(t, a.Subject)
	require.True(t, a.ValidUntil.IsZero())
	p, err := NewLocalProvider(LocalConfig{ProviderIdentity: "user-source", Actor: actor, Verify: verify, Catalog: source, Authoring: source, Authorize: userprovider.Authorize, Validators: map[string]LocalResourceValidator{"report": ValidateReportEnvelope}})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	require.NoError(t, mgr.RegisterLocal(ctx, "user-source", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "user-source"}, func(context.Context) (mcpclient.Interface, error) { return NewLocalMCPClient(p) }))
	g := NewGateway(mgr, actor, verify, "")
	defer g.Close()
	writer, err := NewAuthoring(g, "user-source")
	require.NoError(t, err)
	uri := "report://users/alice/saved"
	original := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Original"}}`)
	saved, err := writer.CreateResource(ctx, primitive.CreateResourceInput{URI: uri, FormatVersion: 1, Definition: original})
	require.NoError(t, err)
	require.Equal(t, uri, saved.URI)
	catalog := &ReportGatewayCatalog{Gateway: g}
	listed, err := catalog.List(ctx, reportcatalog.ReportCatalogQuery{Namespace: "users"})
	require.NoError(t, err)
	require.Len(t, listed.Reports, 1)
	require.True(t, listed.Reports[0].Resource.ValidUntil.IsZero())
	reportingService := reporting.New(reporting.Options{Store: reporting.NewMemoryStore(), ReportCatalog: catalog})
	reportingService.SetResourceReaderFactory(catalog.Reader)
	reopened, err := reportingService.GetReport(ctx, &reporting.GetReportInput{ArtifactID: uri})
	require.NoError(t, err)
	require.Equal(t, uri, reopened.ArtifactRef)
	scoped, finish, err := g.BeginReadSequence(ctx, nil)
	require.NoError(t, err)
	reader, err := catalog.Reader(scoped, "report.retrieve")
	require.NoError(t, err)
	pin, err := reader.Resolve(scoped, identity.ResourceRef{URI: uri})
	require.NoError(t, err)
	require.True(t, pin.ValidUntil.IsZero())
	for i := 0; i < 2; i++ {
		raw, held, err := reader.ReadResolved(scoped, *pin)
		require.NoError(t, err)
		require.JSONEq(t, string(original), string(raw))
		require.True(t, held.ValidUntil.IsZero())
	}
	end, err := requestctx.BeginResourceReadDispatch(scoped)
	require.NoError(t, err)
	require.NoError(t, end())
	_, held, err := reader.ReadResolved(scoped, *pin)
	require.NoError(t, err)
	require.True(t, held.ValidUntil.IsZero())
	require.NoError(t, finish())
	_, err = writer.UpdateDraft(bob, uri, primitive.UpdateDraftInput{FormatVersion: 1, Definition: original})
	require.Error(t, err, "zero metadata never permits a foreign owner write")
	updated := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Updated"}}`)
	_, err = writer.UpdateDraft(ctx, uri, primitive.UpdateDraftInput{FormatVersion: 1, Definition: updated})
	require.NoError(t, err)
	raw, current, err := catalog.Read(ctx, identity.ResourceRef{URI: uri}, nil)
	require.NoError(t, err)
	require.JSONEq(t, string(updated), string(raw))
	require.True(t, current.ValidUntil.IsZero())
	_, _, err = catalog.Read(ctx, identity.ResourceRef{URI: uri}, pin)
	require.Error(t, err, "successful write invalidates the old fingerprint")
	_, _, err = catalog.Read(bob, identity.ResourceRef{URI: uri}, nil)
	require.Error(t, err, "private owning MCP still denies foreign reads")
}
