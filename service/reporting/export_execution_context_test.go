package reporting

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
	"github.com/viant/forge/backend/types"
	"github.com/viant/scy"
	"github.com/viant/scy/auth/jwt/signer"
)

type verifiedQueueExporter struct {
	t        *testing.T
	owner    *types.LocalStateOwner
	deadline time.Time
	calls    int
}

func (e *verifiedQueueExporter) Export(ctx context.Context, _ *RenderRequest) (*RenderResult, error) {
	e.calls++
	owner, err := authsvc.LocalStateOwner(ctx)
	require.NoError(e.t, err)
	require.Equal(e.t, e.owner, owner, "local admission/session key must survive queue dispatch")
	require.Empty(e.t, iauth.CanonicalUserID(ctx), "missing canonical identity must not be guessed")
	deadline, ok := ctx.Deadline()
	require.True(e.t, ok)
	require.Equal(e.t, e.deadline, deadline)
	return &RenderResult{ContentType: "application/pdf", Data: []byte("%PDF synthetic")}, nil
}

func verifiedQueueCookie(t *testing.T) (*authsvc.Runtime, *http.Cookie) {
	t.Helper()
	root := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	private := filepath.Join(root, "private.pem")
	public := filepath.Join(root, "public.pem")
	require.NoError(t, os.WriteFile(private, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600))
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(public, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0600))
	config, err := json.Marshal(map[string]any{"auth": map[string]any{"enabled": true, "cookieName": "test_session", "jwt": map[string]any{"enabled": true, "rsa": []string{public}}}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), config, 0600))
	lifetime, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	runtime, err := authsvc.NewRuntime(lifetime, root, nil)
	require.NoError(t, err)
	s := signer.New(&signer.Config{RSA: scy.NewResource("", private, "")})
	require.NoError(t, s.Init(context.Background()))
	jwt, err := s.Create(time.Hour, map[string]any{"sub": "owner", "email": "owner@example.test"})
	require.NoError(t, err)
	body, err := json.Marshal(map[string]string{"idToken": jwt})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "http://example.test/v1/api/auth/session", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	authsvc.WithAuthExtensions(http.NotFoundHandler(), runtime).ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Len(t, recorder.Result().Cookies(), 1)
	return runtime, recorder.Result().Cookies()[0]
}

func TestExportWorkerRetainsVerifiedCookieIdentityAndOriginalPinDeadline(t *testing.T) {
	exporter := &verifiedQueueExporter{t: t}
	service, store, _, pin := newPinnedExportService(t, exporter)
	runtime, cookie := verifiedQueueCookie(t)
	var job *ExportJob
	requestCtx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "http://example.test/v1/export", nil).WithContext(requestCtx)
	request.Header.Set("Origin", "http://example.test")
	request.AddCookie(cookie)
	handler := authsvc.WithAuthProtection(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var err error
		exporter.owner, err = authsvc.LocalStateOwner(request.Context())
		require.NoError(t, err)
		require.NotNil(t, exporter.owner)
		require.Empty(t, iauth.CanonicalUserID(request.Context()))
		job, err = service.SubmitExport(request.Context(), &SubmitExportRequest{Format: ExportFormatPDF, execution: trustedExecution(pin)})
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}), runtime)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	cancel()
	exporter.deadline = pin.ValidUntil
	result, err := NewWorker(service, WorkerOptions{BatchLimit: 2}).RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.SucceededCount)
	require.Equal(t, 1, exporter.calls)
	completed, err := store.GetJob(context.Background(), job.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, completed.Status)
	require.Equal(t, pin.ValidUntil, completed.ResourcePin.ValidUntil)
	require.Empty(t, service.exportContexts.entries)
}

func TestExportWorkerFinalizesUnrestorablePinAndContinuesQueue(t *testing.T) {
	exporter := &pinAwareExporter{}
	service, store, _, pin := newPinnedExportService(t, exporter)
	bad := submitPinnedExport(t, service, pin)
	bad.AuthContextRef = "actor=owner;provider=fixture-oauth"
	require.NoError(t, store.UpdateJob(context.Background(), bad))
	good := submitPinnedExport(t, service, pin)
	result, err := NewWorker(service, WorkerOptions{BatchLimit: 2}).RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, result.ProcessedCount)
	require.Equal(t, 1, result.FailedCount)
	require.Equal(t, 1, result.SucceededCount)
	failed, err := store.GetJob(context.Background(), bad.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusFailed, failed.Status)
	require.Contains(t, failed.Error, identity.ErrResourceDenied.Error())
	finished, err := store.GetJob(context.Background(), good.JobID)
	require.NoError(t, err)
	require.Equal(t, JobStatusSucceeded, finished.Status)
}
