package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
	"github.com/viant/forge/backend/reporting/registry"
	"github.com/viant/forge/backend/types"
	"os"
	"testing"
	"time"
)

func TestReferencedReportOriginalChildPinsGuardExecutionAndExport(t *testing.T) {
	raw, e := os.ReadFile("testdata/authored_report.json")
	require.NoError(t, e)
	var envelope registry.ReportEnvelope
	require.NoError(t, json.Unmarshal(raw, &envelope))
	envelope.SchemaVersion = 2
	envelope.SourceFormat = registry.AuthoredReportFormat
	envelope.Format = registry.ResourceReportFormat
	envelope.DataSourceResources = map[string]primitive.DataSourceReference{}
	childPins := map[string]identity.ResolvedResource{}
	expiry := time.Now().Add(time.Minute)
	childExpiry := time.Now().Add(40 * time.Second)
	for id, descriptor := range envelope.DataSources {
		child, _ := json.Marshal(map[string]any{"schemaVersion": 1, "dataSource": json.RawMessage(descriptor)})
		uri := "datasource://shared/" + id
		fp := identity.ContentFingerprint(child)
		envelope.DataSourceResources[id] = primitive.DataSourceReference{Resource: identity.ResourceRef{URI: uri, Revision: "working"}, ContentFingerprint: fp, ProviderIdentity: "library"}
		childPins[id] = identity.ResolvedResource{ProviderIdentity: "library", URI: uri, ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: fp}, AuthorityBinding: "original-child-account", ValidUntil: childExpiry}
	}
	content, _ := json.Marshal(envelope)
	parentURI, _ := identity.ParseResourceURI("report://steward/forecast_supply_command_center")
	resolver := &identity.ResourceResolver{ProviderIdentity: "reports", Source: &identity.LocalResource{URI: parentURI, Load: func(context.Context) (json.RawMessage, error) { return content, nil }}, Policy: reportPolicyFunc(func(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
		return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "parent", ValidUntil: expiry}, nil
	})}
	s := New(Options{Store: NewMemoryStore(), Compiler: NewReportSpecCompiler(time.Now), Exporter: NewForgeExporter(nil), ResourceResolver: func(context.Context, string) (*identity.ResourceResolver, error) { return resolver, nil }})
	proof, e := types.NewWindowTargetHMAC(bytes.Repeat([]byte{7}, 32))
	require.NoError(t, e)
	revoked := false
	s.SetResourceDependencies(func(_ context.Context, _ identity.ResolvedResource, refs map[string]primitive.DataSourceReference, _ map[string]json.RawMessage, original map[string]identity.ResolvedResource) (map[string]identity.ResolvedResource, error) {
		if revoked {
			return nil, identity.ErrResourceDenied
		}
		out := cloneDependencyPins(childPins)
		if original != nil {
			for id, old := range original {
				fresh := out[id]
				if fresh.AuthorityBinding != old.AuthorityBinding || fresh.ResourceCandidate != old.ResourceCandidate || fresh.ProviderIdentity != old.ProviderIdentity {
					return nil, identity.ErrResourceDenied
				}
				fresh.ValidUntil = old.ValidUntil
				out[id] = fresh
			}
		}
		return out, nil
	}, proof)
	calls := 0
	during := func() {}
	s.SetResourceDatasetExecutor(NewResourceDatasetExecutor(reportFixtureTransport(func(context.Context, string, map[string]interface{}) (string, error) {
		calls++
		during()
		return `{"data":[{"channelV2":"CTV","eventDate":"2026-10-07","avails":10,"hhUniqs":5}]}`, nil
	}), s.AuthorizeResourceDataset))
	ctx := authsvc.InjectUser(context.Background(), "fixture-user")
	request := &ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: parentURI.String()}}
	positive, e := s.ExecuteResource(ctx, request)
	require.NoError(t, e)
	require.NotEmpty(t, positive.ReportPrint)
	require.NotEmpty(t, positive.DependencyToken)
	require.Equal(t, len(envelope.DataSourceResources), len(positive.DependencyPins))
	require.True(t, positive.Resource.ValidUntil.Equal(childExpiry))
	require.Greater(t, calls, 0)
	during = func() { revoked = true }
	denied, e := s.ExecuteResource(ctx, request)
	require.Error(t, e)
	require.Nil(t, denied)
	revoked = false
	during = func() {}
	forged := cloneDependencyPins(positive.DependencyPins)
	for id, pin := range forged {
		pin.AuthorityBinding = "fresh-substitute"
		forged[id] = pin
		break
	}
	before := calls
	denied, e = s.ExecuteResource(ctx, &ExecuteResourceRequest{ResolvedResource: positive.Resource, DependencyPins: forged, DependencyToken: positive.DependencyToken})
	require.Error(t, e)
	require.Nil(t, denied)
	require.Equal(t, before, calls)
	job, e := s.SubmitExport(ctx, &SubmitExportRequest{Format: ExportFormatPDF, execution: positive})
	require.NoError(t, e)
	require.NotEmpty(t, job.DependencyPins)
	stored, e := s.store.GetJob(ctx, job.JobID)
	require.NoError(t, e)
	require.Equal(t, job.DependencyToken, stored.DependencyToken)
	row := encodeJob(job)
	roundtrip, e := decodeJob(row)
	require.NoError(t, e)
	require.Equal(t, job.DependencyPins, roundtrip.DependencyPins)
	revoked = true
	result, e := s.RunExport(ctx, job.JobID)
	require.Error(t, e)
	require.Nil(t, result)
}

func TestReferenceReportWithoutDependencyHooksFailsBeforeCompiler(t *testing.T) {
	raw := json.RawMessage(`{"schemaVersion":2,"format":"forge.reportReferences","sourceFormat":"","reportDocument":{},"reportSpec":{},"dataSources":{"x":{"id":"x"}},"dataSourceResources":{"x":{"resource":{"uri":"datasource://example/x","revision":"working"},"contentFingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","providerIdentity":"library"}}}`)
	uri, _ := identity.ParseResourceURI("report://example/x")
	resolver := &identity.ResourceResolver{Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return raw, nil }}, Policy: reportPolicyFunc(func(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
		return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "parent", ValidUntil: time.Now().Add(time.Minute)}, nil
	})}
	s := New(Options{Store: NewMemoryStore(), Compiler: NewReportSpecCompiler(time.Now), ResourceResolver: func(context.Context, string) (*identity.ResourceResolver, error) { return resolver, nil }})
	result, e := s.Compile(context.Background(), &CompileRequest{Resource: &identity.ResourceRef{URI: uri.String()}})
	require.ErrorIs(t, e, identity.ErrResourceDenied)
	require.Nil(t, result)
}
