package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	afsscratchpad "github.com/viant/afs/scratchpad"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	tokenctx "github.com/viant/agently-core/internal/auth/token"
	authsvc "github.com/viant/agently-core/service/auth"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

type compileRecorder struct {
	request *CompileRequest
	result  *CompileResult
	err     error
}

func (c *compileRecorder) Compile(_ context.Context, request *CompileRequest) (*CompileResult, error) {
	c.request = cloneCompileRequest(request)
	if c.err != nil {
		return nil, c.err
	}
	return cloneCompileResult(c.result), nil
}

type exportRecorder struct {
	request *RenderRequest
	result  *RenderResult
	err     error
}

func (r *exportRecorder) Export(_ context.Context, request *RenderRequest) (*RenderResult, error) {
	if request != nil {
		r.request = &RenderRequest{
			JobID:          request.JobID,
			ArtifactRef:    request.ArtifactRef,
			OwnerID:        request.OwnerID,
			ConversationID: request.ConversationID,
			WorkspaceID:    request.WorkspaceID,
			AuthContextRef: request.AuthContextRef,
			Format:         request.Format,
			Scope:          request.Scope,
			ReportSpec:     cloneJSON(request.ReportSpec),
			ReportFill:     cloneJSON(request.ReportFill),
			ReportPrint:    cloneJSON(request.ReportPrint),
			Metadata:       cloneJSON(request.Metadata),
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.result == nil {
		return nil, nil
	}
	return &RenderResult{
		ContentType:  r.result.ContentType,
		Data:         append([]byte{}, r.result.Data...),
		Diagnostics:  cloneDiagnostics(r.result.Diagnostics),
		RetentionTTL: r.result.RetentionTTL,
	}, nil
}

type queuedExportRecorder struct {
	requests []*RenderRequest
	errors   map[string]error
}

func (r *queuedExportRecorder) Export(_ context.Context, request *RenderRequest) (*RenderResult, error) {
	if request != nil {
		r.requests = append(r.requests, &RenderRequest{
			JobID:          request.JobID,
			ArtifactRef:    request.ArtifactRef,
			OwnerID:        request.OwnerID,
			ConversationID: request.ConversationID,
			WorkspaceID:    request.WorkspaceID,
			AuthContextRef: request.AuthContextRef,
			Format:         request.Format,
			Scope:          request.Scope,
			ReportSpec:     cloneJSON(request.ReportSpec),
			ReportFill:     cloneJSON(request.ReportFill),
			ReportPrint:    cloneJSON(request.ReportPrint),
			Metadata:       cloneJSON(request.Metadata),
		})
	}
	if err := r.errors[request.JobID]; err != nil {
		return nil, err
	}
	return &RenderResult{
		ContentType: defaultContentType(request.Format),
		Data:        []byte("%" + string(request.Format) + "-" + request.JobID),
	}, nil
}

type auditRecorder struct {
	events []*AuditEvent
}

func (r *auditRecorder) Record(_ context.Context, event *AuditEvent) error {
	r.events = append(r.events, cloneAuditEvent(event))
	return nil
}

type authCaptureExporter struct {
	requests     []*RenderRequest
	accessTokens []string
	idTokens     []string
	actorIDs     []string
}

func (e *authCaptureExporter) Export(ctx context.Context, request *RenderRequest) (*RenderResult, error) {
	if request != nil {
		e.requests = append(e.requests, &RenderRequest{
			JobID:          request.JobID,
			ArtifactRef:    request.ArtifactRef,
			OwnerID:        request.OwnerID,
			ConversationID: request.ConversationID,
			WorkspaceID:    request.WorkspaceID,
			AuthContextRef: request.AuthContextRef,
			Format:         request.Format,
			Scope:          request.Scope,
			ReportSpec:     cloneJSON(request.ReportSpec),
			ReportFill:     cloneJSON(request.ReportFill),
			ReportPrint:    cloneJSON(request.ReportPrint),
			Metadata:       cloneJSON(request.Metadata),
		})
	}
	e.accessTokens = append(e.accessTokens, strings.TrimSpace(authsvc.MCPAuthToken(ctx, false)))
	e.idTokens = append(e.idTokens, strings.TrimSpace(authsvc.MCPAuthToken(ctx, true)))
	e.actorIDs = append(e.actorIDs, strings.TrimSpace(authsvc.EffectiveUserID(ctx)))
	return &RenderResult{
		ContentType: "application/pdf",
		Data:        []byte("%PDF-auth"),
	}, nil
}

type staticTokenProvider struct {
	tokensByKey map[tokenctx.Key]*scyauth.Token
}

func (p *staticTokenProvider) EnsureTokens(ctx context.Context, key tokenctx.Key) (context.Context, error) {
	if p == nil {
		return ctx, nil
	}
	tok := p.tokensByKey[key]
	if tok == nil {
		return ctx, nil
	}
	return authsvc.InjectTokens(ctx, tok), nil
}

func (p *staticTokenProvider) Store(ctx context.Context, key tokenctx.Key, tok *scyauth.Token) error {
	return nil
}

func (p *staticTokenProvider) Invalidate(ctx context.Context, key tokenctx.Key) error {
	return nil
}

type claimRaceStore struct {
	base       Store
	claimJobID string
	claimTime  time.Time
	listed     bool
	claimed    bool
}

func (s *claimRaceStore) CreateJob(ctx context.Context, job *ExportJob) error {
	return s.base.CreateJob(ctx, job)
}

func (s *claimRaceStore) GetJob(ctx context.Context, jobID string) (*ExportJob, error) {
	if s.listed && !s.claimed && jobID == s.claimJobID {
		job, err := s.base.GetJob(ctx, jobID)
		if err != nil || job == nil {
			return job, err
		}
		startedAt := s.claimTime
		job.Status = JobStatusRunning
		job.StartedAt = &startedAt
		if err := s.base.UpdateJob(ctx, job); err != nil {
			return nil, err
		}
		s.claimed = true
	}
	return s.base.GetJob(ctx, jobID)
}

func (s *claimRaceStore) ListJobs(ctx context.Context) ([]*ExportJob, error) {
	s.listed = true
	return s.base.ListJobs(ctx)
}

func (s *claimRaceStore) UpdateJob(ctx context.Context, job *ExportJob) error {
	return s.base.UpdateJob(ctx, job)
}

func (s *claimRaceStore) PutArtifact(ctx context.Context, artifact *Artifact) error {
	return s.base.PutArtifact(ctx, artifact)
}

func (s *claimRaceStore) GetArtifact(ctx context.Context, artifactID string) (*Artifact, error) {
	return s.base.GetArtifact(ctx, artifactID)
}

func (s *claimRaceStore) ListArtifacts(ctx context.Context) ([]*Artifact, error) {
	return s.base.ListArtifacts(ctx)
}

func (s *claimRaceStore) CreateSharedArtifact(ctx context.Context, artifact *SharedArtifact) error {
	return s.base.CreateSharedArtifact(ctx, artifact)
}

func (s *claimRaceStore) GetSharedArtifact(ctx context.Context, artifactID string) (*SharedArtifact, error) {
	return s.base.GetSharedArtifact(ctx, artifactID)
}

func (s *claimRaceStore) ListSharedArtifacts(ctx context.Context) ([]*SharedArtifact, error) {
	return s.base.ListSharedArtifacts(ctx)
}

func (s *claimRaceStore) UpdateSharedArtifact(ctx context.Context, artifact *SharedArtifact) error {
	return s.base.UpdateSharedArtifact(ctx, artifact)
}

type failingCompleteStore struct {
	base      Store
	failJobID string
}

func (s *failingCompleteStore) CreateJob(ctx context.Context, job *ExportJob) error {
	return s.base.CreateJob(ctx, job)
}

func (s *failingCompleteStore) GetJob(ctx context.Context, jobID string) (*ExportJob, error) {
	return s.base.GetJob(ctx, jobID)
}

func (s *failingCompleteStore) ListJobs(ctx context.Context) ([]*ExportJob, error) {
	return s.base.ListJobs(ctx)
}

func (s *failingCompleteStore) UpdateJob(ctx context.Context, job *ExportJob) error {
	if s.failJobID != "" && job != nil && job.JobID == s.failJobID && job.Status == JobStatusSucceeded {
		return errors.New("completion update failed")
	}
	return s.base.UpdateJob(ctx, job)
}

func (s *failingCompleteStore) PutArtifact(ctx context.Context, artifact *Artifact) error {
	return s.base.PutArtifact(ctx, artifact)
}

func (s *failingCompleteStore) GetArtifact(ctx context.Context, artifactID string) (*Artifact, error) {
	return s.base.GetArtifact(ctx, artifactID)
}

func (s *failingCompleteStore) ListArtifacts(ctx context.Context) ([]*Artifact, error) {
	return s.base.ListArtifacts(ctx)
}

func (s *failingCompleteStore) CreateSharedArtifact(ctx context.Context, artifact *SharedArtifact) error {
	return s.base.CreateSharedArtifact(ctx, artifact)
}

func (s *failingCompleteStore) GetSharedArtifact(ctx context.Context, artifactID string) (*SharedArtifact, error) {
	return s.base.GetSharedArtifact(ctx, artifactID)
}

func (s *failingCompleteStore) ListSharedArtifacts(ctx context.Context) ([]*SharedArtifact, error) {
	return s.base.ListSharedArtifacts(ctx)
}

func (s *failingCompleteStore) UpdateSharedArtifact(ctx context.Context, artifact *SharedArtifact) error {
	return s.base.UpdateSharedArtifact(ctx, artifact)
}

func TestServiceCompileDelegatesAndAudits(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	compiler := &compileRecorder{
		result: &CompileResult{
			ArtifactRef: "report://draft/performance",
			ReportSpec:  json.RawMessage(`{"kind":"reportSpec","version":1}`),
			Diagnostics: []Diagnostic{{Code: "previewOnly", Severity: "warning", Message: "preview compile"}},
		},
	}
	audit := &auditRecorder{}
	svc := New(Options{
		Compiler: compiler,
		Store:    NewStoreAdapter(reportmemory.New()),
		Audit:    audit,
		Now:      func() time.Time { return now },
		NewID:    func() string { return "job-fixed" },
	})
	ctx := authsvc.InjectUser(context.Background(), "user-123")

	result, err := svc.Compile(ctx, &CompileRequest{
		ArtifactRef: "report://draft/performance",
		SourceKind:  "draft",
		Document:    json.RawMessage(`{"kind":"reportDocument","id":"performance"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "report://draft/performance", result.ArtifactRef)
	require.JSONEq(t, `{"kind":"reportDocument","id":"performance"}`, string(compiler.request.Document))
	require.Equal(t, now, result.CompiledAt)
	require.Len(t, audit.events, 1)
	require.Equal(t, "report.compile", audit.events[0].EventType)
	require.Equal(t, "user-123", audit.events[0].ActorID)
}

func TestServiceExportLifecycleScopesArtifactsToOwner(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 30, 0, 0, time.UTC)
	fs := afs.New()
	root := "file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "scratchpad", "${userID}"))
	afsscratchpad.Register(
		afsscratchpad.WithAFS(fs),
		afsscratchpad.WithRootURI(root),
		afsscratchpad.WithAllowedTargetSchemes("file"),
	)
	scratch := afsscratchpad.New(
		afsscratchpad.WithAFS(fs),
		afsscratchpad.WithRootURI(root),
		afsscratchpad.WithAllowedTargetSchemes("file"),
	)
	audit := &auditRecorder{}
	svc := New(Options{
		Store:        NewStoreAdapter(reportmemory.New()),
		Audit:        audit,
		Scratchpad:   scratch,
		ScratchpadFS: fs,
		Now:          func() time.Time { return now },
		NewID: func() string {
			if len(audit.events) == 0 {
				return "job-1"
			}
			return "artifact-1"
		},
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	job, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validTestReportPrintJSON()),
	})
	require.NoError(t, err)
	require.Equal(t, JobStatusQueued, job.Status)
	require.Equal(t, "owner-1", job.OwnerID)

	running, err := svc.StartExport(context.Background(), job.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusRunning, running.Status)
	require.NotNil(t, running.StartedAt)

	completed, err := svc.CompleteExport(context.Background(), &CompleteExportRequest{
		JobID:       job.JobID,
		Data:        []byte("%PDF-1.7"),
		Diagnostics: []Diagnostic{{Code: "rendered", Severity: "info", Message: "pdf rendered"}},
	})
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, completed.Status)
	require.Equal(t, "artifact-1", completed.ArtifactID)

	gotStatus, err := svc.GetExportStatus(ownerCtx, job.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, gotStatus.Status)

	artifact, err := svc.GetArtifact(ownerCtx, completed.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, ExportFormatPDF, artifact.Format)
	require.Equal(t, "application/pdf", artifact.ContentType)
	require.Equal(t, "scratchpad://artifact/artifact-1", artifact.SourceURL)
	require.Equal(t, []byte("%PDF-1.7"), artifact.Data)

	otherCtx := authsvc.InjectUser(context.Background(), "owner-2")
	_, err = svc.GetExportStatus(otherCtx, job.JobID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = svc.GetArtifact(otherCtx, completed.ArtifactID)
	require.ErrorIs(t, err, ErrNotFound)

	require.Len(t, audit.events, 2)
	require.Equal(t, "report.export.submit", audit.events[0].EventType)
	require.Equal(t, "report.export.complete", audit.events[1].EventType)
}

func TestServiceCompleteExportPublishesScratchpadArtifact(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 30, 0, 0, time.UTC)
	fs := afs.New()
	root := "file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "scratchpad", "${userID}"))
	afsscratchpad.Register(
		afsscratchpad.WithAFS(fs),
		afsscratchpad.WithRootURI(root),
		afsscratchpad.WithAllowedTargetSchemes("file"),
	)
	scratch := afsscratchpad.New(
		afsscratchpad.WithAFS(fs),
		afsscratchpad.WithRootURI(root),
		afsscratchpad.WithAllowedTargetSchemes("file"),
	)
	audit := &auditRecorder{}
	svc := New(Options{
		Store:        NewStoreAdapter(reportmemory.New()),
		Audit:        audit,
		Scratchpad:   scratch,
		ScratchpadFS: fs,
		Now:          func() time.Time { return now },
		NewID: func() string {
			if len(audit.events) == 0 {
				return "job-1"
			}
			return "artifact-1"
		},
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	job, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validTestReportPrintJSON()),
	})
	require.NoError(t, err)

	_, err = svc.StartExport(context.Background(), job.JobID)
	require.NoError(t, err)

	completed, err := svc.CompleteExport(context.Background(), &CompleteExportRequest{
		JobID: job.JobID,
		Data:  []byte("%PDF-1.7 scratchpad"),
	})
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, completed.Status)

	artifact, err := svc.GetArtifact(ownerCtx, completed.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, "scratchpad://artifact/artifact-1", artifact.SourceURL)
	require.Equal(t, []byte("%PDF-1.7 scratchpad"), artifact.Data)

	data, err := fs.DownloadWithURL(afsscratchpad.ContextWithUserID(context.Background(), "owner-1"), artifact.SourceURL)
	require.NoError(t, err)
	require.Equal(t, []byte("%PDF-1.7 scratchpad"), data)
}

func TestServiceSubmitExportResolvesReportSource(t *testing.T) {
	now := time.Date(2026, 6, 24, 14, 0, 0, 0, time.UTC)
	idCount := 0
	svc := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Now:   func() time.Time { return now },
		NewID: func() string {
			idCount++
			return "report-" + string(rune('0'+idCount))
		},
	})
	ctx := authsvc.InjectUser(context.Background(), "owner-1")

	saved, err := svc.SaveReport(ctx, &SaveReportRequest{
		ReportID:    "forecastingQ3",
		Title:       "Forecasting Q3",
		ReportSpec:  json.RawMessage(validTestReportSpecJSON()),
		ReportFill:  json.RawMessage(validTestReportFillJSON()),
		ReportPrint: json.RawMessage(validTestReportPrintJSON()),
		Metadata:    json.RawMessage(`{"workspaceId":"steward"}`),
	})
	require.NoError(t, err)

	job, err := svc.SubmitExport(ctx, &SubmitExportRequest{
		Format:         ExportFormatPDF,
		ConversationID: "conv-1",
		WorkspaceID:    "steward",
		Source: &ExportSource{
			Kind:     "report",
			ReportID: "forecastingQ3",
		},
	})
	require.NoError(t, err)
	require.Equal(t, ExportScopeSavedPayload, job.Scope)
	require.Equal(t, saved.ArtifactRef, job.ArtifactRef)
	require.JSONEq(t, validTestReportPrintJSON(), string(job.ReportPrint))
	require.JSONEq(t, validTestReportFillJSON(), string(job.ReportFill))
	require.JSONEq(t, validTestReportSpecJSON(), string(job.ReportSpec))
	require.JSONEq(t, `{"conversationId":"conv-1","workspaceId":"steward"}`, string(job.Metadata))
}

func TestServiceSubmitExportResolvesInlineSource(t *testing.T) {
	now := time.Date(2026, 6, 24, 14, 0, 0, 0, time.UTC)
	idCount := 0
	svc := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Now:   func() time.Time { return now },
		NewID: func() string {
			idCount++
			return "inline-" + string(rune('0'+idCount))
		},
	})
	ctx := authsvc.InjectUser(context.Background(), "owner-1")

	job, err := svc.SubmitExport(ctx, &SubmitExportRequest{
		Format:         ExportFormatPDF,
		ConversationID: "conv-inline",
		WorkspaceID:    "steward",
		Source: &ExportSource{
			Kind:        "inline",
			ArtifactRef: "report://inline/test",
			ReportSpec:  json.RawMessage(validTestReportSpecJSON()),
			ReportFill:  json.RawMessage(validTestReportFillJSON()),
			ReportPrint: json.RawMessage(validTestReportPrintJSON()),
			Metadata:    json.RawMessage(`{"source":"inline"}`),
		},
	})
	require.NoError(t, err)
	require.Equal(t, ExportScopeDraft, job.Scope)
	require.Equal(t, "report://inline/test", job.ArtifactRef)
	require.JSONEq(t, validTestReportPrintJSON(), string(job.ReportPrint))
	require.JSONEq(t, validTestReportFillJSON(), string(job.ReportFill))
	require.JSONEq(t, validTestReportSpecJSON(), string(job.ReportSpec))
	require.JSONEq(t, `{"conversationId":"conv-inline","workspaceId":"steward","source":"inline"}`, string(job.Metadata))
}

func TestServiceSubmitExportResolvesPresetSource(t *testing.T) {
	now := time.Date(2026, 6, 24, 14, 0, 0, 0, time.UTC)
	idCount := 0
	svc := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Now:   func() time.Time { return now },
		NewID: func() string {
			idCount++
			return "preset-" + string(rune('0'+idCount))
		},
	})
	ctx := authsvc.InjectUser(context.Background(), "owner-1")

	job, err := svc.SubmitExport(ctx, &SubmitExportRequest{
		Format:         ExportFormatPDF,
		ConversationID: "conv-preset",
		WorkspaceID:    "steward",
		Source: &ExportSource{
			Kind:        "preset",
			WindowKey:   "metricReportBuilder",
			PresetID:    "performance_inventory_brief",
			ReportSpec:  json.RawMessage(validTestReportSpecJSON()),
			ReportFill:  json.RawMessage(validTestReportFillJSON()),
			ReportPrint: json.RawMessage(validTestReportPrintJSON()),
			Metadata:    json.RawMessage(`{"source":"preset-runtime"}`),
		},
	})
	require.NoError(t, err)
	require.Equal(t, ExportScopeDraft, job.Scope)
	require.Equal(t, "report://preset/metricReportBuilder/performance_inventory_brief", job.ArtifactRef)
	require.JSONEq(t, validTestReportPrintJSON(), string(job.ReportPrint))
	require.JSONEq(t, validTestReportFillJSON(), string(job.ReportFill))
	require.JSONEq(t, validTestReportSpecJSON(), string(job.ReportSpec))
	require.JSONEq(t, `{"conversationId":"conv-preset","workspaceId":"steward","source":"preset-runtime","sourceKind":"preset","windowKey":"metricReportBuilder","presetId":"performance_inventory_brief"}`, string(job.Metadata))
}

func TestServiceSubmitExportPresetSourceRequiresMaterializedArtifacts(t *testing.T) {
	svc := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
	})
	ctx := authsvc.InjectUser(context.Background(), "owner-1")

	_, err := svc.SubmitExport(ctx, &SubmitExportRequest{
		Format: ExportFormatPDF,
		Source: &ExportSource{
			Kind:      "preset",
			WindowKey: "metricReportBuilder",
			PresetID:  "performance_inventory_brief",
		},
	})
	require.EqualError(t, err, "reporting export: preset source requires a materialized reportPrint for pdf export")
}

func TestServiceRunExportRehydratesTokensFromTokenProvider(t *testing.T) {
	now := time.Date(2026, 6, 24, 14, 30, 0, 0, time.UTC)
	exporter := &authCaptureExporter{}
	provider := &staticTokenProvider{
		tokensByKey: map[tokenctx.Key]*scyauth.Token{
			{Subject: "owner-1", Provider: "oauth"}: {
				Token: oauth2.Token{
					AccessToken: "access-token-1",
				},
				IDToken: "id-token-1",
			},
		},
	}
	idCount := 0
	svc := New(Options{
		Exporter:      exporter,
		Store:         NewStoreAdapter(reportmemory.New()),
		TokenProvider: provider,
		Now:           func() time.Time { return now },
		NewID: func() string {
			idCount++
			if idCount == 1 {
				return "job-1"
			}
			return "artifact-1"
		},
	})
	submitCtx := authsvc.InjectUser(context.Background(), "owner-1")
	submitCtx = authsvc.InjectTokens(submitCtx, &scyauth.Token{
		Token: oauth2.Token{
			AccessToken: "access-token-1",
		},
		IDToken: "id-token-1",
	})
	job, err := svc.SubmitExport(submitCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/auth",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)
	require.Contains(t, job.AuthContextRef, "actor=owner-1")
	require.Contains(t, job.AuthContextRef, "access=true")
	require.Contains(t, job.AuthContextRef, "id=true")

	completed, err := svc.RunExport(context.Background(), job.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, completed.Status)
	require.Len(t, exporter.requests, 1)
	require.Equal(t, "owner-1", exporter.actorIDs[0])
	require.Equal(t, "access-token-1", exporter.accessTokens[0])
	require.Equal(t, "id-token-1", exporter.idTokens[0])
}

func TestServiceListExportJobsScopesAndFiltersByOwner(t *testing.T) {
	now := time.Date(2026, 6, 13, 18, 0, 0, 0, time.UTC)
	timestamps := []time.Time{
		now,
		now.Add(1 * time.Minute),
		now.Add(2 * time.Minute),
		now.Add(3 * time.Minute),
		now.Add(4 * time.Minute),
	}
	timeIndex := 0
	idCounter := 0
	svc := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Now: func() time.Time {
			if timeIndex >= len(timestamps) {
				return timestamps[len(timestamps)-1]
			}
			current := timestamps[timeIndex]
			timeIndex++
			return current
		},
		NewID: func() string {
			idCounter++
			switch idCounter {
			case 1:
				return "job-1"
			case 2:
				return "job-2"
			case 3:
				return "job-3"
			default:
				return "artifact-1"
			}
		},
	})

	ownerOneCtx := authsvc.InjectUser(context.Background(), "owner-1")
	ownerTwoCtx := authsvc.InjectUser(context.Background(), "owner-2")

	firstJob, err := svc.SubmitExport(ownerOneCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)

	secondJob, err := svc.SubmitExport(ownerTwoCtx, &SubmitExportRequest{
		ArtifactRef: "report://saved-view/performance",
		Format:      ExportFormatCSV,
		Scope:       ExportScopeSavedView,
		ReportFill:  json.RawMessage(validTestReportFillJSON()),
	})
	require.NoError(t, err)

	thirdJob, err := svc.SubmitExport(ownerOneCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeSavedPayload,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)

	_, err = svc.StartExport(context.Background(), thirdJob.JobID)
	require.NoError(t, err)

	ownerOneJobs, err := svc.ListExportJobs(ownerOneCtx, nil)
	require.NoError(t, err)
	require.Equal(t, 2, ownerOneJobs.TotalCount)
	require.Len(t, ownerOneJobs.Jobs, 2)
	require.Equal(t, thirdJob.JobID, ownerOneJobs.Jobs[0].JobID)
	require.Equal(t, JobStatusRunning, ownerOneJobs.Jobs[0].Status)
	require.Equal(t, firstJob.JobID, ownerOneJobs.Jobs[1].JobID)
	require.Equal(t, JobStatusQueued, ownerOneJobs.Jobs[1].Status)

	filtered, err := svc.ListExportJobs(ownerOneCtx, &ListExportJobsInput{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		Status:      JobStatusQueued,
	})
	require.NoError(t, err)
	require.Equal(t, 1, filtered.TotalCount)
	require.Len(t, filtered.Jobs, 1)
	require.Equal(t, firstJob.JobID, filtered.Jobs[0].JobID)

	limited, err := svc.ListExportJobs(ownerOneCtx, &ListExportJobsInput{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, 2, limited.TotalCount)
	require.Len(t, limited.Jobs, 1)
	require.Equal(t, thirdJob.JobID, limited.Jobs[0].JobID)

	ownerTwoJobs, err := svc.ListExportJobs(ownerTwoCtx, nil)
	require.NoError(t, err)
	require.Equal(t, 1, ownerTwoJobs.TotalCount)
	require.Len(t, ownerTwoJobs.Jobs, 1)
	require.Equal(t, secondJob.JobID, ownerTwoJobs.Jobs[0].JobID)

	_, err = svc.ListExportJobs(context.Background(), nil)
	require.EqualError(t, err, "reporting export listing: effective user id is required")
}

func TestServiceListExportArtifactsScopesAndFiltersByOwner(t *testing.T) {
	now := time.Date(2026, 6, 13, 19, 0, 0, 0, time.UTC)
	timestamps := []time.Time{
		now,
		now.Add(1 * time.Minute),
		now.Add(2 * time.Minute),
		now.Add(3 * time.Minute),
		now.Add(4 * time.Minute),
		now.Add(5 * time.Minute),
		now.Add(6 * time.Minute),
		now.Add(7 * time.Minute),
	}
	timeIndex := 0
	idCounter := 0
	exporter := &queuedExportRecorder{}
	svc := New(Options{
		Exporter: exporter,
		Store:    NewStoreAdapter(reportmemory.New()),
		Now: func() time.Time {
			if timeIndex >= len(timestamps) {
				return timestamps[len(timestamps)-1]
			}
			current := timestamps[timeIndex]
			timeIndex++
			return current
		},
		NewID: func() string {
			idCounter++
			switch idCounter {
			case 1:
				return "job-1"
			case 2:
				return "artifact-1"
			case 3:
				return "job-2"
			case 4:
				return "artifact-2"
			case 5:
				return "job-3"
			case 6:
				return "artifact-3"
			default:
				return "artifact-extra"
			}
		},
	})

	ownerOneCtx := authsvc.InjectUser(context.Background(), "owner-1")
	ownerTwoCtx := authsvc.InjectUser(context.Background(), "owner-2")

	firstJob, err := svc.SubmitExport(ownerOneCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)
	_, err = svc.RunExport(context.Background(), firstJob.JobID)
	require.NoError(t, err)

	secondJob, err := svc.SubmitExport(ownerTwoCtx, &SubmitExportRequest{
		ArtifactRef: "report://saved-view/performance",
		Format:      ExportFormatCSV,
		Scope:       ExportScopeSavedView,
		ReportFill:  json.RawMessage(validRenderableTestReportFillJSON()),
	})
	require.NoError(t, err)
	_, err = svc.RunExport(context.Background(), secondJob.JobID)
	require.NoError(t, err)

	thirdJob, err := svc.SubmitExport(ownerOneCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatXLSX,
		Scope:       ExportScopeSavedPayload,
		ReportFill:  json.RawMessage(validRenderableTestReportFillJSON()),
	})
	require.NoError(t, err)
	_, err = svc.RunExport(context.Background(), thirdJob.JobID)
	require.NoError(t, err)

	ownerOneArtifacts, err := svc.ListExportArtifacts(ownerOneCtx, nil)
	require.NoError(t, err)
	require.Equal(t, 2, ownerOneArtifacts.TotalCount)
	require.Len(t, ownerOneArtifacts.Artifacts, 2)
	require.Equal(t, thirdJob.JobID, ownerOneArtifacts.Artifacts[0].JobID)
	require.Equal(t, firstJob.JobID, ownerOneArtifacts.Artifacts[1].JobID)

	filtered, err := svc.ListExportArtifacts(ownerOneCtx, &ListExportArtifactsInput{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		JobID:       firstJob.JobID,
	})
	require.NoError(t, err)
	require.Equal(t, 1, filtered.TotalCount)
	require.Len(t, filtered.Artifacts, 1)
	require.Equal(t, firstJob.JobID, filtered.Artifacts[0].JobID)
	require.Equal(t, ExportFormatPDF, filtered.Artifacts[0].Format)

	limited, err := svc.ListExportArtifacts(ownerOneCtx, &ListExportArtifactsInput{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, 2, limited.TotalCount)
	require.Len(t, limited.Artifacts, 1)
	require.Equal(t, thirdJob.JobID, limited.Artifacts[0].JobID)

	ownerTwoArtifacts, err := svc.ListExportArtifacts(ownerTwoCtx, nil)
	require.NoError(t, err)
	require.Equal(t, 1, ownerTwoArtifacts.TotalCount)
	require.Len(t, ownerTwoArtifacts.Artifacts, 1)
	require.Equal(t, secondJob.JobID, ownerTwoArtifacts.Artifacts[0].JobID)

	_, err = svc.ListExportArtifacts(context.Background(), nil)
	require.EqualError(t, err, "reporting artifact listing: effective user id is required")
}

func TestServiceListSharedArtifactsScopesAndFiltersByOwner(t *testing.T) {
	now := time.Date(2026, 6, 24, 14, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	svc := New(Options{
		Store: store,
		Now:   func() time.Time { return now },
	})

	ownerOneCtx := authsvc.InjectUser(context.Background(), "owner-1")
	ownerTwoCtx := authsvc.InjectUser(context.Background(), "owner-2")

	require.NoError(t, store.CreateSharedArtifact(context.Background(), &SharedArtifact{
		ArtifactID:       "shared-1",
		ArtifactRef:      "reportBuilder.savedView://saved_view_capacity_q3",
		OwnerID:          "owner-1",
		OwnerRef:         "user://owner-1",
		Kind:             "reportBuilder.savedView",
		Lifecycle:        "draft",
		Version:          4,
		ReportID:         "capacityQ3",
		Title:            "Capacity Q3 Saved View",
		SourceArtifactID: "saved_view_capacity_q3",
		CreatedAt:        now,
	}))
	require.NoError(t, store.CreateSharedArtifact(context.Background(), &SharedArtifact{
		ArtifactID:       "shared-2",
		ArtifactRef:      "reportBuilder.publishedSnapshot://published_snapshot_capacity_q3",
		OwnerID:          "owner-1",
		OwnerRef:         "user://owner-1",
		Kind:             "reportBuilder.publishedSnapshot",
		Lifecycle:        "published",
		Version:          5,
		ReportID:         "capacityQ3",
		Title:            "Capacity Q3 Snapshot",
		SourceArtifactID: "published_snapshot_capacity_q3",
		CreatedAt:        now.Add(time.Minute),
	}))
	require.NoError(t, store.CreateSharedArtifact(context.Background(), &SharedArtifact{
		ArtifactID:       "shared-3",
		ArtifactRef:      "reportBuilder.savedView://saved_view_forecasting_q3",
		OwnerID:          "owner-2",
		OwnerRef:         "user://owner-2",
		Kind:             "reportBuilder.savedView",
		Lifecycle:        "draft",
		Version:          4,
		ReportID:         "forecastingQ3",
		Title:            "Forecasting Q3 Saved View",
		SourceArtifactID: "saved_view_forecasting_q3",
		CreatedAt:        now.Add(2 * time.Minute),
	}))

	result, err := svc.ListSharedArtifacts(ownerOneCtx, &ListSharedArtifactsInput{Limit: 10})
	require.NoError(t, err)
	require.Len(t, result.Artifacts, 2)
	require.Equal(t, "shared-2", result.Artifacts[0].ArtifactID)
	require.Equal(t, "shared-1", result.Artifacts[1].ArtifactID)

	filtered, err := svc.ListSharedArtifacts(ownerOneCtx, &ListSharedArtifactsInput{
		ReportID:  "capacityQ3",
		Kind:      "reportBuilder.publishedSnapshot",
		Lifecycle: "published",
	})
	require.NoError(t, err)
	require.Len(t, filtered.Artifacts, 1)
	require.Equal(t, "shared-2", filtered.Artifacts[0].ArtifactID)

	got, err := svc.GetSharedArtifact(ownerOneCtx, "shared-1")
	require.NoError(t, err)
	require.Equal(t, "shared-1", got.ArtifactID)

	_, err = svc.GetSharedArtifact(ownerTwoCtx, "shared-1")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestServiceListExportArtifactsSkipsPartialCompletionArtifacts(t *testing.T) {
	now := time.Date(2026, 6, 13, 19, 30, 0, 0, time.UTC)
	store := &failingCompleteStore{
		base:      NewMemoryStore(),
		failJobID: "job-1",
	}
	idCounter := 0
	svc := New(Options{
		Store: store,
		Now:   func() time.Time { return now },
		NewID: func() string {
			idCounter++
			if idCounter == 1 {
				return "job-1"
			}
			return "artifact-1"
		},
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	job, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/partial",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)

	_, err = svc.StartExport(context.Background(), job.JobID)
	require.NoError(t, err)

	_, err = svc.CompleteExport(context.Background(), &CompleteExportRequest{
		JobID:       job.JobID,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-partial"),
	})
	require.EqualError(t, err, "completion update failed")

	artifacts, err := svc.ListExportArtifacts(ownerCtx, nil)
	require.NoError(t, err)
	require.Equal(t, 0, artifacts.TotalCount)
	require.Empty(t, artifacts.Artifacts)

	_, err = svc.GetArtifact(ownerCtx, "artifact-1")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestServiceGetArtifactHidesExpiredArtifacts(t *testing.T) {
	now := time.Date(2026, 6, 13, 20, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	svc := New(Options{
		Store: store,
		Now:   func() time.Time { return now },
	})

	expiredCompletedAt := now.Add(-2 * time.Hour)
	require.NoError(t, store.CreateJob(context.Background(), &ExportJob{
		JobID:       "job-expired",
		ArtifactRef: "report://draft/expired",
		OwnerID:     "owner-1",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		Status:      JobStatusSucceeded,
		ArtifactID:  "artifact-expired",
		SubmittedAt: now.Add(-3 * time.Hour),
		CompletedAt: &expiredCompletedAt,
	}))
	require.NoError(t, store.PutArtifact(context.Background(), &Artifact{
		ArtifactID:   "artifact-expired",
		JobID:        "job-expired",
		ArtifactRef:  "report://draft/expired",
		OwnerID:      "owner-1",
		Format:       ExportFormatPDF,
		ContentType:  "application/pdf",
		Data:         []byte("%PDF-expired"),
		CreatedAt:    now.Add(-2 * time.Hour),
		RetentionTTL: time.Hour,
	}))

	liveCompletedAt := now.Add(-10 * time.Minute)
	require.NoError(t, store.CreateJob(context.Background(), &ExportJob{
		JobID:       "job-live",
		ArtifactRef: "report://draft/live",
		OwnerID:     "owner-1",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		Status:      JobStatusSucceeded,
		ArtifactID:  "artifact-live",
		SubmittedAt: now.Add(-15 * time.Minute),
		CompletedAt: &liveCompletedAt,
	}))
	require.NoError(t, store.PutArtifact(context.Background(), &Artifact{
		ArtifactID:   "artifact-live",
		JobID:        "job-live",
		ArtifactRef:  "report://draft/live",
		OwnerID:      "owner-1",
		Format:       ExportFormatPDF,
		ContentType:  "application/pdf",
		Data:         []byte("%PDF-live"),
		CreatedAt:    now.Add(-10 * time.Minute),
		RetentionTTL: 2 * time.Hour,
	}))

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	_, err := svc.GetArtifact(ownerCtx, "artifact-expired")
	require.ErrorIs(t, err, ErrNotFound)

	live, err := svc.GetArtifact(ownerCtx, "artifact-live")
	require.NoError(t, err)
	require.Equal(t, []byte("%PDF-live"), live.Data)
}

func TestServiceListExportArtifactsSkipsExpiredArtifacts(t *testing.T) {
	now := time.Date(2026, 6, 13, 20, 30, 0, 0, time.UTC)
	store := NewMemoryStore()
	svc := New(Options{
		Store: store,
		Now:   func() time.Time { return now },
	})

	expiredCompletedAt := now.Add(-2 * time.Hour)
	require.NoError(t, store.CreateJob(context.Background(), &ExportJob{
		JobID:       "job-expired",
		ArtifactRef: "report://draft/expired",
		OwnerID:     "owner-1",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		Status:      JobStatusSucceeded,
		ArtifactID:  "artifact-expired",
		SubmittedAt: now.Add(-3 * time.Hour),
		CompletedAt: &expiredCompletedAt,
	}))
	require.NoError(t, store.PutArtifact(context.Background(), &Artifact{
		ArtifactID:   "artifact-expired",
		JobID:        "job-expired",
		ArtifactRef:  "report://draft/expired",
		OwnerID:      "owner-1",
		Format:       ExportFormatPDF,
		ContentType:  "application/pdf",
		Data:         []byte("%PDF-expired"),
		CreatedAt:    now.Add(-2 * time.Hour),
		RetentionTTL: time.Hour,
	}))

	liveCompletedAt := now.Add(-10 * time.Minute)
	require.NoError(t, store.CreateJob(context.Background(), &ExportJob{
		JobID:       "job-live",
		ArtifactRef: "report://draft/live",
		OwnerID:     "owner-1",
		Format:      ExportFormatCSV,
		Scope:       ExportScopeDraft,
		Status:      JobStatusSucceeded,
		ArtifactID:  "artifact-live",
		SubmittedAt: now.Add(-15 * time.Minute),
		CompletedAt: &liveCompletedAt,
	}))
	require.NoError(t, store.PutArtifact(context.Background(), &Artifact{
		ArtifactID:   "artifact-live",
		JobID:        "job-live",
		ArtifactRef:  "report://draft/live",
		OwnerID:      "owner-1",
		Format:       ExportFormatCSV,
		ContentType:  "text/csv",
		Data:         []byte("a,b\n1,2\n"),
		CreatedAt:    now.Add(-10 * time.Minute),
		RetentionTTL: 2 * time.Hour,
	}))

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	result, err := svc.ListExportArtifacts(ownerCtx, nil)
	require.NoError(t, err)
	require.Equal(t, 1, result.TotalCount)
	require.Len(t, result.Artifacts, 1)
	require.Equal(t, "artifact-live", result.Artifacts[0].ArtifactID)
}

func TestServiceGetArtifactUsesCompletedAtWhenCreatedAtMissing(t *testing.T) {
	now := time.Date(2026, 6, 13, 21, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	svc := New(Options{
		Store: store,
		Now:   func() time.Time { return now },
	})

	completedAt := now.Add(-30 * time.Minute)
	require.NoError(t, store.CreateJob(context.Background(), &ExportJob{
		JobID:       "job-fallback",
		ArtifactRef: "report://draft/fallback",
		OwnerID:     "owner-1",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		Status:      JobStatusSucceeded,
		ArtifactID:  "artifact-fallback",
		SubmittedAt: now.Add(-45 * time.Minute),
		CompletedAt: &completedAt,
	}))
	require.NoError(t, store.PutArtifact(context.Background(), &Artifact{
		ArtifactID:   "artifact-fallback",
		JobID:        "job-fallback",
		ArtifactRef:  "report://draft/fallback",
		OwnerID:      "owner-1",
		Format:       ExportFormatPDF,
		ContentType:  "application/pdf",
		Data:         []byte("%PDF-fallback"),
		RetentionTTL: time.Hour,
	}))

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	artifact, err := svc.GetArtifact(ownerCtx, "artifact-fallback")
	require.NoError(t, err)
	require.Equal(t, []byte("%PDF-fallback"), artifact.Data)
}

func TestServiceGetArtifactHidesPositiveTTLArtifactWithoutAnyTimestamp(t *testing.T) {
	now := time.Date(2026, 6, 13, 21, 15, 0, 0, time.UTC)
	store := NewMemoryStore()
	svc := New(Options{
		Store: store,
		Now:   func() time.Time { return now },
	})

	require.NoError(t, store.CreateJob(context.Background(), &ExportJob{
		JobID:       "job-no-time",
		ArtifactRef: "report://draft/no-time",
		OwnerID:     "owner-1",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		Status:      JobStatusSucceeded,
		ArtifactID:  "artifact-no-time",
		SubmittedAt: now.Add(-5 * time.Minute),
	}))
	require.NoError(t, store.PutArtifact(context.Background(), &Artifact{
		ArtifactID:   "artifact-no-time",
		JobID:        "job-no-time",
		ArtifactRef:  "report://draft/no-time",
		OwnerID:      "owner-1",
		Format:       ExportFormatPDF,
		ContentType:  "application/pdf",
		Data:         []byte("%PDF-no-time"),
		RetentionTTL: time.Hour,
	}))

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	_, err := svc.GetArtifact(ownerCtx, "artifact-no-time")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestServiceSubmitExportAcceptsCanonicalEnvelope(t *testing.T) {
	svc := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Now:   func() time.Time { return time.Unix(0, 0).UTC() },
		NewID: func() string { return "job-envelope" },
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")

	job, err := svc.SubmitExport(ctx, &SubmitExportRequest{
		ReportExportRequest: validTestReportExportRequestEnvelope(),
	})
	require.NoError(t, err)
	require.Equal(t, "job-envelope", job.JobID)
	require.Equal(t, "reportBuilder.savedReportPayload://rbreport_forecasting_q3", job.ArtifactRef)
	require.Equal(t, ExportFormatPDF, job.Format)
	require.Equal(t, ExportScopeSavedPayload, job.Scope)
	require.JSONEq(t, validTestReportSpecJSON(), string(job.ReportSpec))
	require.JSONEq(t, validTestReportFillJSON(), string(job.ReportFill))
	require.JSONEq(t, validTestReportPrintJSON(), string(job.ReportPrint))
}

func TestServiceSubmitExportPersistsMetadataAndDerivedAuthContextRef(t *testing.T) {
	svc := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Now:   func() time.Time { return time.Unix(0, 0).UTC() },
		NewID: func() string { return "job-metadata" },
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")
	envelope := validTestReportExportRequestEnvelope()
	envelope.Metadata = json.RawMessage(`{"conversationId":"conv-123","workspaceId":"steward","renderHints":{"theme":"print"}}`)

	job, err := svc.SubmitExport(ctx, &SubmitExportRequest{
		ReportExportRequest: envelope,
	})
	require.NoError(t, err)
	require.Equal(t, "conv-123", job.ConversationID)
	require.Equal(t, "steward", job.WorkspaceID)
	require.JSONEq(t, `{"conversationId":"conv-123","workspaceId":"steward","renderHints":{"theme":"print"}}`, string(job.Metadata))
	require.Contains(t, job.AuthContextRef, "actor=user-1")
}

func TestServiceSubmitExportSurfacesDuplicateJobIDs(t *testing.T) {
	svc := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Now:   func() time.Time { return time.Unix(0, 0).UTC() },
		NewID: func() string { return "job-duplicate" },
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")

	_, err := svc.SubmitExport(ctx, &SubmitExportRequest{
		ReportExportRequest: validTestReportExportRequestEnvelope(),
	})
	require.NoError(t, err)

	_, err = svc.SubmitExport(ctx, &SubmitExportRequest{
		ReportExportRequest: validTestReportExportRequestEnvelope(),
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAlreadyExists)
}

func TestServiceCompleteExportMarksJobFailedOnArtifactIDCollision(t *testing.T) {
	now := time.Date(2026, 6, 13, 15, 50, 0, 0, time.UTC)
	store := NewMemoryStore()
	idCounter := 0
	svc := New(Options{
		Store: store,
		Now:   func() time.Time { return now },
		NewID: func() string {
			idCounter++
			switch idCounter {
			case 1:
				return "job-1"
			default:
				return "artifact-1"
			}
		},
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	job, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/collision",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)

	require.NoError(t, store.PutArtifact(context.Background(), &Artifact{
		ArtifactID:  "artifact-1",
		JobID:       "other-job",
		ArtifactRef: "report://draft/other",
		OwnerID:     "owner-1",
		Format:      ExportFormatPDF,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-existing"),
		CreatedAt:   now,
	}))

	_, err = svc.StartExport(context.Background(), job.JobID)
	require.NoError(t, err)

	failed, err := svc.CompleteExport(context.Background(), &CompleteExportRequest{
		JobID:       job.JobID,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-collision"),
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAlreadyExists)
	require.NotNil(t, failed)
	require.Equal(t, JobStatusFailed, failed.Status)
	require.Contains(t, failed.Error, "artifact artifact-1 already exists")

	status, statusErr := svc.GetExportStatus(ownerCtx, job.JobID)
	require.NoError(t, statusErr)
	require.Equal(t, JobStatusFailed, status.Status)
	require.Contains(t, status.Error, "artifact artifact-1 already exists")

	artifact, artifactErr := store.GetArtifact(context.Background(), "artifact-1")
	require.NoError(t, artifactErr)
	require.Equal(t, []byte("%PDF-existing"), artifact.Data)
}

func TestServiceValidatesExportArtifactsByFormat(t *testing.T) {
	svc := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Now:   func() time.Time { return time.Unix(0, 0).UTC() },
		NewID: func() string { return "job-1" },
	})
	ctx := authsvc.InjectUser(context.Background(), "user-1")

	_, err := svc.SubmitExport(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
	})
	require.EqualError(t, err, "reporting export: reportPrint is required for pdf export")

	_, err = svc.SubmitExport(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatCSV,
		Scope:       ExportScopeDraft,
	})
	require.EqualError(t, err, "reporting export: reportFill is required for tabular export")

	_, err = svc.SubmitExport(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(`{"version":1,"kind":"reportPrint"}`),
	})
	require.EqualError(t, err, "reporting export: invalid reportPrint: missing specVersion")

	_, err = svc.SubmitExport(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatCSV,
		Scope:       ExportScopeDraft,
		ReportFill:  json.RawMessage(`{"version":1,"kind":"reportFill"}`),
	})
	require.EqualError(t, err, "reporting export: invalid reportFill: missing specVersion")

	_, err = svc.SubmitExport(ctx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatCSV,
		Scope:       ExportScopeDraft,
		ReportSpec:  json.RawMessage(validTestReportSpecJSON()),
		ReportFill:  json.RawMessage(validTestReportFillJSONWithSpecVersion(2)),
	})
	require.EqualError(t, err, "reporting export: reportFill specVersion 2 does not match reportSpec version 1")

	_, err = svc.SubmitExport(ctx, &SubmitExportRequest{
		ReportExportRequest: &ReportExportRequest{
			Version: 1,
			Kind:    "reportExportRequest",
			Target: ReportExportTarget{
				Format: ExportFormatPDF,
			},
			Source: ReportExportSource{
				From:        "savedPayload",
				ArtifactRef: "reportBuilder.savedReportPayload://rbreport_forecasting_q3",
				Title:       "Forecasting Q3",
			},
			ReportSpec:  json.RawMessage(validTestReportSpecJSON()),
			ReportFill:  json.RawMessage(validTestReportFillJSON()),
			ReportPrint: json.RawMessage(validTestReportPrintJSON()),
		},
	})
	require.EqualError(t, err, "reporting export: reportExportRequest source.payloadId is required for savedPayload exports")

	job, err := svc.SubmitExport(ctx, &SubmitExportRequest{
		ReportExportRequest: &ReportExportRequest{
			Version: 1,
			Kind:    "reportExportRequest",
			Target: ReportExportTarget{
				Format: ExportFormatPDF,
			},
			Source: ReportExportSource{
				From:             "preset",
				ArtifactKind:     "reportBuilder.reportTemplate",
				ArtifactRef:      "reportBuilder.reportTemplate://metricReportBuilder:performance_inventory_brief",
				Title:            "Performance Inventory Brief",
				SourceArtifactID: "performance_inventory_brief",
			},
			ReportSpec:  json.RawMessage(validTestReportSpecJSON()),
			ReportFill:  json.RawMessage(validTestReportFillJSON()),
			ReportPrint: json.RawMessage(validTestReportPrintJSON()),
		},
	})
	require.NoError(t, err)
	require.Equal(t, ExportScopeDraft, job.Scope)
	require.Equal(t, "reportBuilder.reportTemplate://metricReportBuilder:performance_inventory_brief", job.ArtifactRef)
}

func TestServiceRunExportUsesConfiguredExporter(t *testing.T) {
	now := time.Date(2026, 6, 13, 15, 0, 0, 0, time.UTC)
	exporter := &exportRecorder{
		result: &RenderResult{
			Data:         []byte("%PDF-runtime"),
			Diagnostics:  []Diagnostic{{Code: "rendered", Severity: "info", Message: "rendered successfully"}},
			RetentionTTL: 2 * time.Hour,
		},
	}
	idCounter := 0
	svc := New(Options{
		Exporter: exporter,
		Store:    NewStoreAdapter(reportmemory.New()),
		Now:      func() time.Time { return now },
		NewID: func() string {
			idCounter++
			if idCounter == 1 {
				return "job-1"
			}
			return "artifact-1"
		},
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	job, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef:    "report://draft/performance",
		Format:         ExportFormatPDF,
		Scope:          ExportScopeDraft,
		ConversationID: "conv-456",
		WorkspaceID:    "steward",
		ReportSpec:     json.RawMessage(validTestReportSpecJSON()),
		ReportFill:     json.RawMessage(validTestReportFillJSON()),
		ReportPrint:    json.RawMessage(validTestReportPrintJSON()),
		Metadata:       json.RawMessage(`{"conversationId":"conv-456","workspaceId":"steward","renderHints":{"page":"landscape"}}`),
	})
	require.NoError(t, err)

	completed, err := svc.RunExport(context.Background(), job.JobID)
	require.NoError(t, err)
	require.NotNil(t, completed)
	require.Equal(t, JobStatusSucceeded, completed.Status)
	require.Equal(t, "artifact-1", completed.ArtifactID)
	require.NotNil(t, exporter.request)
	require.Equal(t, ExportFormatPDF, exporter.request.Format)
	require.Equal(t, ExportScopeDraft, exporter.request.Scope)
	require.Equal(t, "owner-1", exporter.request.OwnerID)
	require.Equal(t, "conv-456", exporter.request.ConversationID)
	require.Equal(t, "steward", exporter.request.WorkspaceID)
	require.Contains(t, exporter.request.AuthContextRef, "actor=owner-1")
	require.JSONEq(t, `{"conversationId":"conv-456","workspaceId":"steward","renderHints":{"page":"landscape"}}`, string(exporter.request.Metadata))
	require.JSONEq(t, validTestReportPrintJSON(), string(exporter.request.ReportPrint))
	require.JSONEq(t, validTestReportFillJSON(), string(exporter.request.ReportFill))
	require.JSONEq(t, validTestReportSpecJSON(), string(exporter.request.ReportSpec))

	artifact, err := svc.GetArtifact(ownerCtx, completed.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, []byte("%PDF-runtime"), artifact.Data)
	require.Equal(t, "application/pdf", artifact.ContentType)
	require.Equal(t, 2*time.Hour, artifact.RetentionTTL)
}

func TestServiceRunExportMarksJobFailedOnExporterError(t *testing.T) {
	svc := New(Options{
		Exporter: &exportRecorder{err: errors.New("render failed")},
		Store:    NewStoreAdapter(reportmemory.New()),
		Now:      func() time.Time { return time.Date(2026, 6, 13, 15, 30, 0, 0, time.UTC) },
		NewID:    func() string { return "job-1" },
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	job, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validTestReportPrintJSON()),
	})
	require.NoError(t, err)

	failed, err := svc.RunExport(context.Background(), job.JobID)
	require.EqualError(t, err, "render failed")
	require.NotNil(t, failed)
	require.Equal(t, JobStatusFailed, failed.Status)
	require.Equal(t, "render failed", failed.Error)

	status, statusErr := svc.GetExportStatus(ownerCtx, job.JobID)
	require.NoError(t, statusErr)
	require.Equal(t, JobStatusFailed, status.Status)
	require.Equal(t, "render failed", status.Error)
}

func TestServiceRunExportReturnsFailedJobOnArtifactIDCollision(t *testing.T) {
	now := time.Date(2026, 6, 13, 15, 40, 0, 0, time.UTC)
	exporter := &exportRecorder{
		result: &RenderResult{
			ContentType: "application/pdf",
			Data:        []byte("%PDF-collision"),
		},
	}
	store := NewMemoryStore()
	idCounter := 0
	svc := New(Options{
		Exporter: exporter,
		Store:    store,
		Now:      func() time.Time { return now },
		NewID: func() string {
			idCounter++
			if idCounter == 1 {
				return "job-1"
			}
			return "artifact-1"
		},
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	job, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)

	require.NoError(t, store.PutArtifact(context.Background(), &Artifact{
		ArtifactID:  "artifact-1",
		JobID:       "other-job",
		ArtifactRef: "report://draft/other",
		OwnerID:     "owner-1",
		Format:      ExportFormatPDF,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-existing"),
		CreatedAt:   now,
	}))

	failed, err := svc.RunExport(context.Background(), job.JobID)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAlreadyExists)
	require.NotNil(t, failed)
	require.Equal(t, JobStatusFailed, failed.Status)
	require.Contains(t, failed.Error, "artifact artifact-1 already exists")

	status, statusErr := svc.GetExportStatus(ownerCtx, job.JobID)
	require.NoError(t, statusErr)
	require.Equal(t, JobStatusFailed, status.Status)
	require.Contains(t, status.Error, "artifact artifact-1 already exists")
}

func TestServiceRejectsFinalizingJobsThatAreNotRunning(t *testing.T) {
	now := time.Date(2026, 6, 13, 15, 45, 0, 0, time.UTC)
	idCounter := 0
	svc := New(Options{
		Store: NewStoreAdapter(reportmemory.New()),
		Now:   func() time.Time { return now },
		NewID: func() string {
			idCounter++
			switch idCounter {
			case 1:
				return "job-1"
			default:
				return "artifact-1"
			}
		},
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	job, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/finalization-guard",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)

	_, err = svc.CompleteExport(context.Background(), &CompleteExportRequest{
		JobID:       job.JobID,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-invalid"),
	})
	require.EqualError(t, err, "reporting export completion: job job-1 is not running")

	_, err = svc.FailExport(context.Background(), &FailExportRequest{
		JobID: job.JobID,
		Error: "queued job cannot fail directly",
	})
	require.EqualError(t, err, "reporting export failure: job job-1 is not running")

	started, err := svc.StartExport(context.Background(), job.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusRunning, started.Status)

	completed, err := svc.CompleteExport(context.Background(), &CompleteExportRequest{
		JobID:       job.JobID,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-valid"),
	})
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, completed.Status)

	_, err = svc.FailExport(context.Background(), &FailExportRequest{
		JobID: job.JobID,
		Error: "terminal jobs cannot be mutated",
	})
	require.EqualError(t, err, "reporting export failure: job job-1 is not running")

	_, err = svc.CompleteExport(context.Background(), &CompleteExportRequest{
		JobID:       job.JobID,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-duplicate"),
	})
	require.EqualError(t, err, "reporting export completion: job job-1 is not running")
}

func TestServiceRunQueuedExportsProcessesQueuedJobsInSubmittedOrder(t *testing.T) {
	now := time.Date(2026, 6, 13, 16, 0, 0, 0, time.UTC)
	exporter := &queuedExportRecorder{
		errors: map[string]error{
			"job-2": errors.New("render failed"),
		},
	}
	idCounter := 0
	svc := New(Options{
		Exporter: exporter,
		Store:    NewStoreAdapter(reportmemory.New()),
		Now: func() time.Time {
			current := now.Add(time.Duration(idCounter) * time.Minute)
			return current
		},
		NewID: func() string {
			idCounter++
			switch idCounter {
			case 1:
				return "job-1"
			case 2:
				return "job-2"
			case 3:
				return "job-3"
			case 4:
				return "artifact-1"
			case 5:
				return "artifact-2"
			default:
				return "artifact-extra"
			}
		},
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	_, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/one",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)
	_, err = svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/two",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)
	queuedThird, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/three",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)

	result, err := svc.RunQueuedExports(context.Background(), 2)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, result.ProcessedCount)
	require.Equal(t, 1, result.SucceededCount)
	require.Equal(t, 1, result.FailedCount)
	require.Len(t, result.Jobs, 2)
	require.Equal(t, "job-1", result.Jobs[0].JobID)
	require.Equal(t, JobStatusSucceeded, result.Jobs[0].Status)
	require.Equal(t, "job-2", result.Jobs[1].JobID)
	require.Equal(t, JobStatusFailed, result.Jobs[1].Status)
	require.Equal(t, "render failed", result.Jobs[1].Error)
	require.Len(t, exporter.requests, 2)
	require.Equal(t, "job-1", exporter.requests[0].JobID)
	require.Equal(t, "job-2", exporter.requests[1].JobID)

	status, err := svc.GetExportStatus(ownerCtx, queuedThird.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusQueued, status.Status)
}

func TestServiceRunQueuedExportsSkipsJobsClaimedByAnotherWorker(t *testing.T) {
	now := time.Date(2026, 6, 13, 16, 15, 0, 0, time.UTC)
	exporter := &queuedExportRecorder{}
	idCounter := 0
	baseStore := NewStoreAdapter(reportmemory.New())
	store := &claimRaceStore{
		base:       baseStore,
		claimJobID: "job-1",
		claimTime:  now.Add(5 * time.Second),
	}
	svc := New(Options{
		Exporter: exporter,
		Store:    store,
		Now: func() time.Time {
			return now.Add(time.Duration(idCounter) * time.Minute)
		},
		NewID: func() string {
			idCounter++
			switch idCounter {
			case 1:
				return "job-1"
			case 2:
				return "job-2"
			case 3:
				return "artifact-1"
			default:
				return "artifact-extra"
			}
		},
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	firstJob, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/claimed",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)
	secondJob, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/ready",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)

	result, err := svc.RunQueuedExports(context.Background(), 2)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ProcessedCount)
	require.Equal(t, 1, result.SucceededCount)
	require.Zero(t, result.FailedCount)
	require.Len(t, result.Jobs, 1)
	require.Equal(t, secondJob.JobID, result.Jobs[0].JobID)
	require.Equal(t, JobStatusSucceeded, result.Jobs[0].Status)
	require.Len(t, exporter.requests, 1)
	require.Equal(t, secondJob.JobID, exporter.requests[0].JobID)

	claimedStatus, err := svc.GetExportStatus(ownerCtx, firstJob.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusRunning, claimedStatus.Status)
	require.NotNil(t, claimedStatus.StartedAt)
}

func TestServiceRunQueuedExportsBackfillsPastClaimedJobsWithinLimit(t *testing.T) {
	now := time.Date(2026, 6, 13, 16, 20, 0, 0, time.UTC)
	exporter := &queuedExportRecorder{}
	idCounter := 0
	baseStore := NewStoreAdapter(reportmemory.New())
	store := &claimRaceStore{
		base:       baseStore,
		claimJobID: "job-1",
		claimTime:  now.Add(5 * time.Second),
	}
	svc := New(Options{
		Exporter: exporter,
		Store:    store,
		Now: func() time.Time {
			return now.Add(time.Duration(idCounter) * time.Minute)
		},
		NewID: func() string {
			idCounter++
			switch idCounter {
			case 1:
				return "job-1"
			case 2:
				return "job-2"
			case 3:
				return "job-3"
			case 4:
				return "artifact-1"
			case 5:
				return "artifact-2"
			default:
				return "artifact-extra"
			}
		},
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	firstJob, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/claimed",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)
	secondJob, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/two",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)
	thirdJob, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/three",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)

	result, err := svc.RunQueuedExports(context.Background(), 2)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, result.ProcessedCount)
	require.Equal(t, 2, result.SucceededCount)
	require.Zero(t, result.FailedCount)
	require.Len(t, result.Jobs, 2)
	require.Equal(t, secondJob.JobID, result.Jobs[0].JobID)
	require.Equal(t, thirdJob.JobID, result.Jobs[1].JobID)
	require.Len(t, exporter.requests, 2)
	require.Equal(t, secondJob.JobID, exporter.requests[0].JobID)
	require.Equal(t, thirdJob.JobID, exporter.requests[1].JobID)

	claimedStatus, err := svc.GetExportStatus(ownerCtx, firstJob.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusRunning, claimedStatus.Status)
}

func TestServiceRunQueuedExportsCountsCollisionFailuresAfterClaimSkips(t *testing.T) {
	now := time.Date(2026, 6, 13, 16, 27, 0, 0, time.UTC)
	exporter := &queuedExportRecorder{}
	idCounter := 0
	baseStore := NewMemoryStore()
	store := &claimRaceStore{
		base:       baseStore,
		claimJobID: "job-1",
		claimTime:  now.Add(5 * time.Second),
	}
	svc := New(Options{
		Exporter: exporter,
		Store:    store,
		Now: func() time.Time {
			return now.Add(time.Duration(idCounter) * time.Minute)
		},
		NewID: func() string {
			idCounter++
			switch idCounter {
			case 1:
				return "job-1"
			case 2:
				return "job-2"
			case 3:
				return "job-3"
			case 4:
				return "artifact-1"
			case 5:
				return "artifact-2"
			default:
				return "artifact-extra"
			}
		},
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	firstJob, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/claimed",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)
	secondJob, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/collision",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)
	thirdJob, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/success",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)

	require.NoError(t, baseStore.PutArtifact(context.Background(), &Artifact{
		ArtifactID:  "artifact-1",
		JobID:       "other-job",
		ArtifactRef: "report://draft/existing",
		OwnerID:     "owner-1",
		Format:      ExportFormatPDF,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-existing"),
		CreatedAt:   now,
	}))

	result, err := svc.RunQueuedExports(context.Background(), 2)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, result.ProcessedCount)
	require.Equal(t, 1, result.SucceededCount)
	require.Equal(t, 1, result.FailedCount)
	require.Len(t, result.Jobs, 2)
	require.Equal(t, secondJob.JobID, result.Jobs[0].JobID)
	require.Equal(t, JobStatusFailed, result.Jobs[0].Status)
	require.Contains(t, result.Jobs[0].Error, "artifact artifact-1 already exists")
	require.Equal(t, thirdJob.JobID, result.Jobs[1].JobID)
	require.Equal(t, JobStatusSucceeded, result.Jobs[1].Status)
	require.Len(t, exporter.requests, 2)
	require.Equal(t, secondJob.JobID, exporter.requests[0].JobID)
	require.Equal(t, thirdJob.JobID, exporter.requests[1].JobID)

	claimedStatus, err := svc.GetExportStatus(ownerCtx, firstJob.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusRunning, claimedStatus.Status)

	failedStatus, err := svc.GetExportStatus(ownerCtx, secondJob.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusFailed, failedStatus.Status)
	require.Contains(t, failedStatus.Error, "artifact artifact-1 already exists")

	succeededStatus, err := svc.GetExportStatus(ownerCtx, thirdJob.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, succeededStatus.Status)
}

func TestServiceRunQueuedExportsCountsFailedJobsAfterClaimSkips(t *testing.T) {
	now := time.Date(2026, 6, 13, 16, 25, 0, 0, time.UTC)
	exporter := &queuedExportRecorder{}
	idCounter := 0
	baseStore := NewMemoryStore()
	store := &claimRaceStore{
		base:       baseStore,
		claimJobID: "job-1",
		claimTime:  now.Add(5 * time.Second),
	}
	svc := New(Options{
		Exporter: exporter,
		Store:    store,
		Now: func() time.Time {
			return now.Add(time.Duration(idCounter) * time.Minute)
		},
		NewID: func() string {
			idCounter++
			switch idCounter {
			case 1:
				return "job-1"
			case 2:
				return "job-2"
			case 3:
				return "job-3"
			case 4:
				return "artifact-1"
			case 5:
				return "artifact-2"
			default:
				return "artifact-extra"
			}
		},
	})

	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	firstJob, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/claimed",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)
	secondJob, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/collision",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)
	thirdJob, err := svc.SubmitExport(ownerCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/ready",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)

	require.NoError(t, baseStore.PutArtifact(context.Background(), &Artifact{
		ArtifactID:  "artifact-1",
		JobID:       "other-job",
		ArtifactRef: "report://draft/existing",
		OwnerID:     "owner-1",
		Format:      ExportFormatPDF,
		ContentType: "application/pdf",
		Data:        []byte("%PDF-existing"),
		CreatedAt:   now,
	}))

	result, err := svc.RunQueuedExports(context.Background(), 2)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, result.ProcessedCount)
	require.Equal(t, 1, result.SucceededCount)
	require.Equal(t, 1, result.FailedCount)
	require.Len(t, result.Jobs, 2)
	require.Equal(t, secondJob.JobID, result.Jobs[0].JobID)
	require.Equal(t, JobStatusFailed, result.Jobs[0].Status)
	require.Contains(t, result.Jobs[0].Error, "artifact artifact-1 already exists")
	require.Equal(t, thirdJob.JobID, result.Jobs[1].JobID)
	require.Equal(t, JobStatusSucceeded, result.Jobs[1].Status)
	require.Len(t, exporter.requests, 2)
	require.Equal(t, secondJob.JobID, exporter.requests[0].JobID)
	require.Equal(t, thirdJob.JobID, exporter.requests[1].JobID)

	claimedStatus, err := svc.GetExportStatus(ownerCtx, firstJob.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusRunning, claimedStatus.Status)

	failedStatus, err := svc.GetExportStatus(ownerCtx, secondJob.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusFailed, failedStatus.Status)
	require.Contains(t, failedStatus.Error, "artifact artifact-1 already exists")

	succeededStatus, err := svc.GetExportStatus(ownerCtx, thirdJob.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, succeededStatus.Status)
}

func TestServiceRunQueuedExportsSharesQueueAcrossFormatsWhileKeepingOwnerVisibility(t *testing.T) {
	now := time.Date(2026, 6, 13, 16, 30, 0, 0, time.UTC)
	exporter := &queuedExportRecorder{}
	idCounter := 0
	fs := afs.New()
	root := "file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "scratchpad", "${userID}"))
	afsscratchpad.Register(
		afsscratchpad.WithAFS(fs),
		afsscratchpad.WithRootURI(root),
		afsscratchpad.WithAllowedTargetSchemes("file"),
	)
	scratch := afsscratchpad.New(
		afsscratchpad.WithAFS(fs),
		afsscratchpad.WithRootURI(root),
		afsscratchpad.WithAllowedTargetSchemes("file"),
	)
	svc := New(Options{
		Exporter:     exporter,
		Store:        NewStoreAdapter(reportmemory.New()),
		Scratchpad:   scratch,
		ScratchpadFS: fs,
		Now: func() time.Time {
			return now.Add(time.Duration(idCounter) * time.Minute)
		},
		NewID: func() string {
			idCounter++
			switch idCounter {
			case 1:
				return "job-pdf"
			case 2:
				return "job-csv"
			case 3:
				return "job-xlsx"
			case 4:
				return "artifact-pdf"
			case 5:
				return "artifact-csv"
			case 6:
				return "artifact-xlsx"
			default:
				return "artifact-extra"
			}
		},
	})

	ownerOneCtx := authsvc.InjectUser(context.Background(), "owner-1")
	ownerTwoCtx := authsvc.InjectUser(context.Background(), "owner-2")

	pdfJob, err := svc.SubmitExport(ownerOneCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance-pdf",
		Format:      ExportFormatPDF,
		Scope:       ExportScopeDraft,
		ReportPrint: json.RawMessage(validRenderableTestReportPrintJSON()),
	})
	require.NoError(t, err)

	csvJob, err := svc.SubmitExport(ownerTwoCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance-csv",
		Format:      ExportFormatCSV,
		Scope:       ExportScopeDraft,
		ReportFill:  json.RawMessage(validTestReportFillJSON()),
	})
	require.NoError(t, err)

	xlsxJob, err := svc.SubmitExport(ownerOneCtx, &SubmitExportRequest{
		ArtifactRef: "report://draft/performance-xlsx",
		Format:      ExportFormatXLSX,
		Scope:       ExportScopeDraft,
		ReportFill:  json.RawMessage(validTestReportFillJSON()),
	})
	require.NoError(t, err)

	result, err := svc.RunQueuedExports(context.Background(), 3)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 3, result.ProcessedCount)
	require.Equal(t, 3, result.SucceededCount)
	require.Zero(t, result.FailedCount)
	require.Len(t, result.Jobs, 3)

	require.Equal(t, "job-pdf", result.Jobs[0].JobID)
	require.Equal(t, ExportFormatPDF, result.Jobs[0].Format)
	require.Equal(t, JobStatusSucceeded, result.Jobs[0].Status)
	require.Equal(t, "artifact-pdf", result.Jobs[0].ArtifactID)

	require.Equal(t, "job-csv", result.Jobs[1].JobID)
	require.Equal(t, ExportFormatCSV, result.Jobs[1].Format)
	require.Equal(t, JobStatusSucceeded, result.Jobs[1].Status)
	require.Equal(t, "artifact-csv", result.Jobs[1].ArtifactID)

	require.Equal(t, "job-xlsx", result.Jobs[2].JobID)
	require.Equal(t, ExportFormatXLSX, result.Jobs[2].Format)
	require.Equal(t, JobStatusSucceeded, result.Jobs[2].Status)
	require.Equal(t, "artifact-xlsx", result.Jobs[2].ArtifactID)

	require.Len(t, exporter.requests, 3)
	require.Equal(t, "job-pdf", exporter.requests[0].JobID)
	require.Equal(t, ExportFormatPDF, exporter.requests[0].Format)
	require.Equal(t, "owner-1", exporter.requests[0].OwnerID)
	require.Equal(t, "job-csv", exporter.requests[1].JobID)
	require.Equal(t, ExportFormatCSV, exporter.requests[1].Format)
	require.Equal(t, "owner-2", exporter.requests[1].OwnerID)
	require.Equal(t, "job-xlsx", exporter.requests[2].JobID)
	require.Equal(t, ExportFormatXLSX, exporter.requests[2].Format)
	require.Equal(t, "owner-1", exporter.requests[2].OwnerID)

	pdfStatus, err := svc.GetExportStatus(ownerOneCtx, pdfJob.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, pdfStatus.Status)

	csvStatus, err := svc.GetExportStatus(ownerTwoCtx, csvJob.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, csvStatus.Status)

	xlsxStatus, err := svc.GetExportStatus(ownerOneCtx, xlsxJob.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, xlsxStatus.Status)

	_, err = svc.GetExportStatus(ownerOneCtx, csvJob.JobID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = svc.GetExportStatus(ownerTwoCtx, pdfJob.JobID)
	require.ErrorIs(t, err, ErrNotFound)

	pdfArtifact, err := svc.GetArtifact(ownerOneCtx, pdfStatus.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, ExportFormatPDF, pdfArtifact.Format)
	require.Equal(t, "application/pdf", pdfArtifact.ContentType)
	require.Equal(t, "scratchpad://artifact/artifact-pdf", pdfArtifact.SourceURL)
	require.Equal(t, []byte("%pdf-job-pdf"), pdfArtifact.Data)

	csvArtifact, err := svc.GetArtifact(ownerTwoCtx, csvStatus.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, ExportFormatCSV, csvArtifact.Format)
	require.Equal(t, "text/csv", csvArtifact.ContentType)
	require.Equal(t, "scratchpad://artifact/artifact-csv", csvArtifact.SourceURL)
	require.Equal(t, []byte("%csv-job-csv"), csvArtifact.Data)

	xlsxArtifact, err := svc.GetArtifact(ownerOneCtx, xlsxStatus.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, ExportFormatXLSX, xlsxArtifact.Format)
	require.Equal(t, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", xlsxArtifact.ContentType)
	require.Equal(t, "scratchpad://artifact/artifact-xlsx", xlsxArtifact.SourceURL)
	require.Equal(t, []byte("%xlsx-job-xlsx"), xlsxArtifact.Data)

	_, err = svc.GetArtifact(ownerOneCtx, csvStatus.ArtifactID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = svc.GetArtifact(ownerTwoCtx, xlsxStatus.ArtifactID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = svc.GetArtifact(ownerTwoCtx, pdfStatus.ArtifactID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestReportSpecCompiler_CompileCanonicalSpec(t *testing.T) {
	now := time.Date(2026, 6, 13, 14, 0, 0, 0, time.UTC)
	compiler := NewReportSpecCompiler(func() time.Time { return now })
	result, err := compiler.Compile(context.Background(), &CompileRequest{
		ArtifactRef: "report://draft/performance",
		SourceKind:  SourceKindReportSpec,
		Document: json.RawMessage(`{
			"version": 1,
			"kind": "reportSpec",
			"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
			"title": "Demo Report",
			"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
			"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
			"refinements": [],
			"calculatedFields": [],
			"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
			"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
		}`),
	})
	require.NoError(t, err)
	require.Equal(t, "report://draft/performance", result.ArtifactRef)
	require.JSONEq(t, `{
			"version": 1,
			"kind": "reportSpec",
			"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
			"title": "Demo Report",
			"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
			"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
			"refinements": [],
			"calculatedFields": [],
			"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
			"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
		}`, string(result.ReportSpec))
	require.Equal(t, now, result.CompiledAt)
}

func TestReportSpecCompiler_RejectsInvalidInputs(t *testing.T) {
	compiler := NewReportSpecCompiler(nil)
	_, err := compiler.Compile(context.Background(), &CompileRequest{
		SourceKind: "reportDocument",
		Document:   json.RawMessage(`{"kind":"reportDocument"}`),
	})
	require.EqualError(t, err, `invalid reporting compile source kind "reportDocument": only reportSpec is supported`)

	_, err = compiler.Compile(context.Background(), &CompileRequest{
		SourceKind: SourceKindReportSpec,
		Document:   json.RawMessage(`{"version":1,"kind":"reportSpec"}`),
	})
	require.ErrorContains(t, err, "invalid reportSpec: missing source")

	for _, testCase := range []struct {
		name    string
		doc     string
		wantErr string
	}{
		{
			name: "null source",
			doc: `{
				"version": 1,
				"kind": "reportSpec",
				"source": null,
				"title": "Demo Report",
				"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
				"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
				"refinements": [],
				"calculatedFields": [],
				"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
				"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
			}`,
			wantErr: "invalid reportSpec: source must be an object",
		},
		{
			name: "numeric title",
			doc: `{
				"version": 1,
				"kind": "reportSpec",
				"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
				"title": 42,
				"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
				"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
				"refinements": [],
				"calculatedFields": [],
				"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
				"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
			}`,
			wantErr: "invalid reportSpec: title must be a string",
		},
		{
			name: "null title",
			doc: `{
				"version": 1,
				"kind": "reportSpec",
				"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
				"title": null,
				"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
				"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
				"refinements": [],
				"calculatedFields": [],
				"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
				"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
			}`,
			wantErr: "invalid reportSpec: title must be a string",
		},
		{
			name: "empty title",
			doc: `{
				"version": 1,
				"kind": "reportSpec",
				"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
				"title": "",
				"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
				"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
				"refinements": [],
				"calculatedFields": [],
				"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
				"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
			}`,
			wantErr: "invalid reportSpec: title must be a non-empty string",
		},
		{
			name: "null refinements",
			doc: `{
				"version": 1,
				"kind": "reportSpec",
				"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
				"title": "Demo Report",
				"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
				"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
				"refinements": null,
				"calculatedFields": [],
				"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
				"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
			}`,
			wantErr: "invalid reportSpec: refinements must be an array",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := compiler.Compile(context.Background(), &CompileRequest{
				SourceKind: SourceKindReportSpec,
				Document:   json.RawMessage(testCase.doc),
			})
			require.EqualError(t, err, testCase.wantErr)
		})
	}
}

func validTestReportSpecJSON() string {
	return `{
		"version": 1,
		"kind": "reportSpec",
		"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
		"title": "Demo Report",
		"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
		"layoutIntent": {"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},
		"refinements": [],
		"calculatedFields": [],
		"datasets": [{"id":"primary","dataSourceRef":"demo","request":{}}],
		"blocks": [{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]
	}`
}

func validTestReportFillJSON() string {
	return validTestReportFillJSONWithSpecVersion(1)
}

func validRenderableTestReportFillJSON() string {
	return `{
		"version": 1,
		"kind": "reportFill",
		"specVersion": 1,
		"specHash": "spec-1",
		"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
		"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
		"refinements": [],
		"calculatedFields": [],
		"datasets": [{
			"id": "primary",
			"dataSourceRef": "demo",
			"request": {"limit": 25, "offset": 0},
			"provenance": {"requestHash":"fnv1a:9702fdec","rowCount":2,"truncated":false,"hasMore":false,"diagnostics":[]},
			"rows": [{"channel":"Display","spend":42.5},{"channel":"CTV","spend":30}]
		}],
		"blocks": [{
			"id":"primaryTable",
			"kind":"tableBlock",
			"datasetRef":"primary",
			"columns":[
				{"key":"channel","label":"Channel"},
				{"key":"spend","label":"Spend","format":"currency"}
			],
			"content":{
				"columns":[
					{"key":"channel","label":"Channel"},
					{"key":"spend","label":"Spend","format":"currency"}
				],
				"rowCount":2,
				"resolvedRows":[
					{"rowIndex":0,"cells":[
						{"key":"channel","sourceKey":"channel","displayKey":"channel","value":"Display","displayValue":"Display","visualState":null},
						{"key":"spend","sourceKey":"spend","displayKey":"spend","value":42.5,"displayValue":"$42.50","visualState":null}
					]},
					{"rowIndex":1,"cells":[
						{"key":"channel","sourceKey":"channel","displayKey":"channel","value":"CTV","displayValue":"CTV","visualState":null},
						{"key":"spend","sourceKey":"spend","displayKey":"spend","value":30,"displayValue":"$30.00","visualState":null}
					]}
				]
			}
		}],
		"diagnostics": []
	}`
}

func validTestReportFillJSONWithSpecVersion(specVersion int) string {
	return `{
		"version": 1,
		"kind": "reportFill",
		"specVersion": ` + jsonNumber(specVersion) + `,
		"specHash": "spec-1",
		"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
		"parameters": {"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},
		"refinements": [],
		"calculatedFields": [],
		"datasets": [{
			"id": "primary",
			"dataSourceRef": "demo",
			"request": {"limit": 25, "offset": 0},
			"provenance": {"requestHash":"fnv1a:9702fdec","rowCount":1,"truncated":false,"hasMore":false,"diagnostics":[]},
			"rows": [{"channel":"Display"}]
		}],
		"blocks": [{"id":"primaryTable","kind":"tableBlock"}],
		"diagnostics": []
	}`
}

func validTestReportPrintJSON() string {
	return `{
		"version": 1,
		"kind": "reportPrint",
		"specVersion": 1,
		"specHash": "spec-1",
		"fillVersion": 1,
		"fillHash": "fill-1",
		"source": {"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},
		"title": "Demo Report",
		"pageGeometry": {
			"width": 612,
			"height": 792,
			"marginTop": 48,
			"marginRight": 48,
			"marginBottom": 48,
			"marginLeft": 48,
			"headerHeight": 24,
			"footerHeight": 24
		},
		"pages": [{
			"number": 1,
			"elements": [{
				"id": "body-1",
				"kind": "text",
				"text": "Demo body",
				"box": {"x": 48, "y": 96, "width": 200, "height": 18}
			}],
			"headerElements": [],
			"footerElements": []
		}],
		"bookmarks": [{"id":"section-1","title":"Section 1","pageNumber":1}],
		"diagnostics": []
	}`
}

func validRenderableTestReportPrintJSON() string {
	return validTestReportPrintJSON()
}

func validTestReportExportRequestEnvelope() *ReportExportRequest {
	return &ReportExportRequest{
		Version: 1,
		Kind:    "reportExportRequest",
		Target: ReportExportTarget{
			Format: ExportFormatPDF,
		},
		Source: ReportExportSource{
			From:             "savedPayload",
			ArtifactKind:     "reportBuilder.savedReportPayload",
			ArtifactRef:      "reportBuilder.savedReportPayload://rbreport_forecasting_q3",
			Title:            "Forecasting Q3",
			ReportID:         "forecastingQ3",
			PayloadID:        "rbreport_forecasting_q3",
			SourceArtifactID: "forecasting_q3",
			DocumentVersion:  4,
		},
		ReportSpec:  json.RawMessage(validTestReportSpecJSON()),
		ReportFill:  json.RawMessage(validTestReportFillJSON()),
		ReportPrint: json.RawMessage(validTestReportPrintJSON()),
	}
}

func jsonNumber(value int) string {
	return strconv.Itoa(value)
}
