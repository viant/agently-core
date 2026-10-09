package resource

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
	reporting "github.com/viant/agently-core/service/reporting"
)

func TestReportGatewayActionLeaseCannotBeRenewedByLaterAdmission(t *testing.T) {
	catalog, _, _, _, _ := reportGatewayFixture(t)
	now := time.Now()
	initial := now.Add(time.Second)
	catalog.Gateway.Now = func() time.Time { return now }
	catalog.AdmissionLease = func(context.Context, string, identity.ResolvedResource, json.RawMessage) (time.Time, error) {
		return now.Add(time.Second), nil
	}
	reader, err := catalog.Reader(context.Background(), "report.execute")
	require.NoError(t, err)
	pin, err := reader.Resolve(context.Background(), identity.ResourceRef{URI: "report://steward/sales"})
	require.NoError(t, err)
	require.Equal(t, initial, pin.ValidUntil)
	now = now.Add(500 * time.Millisecond)
	_, fresh, err := reader.ReadResolved(context.Background(), *pin)
	require.NoError(t, err)
	require.Equal(t, initial, fresh.ValidUntil)
	now = initial.Add(time.Nanosecond)
	_, fresh, err = reader.ReadResolved(context.Background(), *pin)
	require.Error(t, err)
	require.Nil(t, fresh)
}

func TestReportGatewayLeaseHookCannotReplaceConfiguredAdmission(t *testing.T) {
	catalog, _, _, _, _ := reportGatewayFixture(t)
	catalog.Admission = func(context.Context, string, identity.ResolvedResource, json.RawMessage) error {
		return identity.ErrResourceDenied
	}
	catalog.AdmissionLease = func(context.Context, string, identity.ResolvedResource, json.RawMessage) (time.Time, error) {
		return time.Now().Add(time.Minute), nil
	}
	reader, err := catalog.Reader(context.Background(), "report.execute")
	require.NoError(t, err)
	pin, err := reader.Resolve(context.Background(), identity.ResourceRef{URI: "report://steward/sales"})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	require.Nil(t, pin)
}

func TestReportGatewayExecutionRetainsLaterShorterGrantAcrossRegrant(t *testing.T) {
	catalog, source, _, _, _ := reportGatewayFixture(t)
	source.raw = actionLeaseExecutionSource()
	now := time.Now()
	base := now
	long, short := base.Add(10*time.Second), base.Add(3*time.Second)
	catalog.Gateway.Now = func() time.Time { return now }
	stage := 0
	catalog.AdmissionLease = func(_ context.Context, action string, _ identity.ResolvedResource, _ json.RawMessage) (time.Time, error) {
		if action == "report.execute" {
			switch stage {
			case 1:
				stage = 2
				return short, nil
			case 2:
				stage = 3
				return long, nil
			case 3:
				now = base.Add(5 * time.Second)
			}
		}
		return long, nil
	}
	svc := reporting.New(reporting.Options{Now: func() time.Time { return now }, Store: reporting.NewMemoryStore(), ReportCatalog: catalog, Compiler: reporting.NewReportSpecCompiler(func() time.Time { return now })})
	svc.SetResourceReaderFactory(catalog.Reader)
	fetched := 0
	// This trusted fixture executor isolates the coordinator's post-execution
	// checks; the real datasource pipeline is exercised by the preceding tests.
	svc.SetResourceDatasetExecutor(func(_ context.Context, pin identity.ResolvedResource, _ *dsproto.DataSource, _ map[string]interface{}) (*dsproto.FetchResult, error) {
		fetched++
		require.Equal(t, long, pin.ValidUntil)
		stage = 1
		return &dsproto.FetchResult{Rows: []map[string]interface{}{{"value": 1}}}, nil
	})
	out, err := svc.ExecuteResource(authsvc.InjectUser(context.Background(), "alice"), &reporting.ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: "report://steward/sales"}})
	require.Error(t, err)
	require.Nil(t, out)
	require.Equal(t, 1, fetched)
	require.Equal(t, 3, stage)
}

