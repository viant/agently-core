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

func TestReadArtifactPayloadRequiresCurrentOwnerAndImmutableIntegrity(t *testing.T) {
	svc := New(WithRootURI("file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}"))))
	ctx := userCtx("alice")
	id := "2f1c171e-0fe9-4aa7-8f17-5eb826f72042"
	d, err := svc.PublishArtifact(ctx, id, "sites.csv", "text/csv", "", strings.NewReader("site\nowned.example\n"))
	require.NoError(t, err)
	data, err := svc.ReadArtifactPayload(ctx, d.URI)
	require.NoError(t, err)
	require.Equal(t, "site\nowned.example\n", string(data))
	_, err = svc.ReadArtifactPayload(userCtx("bob"), d.URI)
	require.Error(t, err)
	_, err = svc.ReadArtifactPayload(context.Background(), d.URI)
	require.ErrorContains(t, err, "identity")
	_, err = svc.ReadArtifactPayload(ctx, ArtifactURI("missing"))
	require.Error(t, err)
	root, _, err := svc.resolveRootURI(ctx)
	require.NoError(t, err)
	note, err := svc.client(ctx).Fetch(ctx, afsscratchpad.ArtifactKey(id))
	require.NoError(t, err)
	var manifest artifactManifest
	require.NoError(t, json.Unmarshal([]byte(note.Body), &manifest))
	// Tamper only this new test-owned file, never a runtime/business resource.
	require.NoError(t, svc.fs.Upload(ctx, manifest.SourceURL, 0600, strings.NewReader("site\nforeign.example\n")))
	_, err = svc.ReadArtifactPayload(ctx, d.URI)
	require.ErrorContains(t, err, "integrity")
	_ = root
}

func TestArtifactPayloadRejectsExpiryMissingIntegrityAndOversizeBeforeRead(t *testing.T) {
	svc := New(WithRootURI("file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}"))))
	ctx := userCtx("owned-user")
	descriptor, err := svc.PublishArtifact(ctx, "owned-limits", "limits.bin", "application/octet-stream", "", strings.NewReader("owned"))
	require.NoError(t, err)
	note, err := svc.client(ctx).Fetch(ctx, afsscratchpad.ArtifactKey(descriptor.ID))
	require.NoError(t, err)
	var original artifactManifest
	require.NoError(t, json.Unmarshal([]byte(note.Body), &original))
	for _, test := range []struct {
		name     string
		change   func(*artifactManifest)
		expected string
	}{
		{"expired", func(m *artifactManifest) { m.ExpiresAt = "2000-01-01T00:00:00Z" }, "expired"},
		{"missing digest", func(m *artifactManifest) { m.SHA256 = "" }, "integrity metadata"},
		{"oversize", func(m *artifactManifest) { m.SizeBytes = MaxArtifactBytes + 1 }, "integrity metadata"},
		{"empty", func(m *artifactManifest) { m.SizeBytes = 0 }, "integrity metadata"},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := original
			test.change(&manifest)
			raw, _ := json.Marshal(manifest)
			_, err := svc.client(ctx).Memorize(ctx, &afsscratchpad.MemorizeInput{Key: afsscratchpad.ArtifactKey(descriptor.ID), Description: "owned artifact limits", Body: string(raw)})
			require.NoError(t, err)
			_, err = svc.ReadArtifactPayload(ctx, descriptor.URI)
			require.ErrorContains(t, err, test.expected)
		})
	}
}

func TestArtifactPayloadRejectsForeignBackingEvenWithMatchingDigest(t *testing.T) {
	svc := New(WithRootURI("file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}"))))
	alice, bob := userCtx("alice"), userCtx("bob")
	a, err := svc.PublishArtifact(alice, "owned-id", "owned.bin", "application/octet-stream", "", strings.NewReader("same owned fixture bytes"))
	require.NoError(t, err)
	_, err = svc.PublishArtifact(bob, "owned-id", "owned.bin", "application/octet-stream", "", strings.NewReader("same owned fixture bytes"))
	require.NoError(t, err)
	foreignNote, err := svc.client(bob).Fetch(bob, afsscratchpad.ArtifactKey(a.ID))
	require.NoError(t, err)
	_, err = svc.client(alice).Memorize(alice, &afsscratchpad.MemorizeInput{Key: afsscratchpad.ArtifactKey(a.ID), Description: "owned tamper fixture", Body: foreignNote.Body})
	require.NoError(t, err)
	_, err = svc.ReadArtifactPayload(alice, a.URI)
	require.ErrorContains(t, err, "backing owner mismatch")
}
