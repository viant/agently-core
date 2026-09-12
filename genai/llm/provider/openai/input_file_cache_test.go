package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	openaiapi "github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/viant/afs/storage"
	authctx "github.com/viant/agently-core/internal/auth"
)

type countedAssetManager struct {
	fakeOpenAIAssetManager
	count int
	user  string
}

func (m *countedAssetManager) Upload(ctx context.Context, u string, mode os.FileMode, r io.Reader, opts ...storage.Option) error {
	m.count++
	m.user = authctx.EffectiveUserID(ctx)
	return m.fakeOpenAIAssetManager.Upload(ctx, u, mode, r, opts...)
}
func TestNativeUploadContextAndHandleReuse(t *testing.T) {
	manager := &countedAssetManager{fakeOpenAIAssetManager: fakeOpenAIAssetManager{fileID: "file-1"}}
	c := &Client{APIKey: "test", storageMgrAPIKey: "test", storageMgr: manager}
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	encoded := base64.StdEncoding.EncodeToString([]byte("%PDF-data"))
	for i := 0; i < 2; i++ {
		id, err := c.uploadInputFileAndGetID(alice, encoded, "a.pdf", "application/pdf", "agent", 60, openaiapi.FilePurposeUserData)
		require.NoError(t, err)
		require.Equal(t, "file-1", id)
	}
	require.Equal(t, 1, manager.count)
	require.Equal(t, "alice", manager.user)
	_, err := c.uploadInputFileAndGetID(bob, encoded, "a.pdf", "application/pdf", "agent", 60, openaiapi.FilePurposeUserData)
	require.NoError(t, err)
	require.Equal(t, 2, manager.count)
	for key, handle := range c.inputFiles {
		handle.expires = time.Now().Add(-time.Second)
		c.inputFiles[key] = handle
	}
	_, err = c.uploadInputFileAndGetID(alice, encoded, "a.pdf", "application/pdf", "agent", 60, openaiapi.FilePurposeUserData)
	require.NoError(t, err)
	require.Equal(t, 3, manager.count)
	canceled, cancel := context.WithCancel(alice)
	cancel()
	_, err = c.uploadInputFileAndGetID(canceled, encoded, "a.pdf", "application/pdf", "agent", 60, openaiapi.FilePurposeUserData)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 3, manager.count)
	require.True(t, bytes.Equal(manager.uploadedBody, []byte("%PDF-data")))
}

func TestNativeUploadUsesConfiguredProviderEndpoint(t *testing.T) {
	filename := ""
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		require.Equal(t, "/v1/files", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			posts++
			require.NoError(t, r.ParseMultipartForm(1<<20))
			defer r.MultipartForm.RemoveAll()
			file, header, err := r.FormFile("file")
			require.NoError(t, err)
			defer file.Close()
			filename = header.Filename
			json.NewEncoder(w).Encode(map[string]interface{}{"id": "file-native", "filename": filename, "bytes": 9, "created_at": 1, "purpose": "user_data"})
			return
		}
		require.Equal(t, http.MethodGet, r.Method)
		json.NewEncoder(w).Encode(map[string]interface{}{"object": "list", "data": []map[string]interface{}{{"id": "file-native", "filename": filename, "bytes": 9, "created_at": 1, "purpose": "user_data"}}})
	}))
	defer server.Close()
	client := NewClient("test-key", "test-model", WithBaseURL(server.URL+"/v1"))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	id, err := client.uploadInputFileAndGetID(ctx, base64.StdEncoding.EncodeToString([]byte("%PDF-data")), "a.pdf", "application/pdf", "agent", 60, openaiapi.FilePurposeUserData)
	require.NoError(t, err)
	require.Equal(t, "file-native", id)
	require.Equal(t, 1, posts)
}
