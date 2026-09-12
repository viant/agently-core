package scratchpad

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	afsscratchpad "github.com/viant/afs/scratchpad"
	authctx "github.com/viant/agently-core/internal/auth"
)

func TestArtifactURIValidation(t *testing.T) {
	for _, uri := range []string{"scratchpad://artifact/a123", "scratchpad://artifact/a-b_c.1"} {
		_, err := ArtifactID(uri)
		require.NoError(t, err, uri)
	}
	for _, uri := range []string{"file:///a", "scratchpad://note/a", "scratchpad://artifact/", "scratchpad://artifact/..", "scratchpad://artifact/a/b", "scratchpad://artifact/a%2fb", "scratchpad://artifact/a%5cb", "scratchpad://artifact/a?x=1", "scratchpad://artifact/a#x", "scratchpad://user@artifact/a"} {
		_, err := ArtifactID(uri)
		require.Error(t, err, uri)
	}
}
func TestArtifactPublicationAndUserIsolation(t *testing.T) {
	svc := New(WithRootURI("file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}"))))
	ctx := userCtx("alice")
	d, err := svc.PublishArtifact(ctx, "test", "hello.txt", "text/plain", "", strings.NewReader("hello"))
	require.NoError(t, err)
	require.Equal(t, "scratchpad://artifact/test", d.URI)
	require.EqualValues(t, 5, d.SizeBytes)
	require.Len(t, d.SHA256, 64)
	_, r, err := svc.OpenArtifact(ctx, d.URI)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	r.Close()
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))
	_, _, err = svc.OpenArtifact(userCtx("bob"), d.URI)
	require.Error(t, err)
	_, _, err = svc.OpenArtifact(context.Background(), d.URI)
	require.Error(t, err)
	same, err := svc.PublishArtifact(ctx, "test", "hello.txt", "text/plain", "", strings.NewReader("hello"))
	require.NoError(t, err)
	require.Equal(t, d, same)
	_, err = svc.PublishArtifact(ctx, "test", "hello.txt", "text/plain", "", strings.NewReader("changed"))
	require.ErrorContains(t, err, "different content")
	// A fresh service opens the persisted manifest and content after restart.
	fresh := New(WithRootURI(svc.rootTemplate))
	_, r, err = fresh.OpenArtifact(ctx, d.URI)
	require.NoError(t, err)
	r.Close()
	for _, method := range []string{"memorize", "append", "fetch"} {
		exec, err := svc.Method(method)
		require.NoError(t, err)
		switch method {
		case "memorize":
			err = exec(ctx, &MemorizeInput{Key: "artifact/test", Description: "bad", Body: "bad"}, &MemorizeOutput{})
		case "append":
			err = exec(ctx, &AppendInput{Key: "artifact/test", Body: "bad"}, &AppendOutput{})
		case "fetch":
			err = exec(ctx, &FetchInput{Key: "artifact/test"}, &FetchOutput{})
		}
		require.Error(t, err, method)
	}
	var listed ListOutput
	require.NoError(t, svc.list(ctx, &ListInput{}, &listed))
	require.Empty(t, listed.Entries)
}
func TestArtifactConcurrentPublication(t *testing.T) {
	svc := New(WithRootURI("file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}"))))
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.PublishArtifact(userCtx("alice"), "same", "a.txt", "text/plain", "", strings.NewReader("same"))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}
func TestArtifactExpiredAndInvalidManifest(t *testing.T) {
	svc := New(WithRootURI("mem://localhost/artifact-expiry/${userID}"))
	ctx := userCtx("alice")
	for _, expiry := range []string{"2000-01-01T00:00:00Z", "not-a-date"} {
		body, _ := json.Marshal(afsscratchpad.Artifact{ArtifactID: "old", SourceURL: "mem://localhost/x", ExpiresAt: expiry})
		_, err := svc.client(ctx).Memorize(ctx, &afsscratchpad.MemorizeInput{Key: "artifact/old", Description: "old", Body: string(body)})
		require.NoError(t, err)
		_, err = svc.DescribeArtifact(ctx, "scratchpad://artifact/old")
		require.ErrorContains(t, err, "expired")
	}
}
func TestArtifactAFSProviderIdentityBridge(t *testing.T) {
	t.Setenv(EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	RegisterProvider()
	t.Cleanup(func() { afsscratchpad.Register() })
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	d, err := New().PublishArtifact(ctx, "", "a.txt", "text/plain", "", bytes.NewReader([]byte("hello")))
	require.NoError(t, err)
	data, err := afs.New().DownloadWithURL(ctx, d.URI)
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))
	_, err = afs.New().DownloadWithURL(userCtx("bob"), d.URI)
	require.Error(t, err)
}
func TestArtifactCancelledAndEmptyPublication(t *testing.T) {
	svc := New()
	ctx, cancel := context.WithCancel(userCtx("alice"))
	cancel()
	_, err := svc.PublishArtifact(ctx, "", "a", "text/plain", "", strings.NewReader("hello"))
	require.Error(t, err)
	_, err = svc.PublishArtifact(userCtx("alice"), "", "a", "text/plain", "", strings.NewReader(""))
	require.Error(t, err)
	_, err = svc.PublishArtifact(userCtx("alice"), "", "a", "text/plain", "", nil)
	require.Error(t, err)
}
