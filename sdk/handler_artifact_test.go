package sdk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	scratchpad "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	"github.com/xuri/excelize/v2"
)

func TestArtifactDownloadRequiresOwnerAndChecksIntegrity(t *testing.T) {
	root := t.TempDir()
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.Join(root, "${userID}"))
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	id := uuid.NewString()
	data := []byte{'P', 'K', 0, 255, 128, 34, '\n'}
	descriptor, err := scratchpad.New().PublishArtifact(owner, id, "workbook.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "", bytes.NewReader(data))
	require.NoError(t, err)
	handler := NewHandler(nil)
	download := func(ctx context.Context, id string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/v1/artifacts/"+id, nil).WithContext(ctx)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	response := download(owner, id)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, data, response.Body.Bytes())
	require.Equal(t, descriptor.MimeType, response.Header().Get("Content-Type"))
	require.Equal(t, "attachment; filename=workbook.xlsx", response.Header().Get("Content-Disposition"))
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, http.StatusUnauthorized, download(context.Background(), id).Code)
	foreign := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "foreign"})
	require.Equal(t, http.StatusNotFound, download(foreign, id).Code)
	require.Equal(t, http.StatusNotFound, download(owner, "invalid").Code)
	require.NoError(t, os.WriteFile(filepath.Join(root, "owner", "artifacts", id+".bin"), []byte("corrupted workbook"), 0600))
	response = download(owner, id)
	require.Equal(t, http.StatusNotFound, response.Code)
	require.Equal(t, "artifact unavailable\n", response.Body.String())
	require.Empty(t, response.Header().Get("Content-Disposition"))
}

type zeroDownloadReader struct{}

func (zeroDownloadReader) Read(data []byte) (int, error) { clear(data); return len(data), nil }

func TestArtifactDownloadStreamsBeyondInputLimit(t *testing.T) {
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.Join(t.TempDir(), "${userID}"))
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "large-owner"})
	const size = int64(65 << 20)
	descriptor, err := scratchpad.New().PublishArtifactStream(owner, "large.bin", "application/octet-stream", "", io.LimitReader(zeroDownloadReader{}, size))
	require.NoError(t, err)
	handler := NewHandler(nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r.WithContext(owner)) }))
	defer server.Close()
	response, err := http.Get(server.URL + "/v1/artifacts/" + descriptor.ID)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	digest := sha256.New()
	count, err := io.Copy(digest, response.Body)
	require.NoError(t, err)
	require.EqualValues(t, size, count)
	require.Equal(t, descriptor.SHA256, hex.EncodeToString(digest.Sum(nil)))
}

func TestArtifactWorkbookDownloadSurvivesNewHandlerWithAllSheets(t *testing.T) {
	if id := os.Getenv("CORE_ARTIFACT_TEST_RESTART_ID"); id != "" {
		owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "published-owner"})
		response := httptest.NewRecorder()
		NewHandler(nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/artifacts/"+id, nil).WithContext(owner))
		require.Equal(t, http.StatusOK, response.Code)
		digest := sha256.Sum256(response.Body.Bytes())
		require.Equal(t, os.Getenv("CORE_ARTIFACT_TEST_RESTART_SHA256"), hex.EncodeToString(digest[:]))
		read, err := excelize.OpenReader(bytes.NewReader(response.Body.Bytes()))
		require.NoError(t, err)
		require.Equal(t, []string{"Campaigns", "Sites"}, read.GetSheetList())
		require.NoError(t, read.Close())
		return
	}
	root := t.TempDir()
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.Join(root, "${userID}"))
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "published-owner"})
	workbook := excelize.NewFile()
	defer workbook.Close()
	require.NoError(t, workbook.SetSheetName("Sheet1", "Campaigns"))
	_, err := workbook.NewSheet("Sites")
	require.NoError(t, err)
	require.NoError(t, workbook.SetCellValue("Campaigns", "A1", "CampaignId"))
	require.NoError(t, workbook.SetCellValue("Campaigns", "A2", 470750))
	require.NoError(t, workbook.SetCellValue("Sites", "A1", "Site"))
	require.NoError(t, workbook.SetCellValue("Sites", "A2", "owned.example"))
	require.NoError(t, workbook.SetCellFormula("Sites", "B2", "1+2"))
	buffer, err := workbook.WriteToBuffer()
	require.NoError(t, err)
	data := append([]byte(nil), buffer.Bytes()...)
	descriptor, err := scratchpad.New().PublishArtifact(owner, "", "spreadsheet-editor.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "", bytes.NewReader(data))
	require.NoError(t, err)
	// Each new HTTP handler and scratchpad service reads the published files;
	// no originating RPC session or in-memory capture is required for download.
	for i := 0; i < 2; i++ {
		handler := NewHandler(nil)
		request := httptest.NewRequest(http.MethodGet, "/v1/artifacts/"+descriptor.ID, nil).WithContext(owner)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
		require.Equal(t, data, response.Body.Bytes())
		read, err := excelize.OpenReader(bytes.NewReader(response.Body.Bytes()))
		require.NoError(t, err)
		require.Equal(t, []string{"Campaigns", "Sites"}, read.GetSheetList())
		campaign, err := read.GetCellValue("Campaigns", "A2")
		require.NoError(t, err)
		require.Equal(t, "470750", campaign)
		site, err := read.GetCellValue("Sites", "A2")
		require.NoError(t, err)
		require.Equal(t, "owned.example", site)
		formula, err := read.GetCellFormula("Sites", "B2")
		require.NoError(t, err)
		require.Equal(t, "1+2", formula)
		require.NoError(t, read.Close())
	}
	digest := sha256.Sum256(data)
	child := exec.Command(os.Args[0], "-test.run=^TestArtifactWorkbookDownloadSurvivesNewHandlerWithAllSheets$", "-test.v")
	child.Env = append(os.Environ(), "CORE_ARTIFACT_TEST_RESTART_ID="+descriptor.ID, "CORE_ARTIFACT_TEST_RESTART_SHA256="+hex.EncodeToString(digest[:]))
	output, err := child.CombinedOutput()
	require.NoError(t, err, "%s", output)
}