func TestReportGatewayExportWorkerRejectsShorterGrantDespiteLaterRegrant(t *testing.T) {
	catalog, source, _, _, _ := reportGatewayFixture(t)
	source.raw = actionLeaseExecutionSource()
	now := time.Now()
	base := now
	long, short := base.Add(10*time.Second), base.Add(3*time.Second)
	catalog.Gateway.Now = func() time.Time { return now }
	stage := 0
	catalog.AdmissionLease = func(_ context.Context, action string, _ identity.ResolvedResource, _ json.RawMessage) (time.Time, error) {
		if action == "report.export" {
			if stage == 1 {
				stage = 2
				return short, nil
			}
			if stage == 2 {
				stage = 3
				return long, nil
			}
		}
		return long, nil
	}
	renders := 0
	svc := reporting.New(reporting.Options{Now: func() time.Time { return now }, Store: reporting.NewMemoryStore(), ReportCatalog: catalog, Compiler: reporting.NewReportSpecCompiler(func() time.Time { return now }), Exporter: actionLeaseExporter(func(context.Context, *reporting.RenderRequest) (*reporting.RenderResult, error) {
		renders++
		stage = 1
		return &reporting.RenderResult{ContentType: "application/pdf", Data: []byte("%PDF buffered")}, nil
	})})
	svc.SetResourceReaderFactory(catalog.Reader)
	svc.SetResourceDatasetExecutor(reporting.NewResourceDatasetExecutor(actionLeaseTransport(func(context.Context, string, map[string]interface{}) (string, error) {
		return `{"data":[{"value":1}]}`, nil
	}), svc.AuthorizeResourceDataset))
	ctx := authsvc.InjectUser(context.Background(), "alice")
	job, err := svc.SubmitExport(ctx, &reporting.SubmitExportRequest{Resource: &identity.ResourceRef{URI: "report://steward/sales"}, Format: reporting.ExportFormatPDF})
	require.NoError(t, err)
	failed, err := svc.RunExport(ctx, job.JobID)
	require.Error(t, err)
	require.Equal(t, 3, stage)
	require.Equal(t, 1, renders)
	if failed != nil {
		require.Empty(t, failed.ArtifactID, "the earlier shorter grant must discard rendered PDF bytes")
	}
	now = base.Add(5 * time.Second)
	_, err = svc.RunExport(ctx, job.JobID)
	require.Error(t, err)
	require.Equal(t, 1, renders, "a regrant cannot replay a failed worker operation")
}

func TestReportGatewayCompileBuffersExpiredOriginalActionLease(t *testing.T) {
	catalog, _, _, _, _ := reportGatewayFixture(t)
	now := time.Now()
	deadline := now.Add(time.Second)
	catalog.Gateway.Now = func() time.Time { return now }
	calls := 0
	catalog.AdmissionLease = func(context.Context, string, identity.ResolvedResource, json.RawMessage) (time.Time, error) {
		calls++
		if calls == 1 {
			return deadline, nil
		}
		return now.Add(time.Minute), nil
	}
	svc := reporting.New(reporting.Options{Now: func() time.Time { return now }, Store: reporting.NewMemoryStore(), ReportCatalog: catalog, Compiler: gatewayReportCompiler(func(_ context.Context, in *reporting.CompileRequest) (*reporting.CompileResult, error) {
		now = deadline.Add(time.Nanosecond)
		return &reporting.CompileResult{ReportSpec: in.Document}, nil
	})})
	svc.SetResourceReaderFactory(catalog.Reader)
	out, err := svc.Compile(context.Background(), &reporting.CompileRequest{Resource: &identity.ResourceRef{URI: "report://steward/sales"}})
	require.Error(t, err)
	require.Nil(t, out)
}

type actionLeaseTransport func(context.Context, string, map[string]interface{}) (string, error)

func (f actionLeaseTransport) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	return f(ctx, name, args)
}

type actionLeaseExporter func(context.Context, *reporting.RenderRequest) (*reporting.RenderResult, error)

