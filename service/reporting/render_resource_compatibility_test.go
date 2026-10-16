package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"

	iauth "github.com/viant/agently-core/internal/auth"
	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
)

// Installing a resource reader does not turn supplied rendering content into
// a selected resource. Explicit references still use that reader exclusively.
func TestInlineRenderingAndResourceDeliveryCoexist(t *testing.T) {
	reads, compiles := 0, 0
	denied := errors.New("managing source unavailable")
	service := New(Options{Store: NewMemoryStore(), Compiler: reportCompilerFunc(func(_ context.Context, in *CompileRequest) (*CompileResult, error) {
		compiles++
		if string(in.Document) != `{"title":"inline"}` {
			t.Fatal("inline document changed")
		}
		return &CompileResult{ReportSpec: json.RawMessage(`{"title":"inline"}`)}, nil
	}), ResourceResolver: func(context.Context, string) (*identity.ResourceResolver, error) { reads++; return nil, denied }})
	ctx := authsvc.InjectUser(context.Background(), "owner")
	compile := &CompileRequest{ArtifactRef: "report://draft/inline", Document: json.RawMessage(`{"title":"inline"}`)}
	before, _ := json.Marshal(compile)
	if _, err := service.Compile(ctx, compile); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(compile)
	if string(before) != string(after) || compiles != 1 || reads != 0 {
		t.Fatal("inline compile consulted source or changed input")
	}
	direct := &SubmitExportRequest{ArtifactRef: "report://draft/inline", Format: ExportFormatPDF, ReportPrint: json.RawMessage(validTestReportPrintJSON())}
	before, _ = json.Marshal(direct)
	job, err := service.SubmitExport(ctx, direct)
	if err != nil || job == nil || job.ResourcePin != nil || reads != 0 {
		t.Fatalf("inline export: %v reads=%d", err, reads)
	}
	after, _ = json.Marshal(direct)
	if string(before) != string(after) {
		t.Fatal("inline export input changed")
	}
	// Completing an inline job remains supported; canonical jobs retain their
	// separate execution/completion proof tests.
	if _, err = service.StartExport(ctx, job.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CompleteExport(ctx, &CompleteExportRequest{JobID: job.JobID, Data: []byte("%PDF inline")}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Compile(ctx, &CompileRequest{Resource: &identity.ResourceRef{URI: "report://managed/selected"}, Document: compile.Document}); !errors.Is(err, denied) {
		t.Fatalf("resource error hidden: %v", err)
	}
	if compiles != 1 {
		t.Fatal("source failure fell back to supplied document")
	}
	result, err := service.SubmitExport(ctx, &SubmitExportRequest{Resource: &identity.ResourceRef{URI: "report://managed/selected"}, Format: ExportFormatPDF})
	if result != nil || !errors.Is(err, denied) {
		t.Fatalf("resource export fell back: %v", err)
	}
	result, err = service.SubmitExport(ctx, &SubmitExportRequest{Source: &ExportSource{Kind: "report", ArtifactRef: "report://managed/selected"}, Format: ExportFormatPDF})
	if result != nil || !errors.Is(err, denied) {
		t.Fatalf("saved-source export fell back: %v", err)
	}
}

func TestStoredMaterializedEnvelopeKeepsRenderPayloadAndPin(t *testing.T) {
	service, _, state, _ := newPinnedExportService(t, nil, reportCompilerFunc(func(_ context.Context, request *CompileRequest) (*CompileResult, error) {
		if len(request.Document) == 0 || request.SourceKind != SourceKindReportSpec {
			t.Fatal("spec-only stored source lost")
		}
		return &CompileResult{ReportSpec: request.Document}, nil
	}))
	raw, _ := json.Marshal(map[string]any{"schemaVersion": 1, "reportSpec": json.RawMessage(validTestReportSpecJSON()), "reportFill": json.RawMessage(validTestReportFillJSON()), "reportPrint": json.RawMessage(validTestReportPrintJSON()), "compileState": map[string]any{"status": "compiled"}, "metadata": map[string]any{"original": "saved"}})
	state.raw = raw
	ctx := authsvc.InjectUser(context.Background(), "owner")
	report, err := service.GetReport(ctx, &GetReportInput{ArtifactRef: state.uri.String()})
	if err != nil || len(report.Document) != 0 || len(report.CompileState) == 0 {
		t.Fatalf("stored envelope projection lost payload: %v", err)
	}
	require.JSONEq(t, validTestReportFillJSON(), string(report.ReportFill))
	request := &SubmitExportRequest{Source: &ExportSource{Kind: "report", ArtifactRef: state.uri.String()}, Format: ExportFormatPDF}
	before, _ := json.Marshal(request)
	job, err := service.SubmitExport(ctx, request)
	if err != nil || job == nil || job.ResourcePin == nil || job.ResourcePin.URI != state.uri.String() {
		t.Fatalf("saved render payload/pin lost: %v", err)
	}
	require.JSONEq(t, validTestReportPrintJSON(), string(job.ReportPrint))
	after, _ := json.Marshal(request)
	if string(before) != string(after) {
		t.Fatal("saved source request changed")
	}
	request.Source.ReportPrint = json.RawMessage(validTestReportPrintJSON())
	if _, err = service.SubmitExport(ctx, request); !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatalf("caller snapshot overrode saved source: %v", err)
	}

	if _, err = service.Compile(ctx, &CompileRequest{Resource: &identity.ResourceRef{URI: state.uri.String()}}); err != nil {
		t.Fatal(err)
	}
}

func TestExportWorkerRestoresOnlyPersistedCanonicalIdentity(t *testing.T) {
	service := New(Options{Store: NewMemoryStore()})
	ctx := iauth.WithProvider(iauth.WithCanonicalUserID(authsvc.InjectUser(context.Background(), "legacy-owner"), "canonical-owner"), "workspace-oauth")
	job := &ExportJob{OwnerID: "legacy-owner", AuthContextRef: buildAuthContextRef(ctx), ResourcePin: &identity.ResolvedResource{}}
	restored, err := service.ensureRunExportAuthContext(context.Background(), job)
	if err != nil || iauth.CanonicalUserID(restored) != "canonical-owner" || iauth.Provider(restored) != "workspace-oauth" || authsvc.EffectiveUserID(restored) != "legacy-owner" {
		t.Fatalf("trusted worker identity lost: %v", err)
	}
	foreign := iauth.WithCanonicalUserID(authsvc.InjectUser(context.Background(), "legacy-owner"), "foreign-owner")
	if _, err = service.ensureRunExportAuthContext(foreign, job); !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatal("worker replaced authenticated canonical identity")
	}
	job.AuthContextRef = "actor=legacy-owner;provider=workspace-oauth"
	if _, err = service.ensureRunExportAuthContext(context.Background(), job); !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatal("old pinned job guessed canonical owner")
	}
	job.ResourcePin = nil
	if _, err = service.ensureRunExportAuthContext(context.Background(), job); err != nil {
		t.Fatal("unpinned legacy owner behavior changed")
	}
}
