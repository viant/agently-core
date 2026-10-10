package scratchpad

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type zeroArtifactReader struct{}

func (zeroArtifactReader) Read(data []byte) (int, error) { clear(data); return len(data), nil }

func TestArtifactStreamBeyondInputLimitVerifiesWithoutWholeBuffer(t *testing.T) {
	service := New(WithRootURI("file://" + filepath.Join(t.TempDir(), "${userID}")))
	ctx := userCtx("stream-owner")
	const size = int64(65 << 20)
	digest := sha256.New()
	_, err := io.Copy(digest, io.LimitReader(zeroArtifactReader{}, size))
	require.NoError(t, err)
	descriptor, err := service.PublishArtifactStream(ctx, "large.bin", "application/octet-stream", "", io.LimitReader(zeroArtifactReader{}, size))
	require.NoError(t, err)
	require.EqualValues(t, size, descriptor.SizeBytes)
	require.Equal(t, hex.EncodeToString(digest.Sum(nil)), descriptor.SHA256)
	_, err = service.ReadArtifactPayload(ctx, descriptor.URI)
	require.Error(t, err, "input/macro size contract remains independent")
	_, stream, err := service.OpenVerifiedArtifactStream(ctx, descriptor.URI)
	require.NoError(t, err)
	private := stream.(*verifiedArtifactStream)
	info, err := os.Stat(private.temporary)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	readHash := sha256.New()
	count, err := io.Copy(readHash, stream)
	require.NoError(t, err)
	require.EqualValues(t, size, count)
	require.Equal(t, digest.Sum(nil), readHash.Sum(nil))
	require.NoError(t, stream.Close())
	_, err = os.Stat(private.temporary)
	require.True(t, os.IsNotExist(err))
}

func TestArtifactStreamUsesConfiguredNonFileDestination(t *testing.T) {
	service := New(WithRootURI("mem://localhost/stream-output/" + uuid.NewString() + "/${userID}"))
	ctx := userCtx("stream-owner")
	descriptor, err := service.PublishArtifactStream(ctx, "owned.bin", "application/octet-stream", "", strings.NewReader("owned streamed bytes"))
	require.NoError(t, err)
	_, stream, err := service.OpenVerifiedArtifactStream(ctx, descriptor.URI)
	require.NoError(t, err)
	defer stream.Close()
	data, err := io.ReadAll(stream)
	require.NoError(t, err)
	require.Equal(t, "owned streamed bytes", string(data))
}

type failedArtifactReader struct {
	cancel context.CancelFunc
	used   bool
}

func (r *failedArtifactReader) Read(data []byte) (int, error) {
	if !r.used {
		r.used = true
		if r.cancel != nil {
			r.cancel()
		}
		return copy(data, "partial"), nil
	}
	return 0, errors.New("private payload reader failed")
}

func TestArtifactStreamCleansOnlyNewObjectsOnFailureAndCancellation(t *testing.T) {
	for _, cancelRead := range []bool{false, true} {
		root := t.TempDir()
		service := New(WithRootURI("file://" + filepath.Join(root, "${userID}")))
		owner := userCtx("owner")
		existing, err := service.PublishArtifactStream(owner, "existing.bin", "application/octet-stream", "", bytes.NewBufferString("preexisting immutable bytes"))
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(owner)
		reader := &failedArtifactReader{}
		if cancelRead {
			reader.cancel = cancel
		}
		defer cancel()
		_, err = service.PublishArtifactStream(ctx, "failed.bin", "application/octet-stream", "", reader)
		require.EqualError(t, err, "artifact stream publication failed")
		entries, err := os.ReadDir(filepath.Join(root, "owner", "artifacts"))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		_, stream, err := service.OpenVerifiedArtifactStream(owner, existing.URI)
		require.NoError(t, err)
		require.NoError(t, stream.Close())
		artifacts, _, err := service.ListArtifacts(owner, "", 100)
		require.NoError(t, err)
		require.Len(t, artifacts, 1)
	}
}
