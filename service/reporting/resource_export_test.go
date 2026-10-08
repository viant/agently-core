package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
	"github.com/viant/forge/backend/reporting/registry"
)

type exportPinPolicy struct {
	state  *exportPinState
	action string
}

func (p exportPinPolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.actions = append(p.state.actions, p.action)
	if p.state.denied[p.action] {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	if len(candidates) != 1 {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: p.state.binding, ValidUntil: p.state.lease}, nil
}

type exportPinState struct {
	mu      sync.Mutex
	uri     identity.ResourceURI
	raw     json.RawMessage
	binding string
	lease   time.Time
	now     time.Time
	denied  map[string]bool
	actions []string
}

func newPinnedExportService(t *testing.T, exporter Exporter) (*Service, *MemoryStore, *exportPinState, *identity.ResolvedResource) {
	t.Helper()
	now := time.Now().UTC()
	state := &exportPinState{uri: identity.ResourceURI{Kind: "report", Namespace: "sales", Name: "orders"}, raw: json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Orders"},"reportSpec":{"kind":"reportSpec","version":1,"source":{"kind":"dashboard.reportBuilder","containerId":"orders","stateKey":"orders","dataSourceRef":"orders"},"datasets":[],"blocks":[]}}`), binding: "owner-account-revision", lease: now.Add(time.Minute), now: now, denied: map[string]bool{}}
	source := &identity.LocalResource{URI: state.uri, Load: func(context.Context) (json.RawMessage, error) {
		state.mu.Lock()
		defer state.mu.Unlock()
		return append(json.RawMessage(nil), state.raw...), nil
	}}
	resolve := func(_ context.Context, action string) (*identity.ResourceResolver, error) {
		return &identity.ResourceResolver{Source: source, Policy: exportPinPolicy{state: state, action: action}, Now: func() time.Time {
			state.mu.Lock()
			defer state.mu.Unlock()
			return state.now
		}}, nil
	}
	store := NewMemoryStore()
	service := New(Options{Store: store, Exporter: exporter, ResourceResolver: resolve, Now: func() time.Time {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.now
	}})
	executorResolver, err := resolve(context.Background(), "report.execute")
	if err != nil {
		t.Fatal(err)
	}
	pin, err := executorResolver.Resolve(context.Background(), identity.ResourceRef{URI: state.uri.String()})
	if err != nil {
		t.Fatal(err)
	}
	return service, store, state, pin
}

func trustedExecution(pin *identity.ResolvedResource) *ExecuteResourceResult {
	return &ExecuteResourceResult{Resource: cloneResolvedResource(pin), ReportSpec: json.RawMessage(validTestReportSpecJSON()), ReportFill: json.RawMessage(validTestReportFillJSON()), ReportPrint: json.RawMessage(validTestReportPrintJSON())}
}

func submitPinnedExport(t *testing.T, service *Service, pin *identity.ResolvedResource) *ExportJob {
	t.Helper()
	ctx := authsvc.InjectUser(context.Background(), "owner")
	job, err := service.SubmitExport(ctx, &SubmitExportRequest{Format: ExportFormatPDF, execution: trustedExecution(pin)})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

type pinAwareExporter struct {
	state   *exportPinState
	mutate  bool
	calls   int
	seenPin *identity.ResolvedResource
}

func (e *pinAwareExporter) Export(_ context.Context, request *RenderRequest) (*RenderResult, error) {
	e.calls++
	e.seenPin = cloneResolvedResource(request.ResourcePin)
	if e.mutate {
		e.state.mu.Lock()
		e.state.raw = json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"Changed"},"reportSpec":{"kind":"reportSpec","version":1,"datasets":[],"blocks":[]}}`)
		e.state.mu.Unlock()
	}
	return &RenderResult{ContentType: "application/pdf", Data: []byte("%PDF pinned")}, nil
}

func TestUnifiedExportRequiresExecutedPinAndPersistsIt(t *testing.T) {
	exporter := &pinAwareExporter{}
	service, store, _, pin := newPinnedExportService(t, exporter)
	ctx := authsvc.InjectUser(context.Background(), "owner")
	legacy := &SubmitExportRequest{ArtifactRef: pin.URI, Format: ExportFormatPDF, ReportPrint: json.RawMessage(validTestReportPrintJSON())}
	if _, err := service.SubmitExport(ctx, legacy); !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatalf("unified mode accepted a legacy snapshot: %v", err)
	}
	if _, err := service.SubmitExport(ctx, &SubmitExportRequest{ReportRunID: "run-without-resource-pin", Format: ExportFormatPDF}); !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatalf("unified mode accepted a run reference without a canonical resource pin: %v", err)
	}
	job := submitPinnedExport(t, service, pin)
	if job.ResourcePin == nil || !sameResourcePin(job.ResourcePin, pin) || job.ArtifactRef != pin.URI {
		t.Fatalf("job lost exact resource pin: %+v", job)
	}
	stored, err := store.GetJob(ctx, job.JobID)
	if err != nil || stored.ResourcePin == nil || !sameResourcePin(stored.ResourcePin, pin) {
		t.Fatalf("memory store lost resource pin: job=%+v err=%v", stored, err)
	}
	row := encodeJob(job)
	restored, err := decodeJob(row)
	if err != nil || restored.ResourcePin == nil || !sameResourcePin(restored.ResourcePin, pin) || string(restored.Metadata) != string(job.Metadata) {
		t.Fatalf("persistent metadata pin round-trip: restored=%+v err=%v", restored, err)
	}
	if !strings.Contains(string(row.Metadata), exportResourceMetadataKey) || strings.Contains(string(job.Metadata), exportResourceMetadataKey) {
		t.Fatalf("pin metadata envelope leaked into runtime metadata: persisted=%s runtime=%s", row.Metadata, job.Metadata)
	}
}

func TestUnifiedExportExecutesTheExactResourceInsteadOfAcceptingSnapshots(t *testing.T) {
	raw, err := os.ReadFile("testdata/authored_report.json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope registry.ReportEnvelope
	if json.Unmarshal(raw, &envelope) != nil {
		t.Fatal("invalid authored report fixture")
	}
	for id, descriptorRaw := range envelope.DataSources {
		descriptor := &dsproto.DataSource{}
		if json.Unmarshal(descriptorRaw, descriptor) != nil || descriptor.Backend == nil {
			continue
		}
		descriptor.Backend.Service = "trusted-fixture"
		descriptor.Backend.Method = "execute-" + id
		descriptorRaw, _ = json.Marshal(descriptor)
		envelope.DataSources[id] = descriptorRaw
		for i := range envelope.Dependencies {
			dependency := &envelope.Dependencies[i]
			if dependency.Kind == "datasource" && dependency.ID == id {
				dependency.ContentFingerprint = identity.ContentFingerprint(descriptorRaw)
			}
		}
	}
	raw, _ = json.Marshal(envelope)
	uri, _ := identity.ParseResourceURI("report://steward/forecast_supply_command_center")
	resolver := &identity.ResourceResolver{Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return append(json.RawMessage(nil), raw...), nil }}, Policy: reportPolicyFunc(func(_ context.Context, _ identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
		if len(candidates) != 1 {
			return identity.ResourceDecision{}, identity.ErrResourceDenied
		}
		return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: "export-owner", ValidUntil: time.Now().Add(time.Minute)}, nil
	})}
	service := New(Options{Store: NewMemoryStore(), Compiler: NewReportSpecCompiler(time.Now), ResourceResolver: func(context.Context, string) (*identity.ResourceResolver, error) { return resolver, nil }})
	calls := 0
	seenDatasources := map[string]bool{}
	service.SetResourceDatasetExecutor(NewResourceDatasetExecutor(reportFixtureTransport(func(_ context.Context, name string, _ map[string]interface{}) (string, error) {
		calls++
		if !strings.HasPrefix(name, "trusted-fixture:execute-") {
			t.Fatalf("unexpected datasource operation: %s", name)
		}
		seenDatasources[name] = true
		return `{"data":[{"eventDate":"2026-10-07","spend":12.5}]}`, nil
	}), service.AuthorizeResourceDataset))
	ctx := authsvc.InjectUser(context.Background(), "export-owner")
	job, err := service.SubmitExport(ctx, &SubmitExportRequest{Resource: &identity.ResourceRef{URI: uri.String()}, Format: ExportFormatPDF})
	if err != nil {
		t.Fatal(err)
	}
	if calls < 2 || len(seenDatasources) < 2 || job.ResourcePin == nil || job.ResourcePin.URI != uri.String() || len(job.ReportPrint) == 0 {
		t.Fatalf("unified submit did not execute and pin canonical resource: calls=%d job=%+v", calls, job)
	}
	if _, err := service.SubmitExport(ctx, &SubmitExportRequest{ArtifactRef: uri.String(), Format: ExportFormatPDF, ReportPrint: json.RawMessage(validTestReportPrintJSON())}); !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatalf("unified submit accepted a client snapshot: %v", err)
	}
}

func TestUnifiedExportRejectsTamperedPinAndExpiredLeaseBeforeRendering(t *testing.T) {
	exporter := &pinAwareExporter{}
	service, store, state, pin := newPinnedExportService(t, exporter)
	job := submitPinnedExport(t, service, pin)
	persisted := encodeJob(job)
	var envelope map[string]persistedExportMetadata
	if err := json.Unmarshal(persisted.Metadata, &envelope); err != nil {
		t.Fatal(err)
	}
	wrapped := envelope[exportResourceMetadataKey]
	wrapped.ResourcePin.AuthorityBinding = "tampered-persisted-binding"
	envelope[exportResourceMetadataKey] = wrapped
	persisted.Metadata, _ = json.Marshal(envelope)
	persistedJob, err := decodeJob(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.validateExportResourcePin(authsvc.InjectUser(context.Background(), "owner"), persistedJob, "report.export"); !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatalf("tampered persisted pin was accepted: %v", err)
	}
	tampered, err := store.GetJob(context.Background(), job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	tampered.ResourcePin.AuthorityBinding = "forged-binding"
	if err := store.UpdateJob(context.Background(), tampered); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunExport(authsvc.InjectUser(context.Background(), "owner"), job.JobID); !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatalf("tampered pin reached renderer: %v", err)
	}
	if exporter.calls != 0 {
		t.Fatalf("renderer called %d times for tampered pin", exporter.calls)
	}

	exporter = &pinAwareExporter{}
	service, store, state, pin = newPinnedExportService(t, exporter)
	job = submitPinnedExport(t, service, pin)
	state.mu.Lock()
	state.now = pin.ValidUntil.Add(time.Second)
	state.mu.Unlock()
	if _, err := service.RunExport(authsvc.InjectUser(context.Background(), "owner"), job.JobID); !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatalf("expired pin reached renderer: %v", err)
	}
	if exporter.calls != 0 {
		t.Fatalf("renderer called %d times for expired pin", exporter.calls)
	}
	if _, err := store.GetArtifact(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unexpected artifact lookup result: %v", err)
	}
}

func TestUnifiedExportDriftAfterRenderDoesNotPersistArtifact(t *testing.T) {
	exporter := &pinAwareExporter{mutate: true}
	service, store, state, pin := newPinnedExportService(t, exporter)
	exporter.state = state
	job := submitPinnedExport(t, service, pin)
	completed, err := service.RunExport(authsvc.InjectUser(context.Background(), "owner"), job.JobID)
	if !errors.Is(err, identity.ErrResourceStale) {
		t.Fatalf("source drift was not reported after render: job=%+v err=%v", completed, err)
	}
	if exporter.calls != 1 || !sameResourcePin(exporter.seenPin, pin) {
		t.Fatalf("renderer did not receive the original pin: calls=%d pin=%+v", exporter.calls, exporter.seenPin)
	}
	if artifacts, _ := store.ListArtifacts(context.Background()); len(artifacts) != 0 {
		t.Fatalf("artifact escaped after source drift: %+v", artifacts)
	}
}

func TestUnifiedExportResultReadsRevalidatePinnedAuthority(t *testing.T) {
	exporter := &pinAwareExporter{}
	service, _, state, pin := newPinnedExportService(t, exporter)
	job := submitPinnedExport(t, service, pin)
	completed, err := service.RunExport(authsvc.InjectUser(context.Background(), "owner"), job.JobID)
	if err != nil || completed.Status != JobStatusSucceeded {
		t.Fatalf("export completion=%+v err=%v", completed, err)
	}
	state.mu.Lock()
	state.denied["report.exportResult.read"] = true
	state.mu.Unlock()
	ctx := authsvc.InjectUser(context.Background(), "owner")
	if _, err := service.GetExportStatus(ctx, job.JobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("status released after pinned authority denial: %v", err)
	}
	if _, err := service.GetArtifact(ctx, completed.ArtifactID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("artifact released after pinned authority denial: %v", err)
	}
	listed, err := service.ListExportJobs(ctx, &ListExportJobsInput{})
	if err != nil || len(listed.Jobs) != 0 {
		t.Fatalf("job listing released denied resource: %+v err=%v", listed, err)
	}
	artifacts, err := service.ListExportArtifacts(ctx, &ListExportArtifactsInput{})
	if err != nil || len(artifacts.Artifacts) != 0 {
		t.Fatalf("artifact listing released denied resource: %+v err=%v", artifacts, err)
	}
}

func TestUnifiedWorkerCannotRestoreAnotherCallersOwner(t *testing.T) {
	exporter := &pinAwareExporter{}
	service, _, _, pin := newPinnedExportService(t, exporter)
	job := submitPinnedExport(t, service, pin)
	if _, err := service.RunExport(authsvc.InjectUser(context.Background(), "other-user"), job.JobID); !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatalf("caller impersonated queued owner: %v", err)
	}
	if exporter.calls != 0 {
		t.Fatal("other caller reached exporter")
	}
}

func TestUnifiedArtifactsDoNotPublishAnUnguardedScratchpadURL(t *testing.T) {
	service, _, _, _ := newPinnedExportService(t, &pinAwareExporter{})
	artifact := &Artifact{ArtifactID: "guarded", Data: []byte("private bytes"), SourceURL: "scratchpad://legacy"}
	if err := service.publishArtifactToScratchpad(context.Background(), artifact); err != nil || artifact.SourceURL != "" {
		t.Fatal("unified export exposed unguarded URL")
	}
	artifact.SourceURL = "scratchpad://legacy"
	output, err := service.enrichArtifactWithScratchpad(context.Background(), artifact)
	if err != nil || output.SourceURL != "" || string(output.Data) != "private bytes" {
		t.Fatal("unified download left resource guard")
	}
}

func TestUnifiedDirectCompletionCannotSupplyArtifactBytes(t *testing.T) {
	service, _, _, pin := newPinnedExportService(t, &pinAwareExporter{})
	job := submitPinnedExport(t, service, pin)
	ctx := authsvc.InjectUser(context.Background(), "owner")
	if _, err := service.CompleteExport(ctx, &CompleteExportRequest{JobID: job.JobID, Data: []byte("forged artifact")}); !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatalf("untrusted artifact bytes completed canonical job: %v", err)
	}
}