func (f actionLeaseExporter) Export(ctx context.Context, in *reporting.RenderRequest) (*reporting.RenderResult, error) {
	return f(ctx, in)
}

func actionLeaseExecutionSource() json.RawMessage {
	return json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Sales"},"reportSpec":{"version":1,"kind":"reportSpec","source":{"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},"title":"Sales","parameters":{"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},"layoutIntent":{"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},"refinements":[],"calculatedFields":[],"datasets":[{"id":"primary","dataSourceRef":"demo","request":{"limit":25,"offset":0}}],"blocks":[{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]},"dataSources":{"demo":{"id":"demo","cardinality":"collection","selectors":{"data":"data"},"backend":{"kind":"mcp_tool","service":"fixture","method":"read"},"cache":{"enabled":false}}}}`)
}

func TestReportGatewayRunAndExportRetainOriginalActionLease(t *testing.T) {
	for _, phase := range []string{"execute", "export-submit", "export-worker"} {
		t.Run(phase, func(t *testing.T) {
			catalog, source, _, _, _ := reportGatewayFixture(t)
			source.raw = actionLeaseExecutionSource()
			now := time.Now()
			deadline := now.Add(time.Second)
			first := true
			catalog.Gateway.Now = func() time.Time { return now }
			operation := "report.execute"
			if phase != "execute" {
				operation = "report.export"
			}
			catalog.AdmissionLease = func(_ context.Context, action string, _ identity.ResolvedResource, _ json.RawMessage) (time.Time, error) {
				if action == operation && first {
					first = false
					return deadline, nil
				}
				return now.Add(time.Minute), nil
			}
			rendered := 0
			svc := reporting.New(reporting.Options{Now: func() time.Time { return now }, Store: reporting.NewMemoryStore(), ReportCatalog: catalog, Compiler: reporting.NewReportSpecCompiler(func() time.Time { return now }), Exporter: actionLeaseExporter(func(_ context.Context, in *reporting.RenderRequest) (*reporting.RenderResult, error) {
				rendered++
				require.Equal(t, deadline, in.ResourcePin.ValidUntil)
				now = deadline.Add(time.Nanosecond)
				return &reporting.RenderResult{ContentType: "application/pdf", Data: []byte("%PDF fixture")}, nil
			})})
			svc.SetResourceReaderFactory(catalog.Reader)
			fetched := 0
			svc.SetResourceDatasetExecutor(reporting.NewResourceDatasetExecutor(actionLeaseTransport(func(_ context.Context, name string, _ map[string]interface{}) (string, error) {
				fetched++
				require.Equal(t, "fixture:read", name)
				if phase != "export-worker" {
					now = deadline.Add(time.Nanosecond)
				}
				return `{"data":[{"value":1}]}`, nil
			}), svc.AuthorizeResourceDataset))
			ctx := authsvc.InjectUser(context.Background(), "alice")
			if phase == "execute" {
				out, err := svc.ExecuteResource(ctx, &reporting.ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: "report://steward/sales"}})
				require.Error(t, err)
				require.Nil(t, out)
				require.Equal(t, 1, fetched)
				return
			}
			job, err := svc.SubmitExport(ctx, &reporting.SubmitExportRequest{Resource: &identity.ResourceRef{URI: "report://steward/sales"}, Format: reporting.ExportFormatPDF})
			if phase == "export-submit" {
				require.Error(t, err)
				require.Nil(t, job)
				require.Equal(t, 1, fetched)
				require.Zero(t, rendered)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, job.ResourcePin)
			require.Equal(t, deadline, job.ResourcePin.ValidUntil)
			result, err := svc.RunExport(ctx, job.JobID)
			require.Error(t, err)
			if result != nil {
				require.Empty(t, result.ArtifactID)
			}
			require.Equal(t, 1, rendered)
			_, err = svc.GetExportStatus(ctx, job.JobID)
			require.Error(t, err, "result retrieval cannot renew the original stored action lease")
		})
	}
}
