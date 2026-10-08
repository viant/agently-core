package reporting

import (
	"context"
	"encoding/json"
	"errors"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	authsvc "github.com/viant/agently-core/service/auth"
	"github.com/viant/forge/backend/reporting/registry"
	"os"
	"strings"
	"testing"
	"time"
)

type reportFixtureTransport func(context.Context, string, map[string]interface{}) (string, error)

func (f reportFixtureTransport) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	return f(ctx, name, args)
}
func TestAuthoredResourceExecutesRealDatasourcePipeline(t *testing.T) {
	original, err := os.ReadFile("testdata/authored_report.json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope registry.ReportEnvelope
	if json.Unmarshal(original, &envelope) != nil {
		t.Fatal("fixture")
	}
	for id, raw := range envelope.DataSources {
		var descriptor dsproto.DataSource
		_ = json.Unmarshal(raw, &descriptor)
		if descriptor.Backend == nil {
			continue
		}
		descriptor.Backend.Service = "arbitrary-producer"
		descriptor.Backend.Method = "read-approved-" + id
		descriptorRaw, _ := json.Marshal(descriptor)
		envelope.DataSources[id] = descriptorRaw
		for i := range envelope.Dependencies {
			d := &envelope.Dependencies[i]
			if d.Kind == "datasource" && d.ID == id {
				d.ContentFingerprint = identity.ContentFingerprint(descriptorRaw)
			}
		}
	}
	original, _ = json.Marshal(envelope)
	content := append([]byte(nil), original...)
	uri, _ := identity.ParseResourceURI("report://steward/forecast_supply_command_center")
	binding := "verified-account"
	allowed := true
	expiry := time.Now().Add(time.Minute)
	resolver := &identity.ResourceResolver{Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return append(json.RawMessage(nil), content...), nil }}, Policy: reportPolicyFunc(func(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
		if !allowed {
			return identity.ResourceDecision{}, identity.ErrResourceDenied
		}
		return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: binding, ValidUntil: expiry}, nil
	})}
	service := New(Options{Store: NewMemoryStore(), Compiler: NewReportSpecCompiler(time.Now), Exporter: NewForgeExporter(nil), ResourceResolver: func(context.Context, string) (*identity.ResourceResolver, error) { return resolver, nil }})
	calls := 0
	during := func() {}
	service.SetResourceDatasetExecutor(NewResourceDatasetExecutor(reportFixtureTransport(func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		calls++
		if !strings.HasPrefix(name, "arbitrary-producer:read-approved-") || authsvc.EffectiveUserID(ctx) != "fixture-user" {
			t.Fatalf("wrong real transport dispatch: %s", name)
		}
		pin, ok := requestctx.ResolvedResourceFromContext(ctx)
		if !ok || pin.URI != uri.String() {
			t.Fatal("missing exact pin")
		}
		during()
		return `{"data":[{"channelV2":"CTV","eventDate":"2026-10-07","avails":10,"hhUniqs":5}]}`, nil
	}), service.AuthorizeResourceDataset))
	ctx := authsvc.InjectUser(context.Background(), "fixture-user")
	request := &ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: uri.String()}}
	result, err := service.ExecuteResource(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if calls != len(result.Datasets) || len(result.Datasets["primary"].Rows) != 1 || len(result.ReportFill) == 0 || len(result.ReportPrint) == 0 {
		t.Fatalf("missing actual execution artifacts: %+v", result)
	}
	originalPin := *result.Resource
	for _, test := range []struct {
		name   string
		change func()
	}{{"source drift", func() { content = append(content, ' ') }}, {"account change", func() { binding = "other-account" }}, {"revocation", func() { allowed = false }}, {"lease expiry", func() { expiry = time.Now().Add(-time.Second) }}} {
		t.Run(test.name, func(t *testing.T) {
			content = append([]byte(nil), original...)
			binding = "verified-account"
			allowed = true
			expiry = time.Now().Add(time.Minute)
			during = test.change
			out, err := service.ExecuteResource(ctx, request)
			if err == nil || out != nil {
				t.Fatal("boundary released rows after authority/content changed")
			}
		})
	}
	content = append([]byte(nil), original...)
	binding = "verified-account"
	allowed = true
	expiry = time.Now().Add(time.Minute)
	during = func() {}
	before := calls
	forged := originalPin
	forged.AuthorityBinding = "forged"
	if out, err := service.ExecuteResource(ctx, &ExecuteResourceRequest{ResolvedResource: &forged}); err == nil || out != nil || calls != before {
		t.Fatal("forged pin reached transport")
	}
	if _, err := service.ExecuteResource(ctx, &ExecuteResourceRequest{Resource: request.Resource, Parameters: map[string]interface{}{"backend": "injected"}}); err == nil || calls != before {
		t.Fatal("undeclared source override executed")
	}

	job, err := service.SubmitExport(ctx, &SubmitExportRequest{Resource: request.Resource, Format: ExportFormatPDF})
	if err != nil {
		t.Fatal(err)
	}
	if job.ResourcePin == nil || job.ResourcePin.URI != uri.String() {
		t.Fatal("server run export lost exact pin")
	}
	completed, err := service.RunExport(ctx, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := service.GetArtifact(ctx, completed.ArtifactID)
	if err != nil || len(artifact.Data) == 0 || !strings.HasPrefix(string(artifact.Data), "%PDF-") {
		t.Fatalf("server run actual PDF export missing: %v", err)
	}
	before = calls
	allowed = false
	if _, err := service.ExecuteResource(ctx, request); !errors.Is(err, identity.ErrResourceDenied) || calls != before {
		t.Fatal("unauthorized source reached transport")
	}
}

func TestExecuteDenialPrecedesCompilerAndDatasourceWork(t *testing.T) {
	uri, _ := identity.ParseResourceURI("report://scope/denied")
	candidate := identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte("definition"))}
	compileCalls, dispatchCalls, compileResolvers := 0, 0, 0
	denied := &identity.ResourceResolver{Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return json.RawMessage("definition"), nil }}, Policy: reportPolicyFunc(func(context.Context, identity.ResourceRef, []identity.ResourceCandidate) (identity.ResourceDecision, error) {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	})}
	service := New(Options{Store: NewMemoryStore(), Compiler: reportCompilerFunc(func(context.Context, *CompileRequest) (*CompileResult, error) { compileCalls++; return nil, nil }), ResourceResolver: func(_ context.Context, op string) (*identity.ResourceResolver, error) {
		if op == "report.compile" {
			compileResolvers++
		}
		return denied, nil
	}})
	service.SetResourceDatasetExecutor(func(context.Context, identity.ResolvedResource, *dsproto.DataSource, map[string]interface{}) (*dsproto.FetchResult, error) {
		dispatchCalls++
		return nil, nil
	})
	for _, request := range []*ExecuteResourceRequest{{Resource: &identity.ResourceRef{URI: uri.String()}}, {ResolvedResource: &identity.ResolvedResource{URI: uri.String(), ResourceCandidate: candidate, AuthorityBinding: "old", ValidUntil: time.Now().Add(time.Minute)}}} {
		result, err := service.ExecuteResource(context.Background(), request)
		if !errors.Is(err, identity.ErrResourceDenied) || result != nil || compileCalls != 0 || compileResolvers != 0 || dispatchCalls != 0 {
			t.Fatalf("denied execution performed compile/data work: %v", err)
		}
	}
}
