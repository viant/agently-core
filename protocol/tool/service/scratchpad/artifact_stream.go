package scratchpad

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	afsscratchpad "github.com/viant/afs/scratchpad"
	afsurl "github.com/viant/afs/url"
	authctx "github.com/viant/agently-core/internal/auth"
)

type artifactStreamReader struct {
	ctx    context.Context
	reader io.Reader
	hash   hash.Hash
	size   int64
	eof    bool
	err    error
}

func (r *artifactStreamReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		r.err = err
		return 0, err
	}
	n, err := r.reader.Read(data)
	if n > 0 {
		_, _ = r.hash.Write(data[:n])
		r.size += int64(n)
	}
	if err == io.EOF {
		r.eof = true
	} else if err != nil {
		r.err = err
	}
	return n, err
}

// PublishArtifactStream streams to the configured current-user AFS root. The
// fresh UUID object is private staging until its manifest is published last;
// no provider-specific rename or fixed local destination is required.
func (s *Service) PublishArtifactStream(ctx context.Context, name, mimeType, sourceURI string, body io.Reader) (*ArtifactDescriptor, error) {
	failure := func() (*ArtifactDescriptor, error) { return nil, fmt.Errorf("artifact stream publication failed") }
	if strings.TrimSpace(authctx.EffectiveUserID(ctx)) == "" || body == nil || len(name) > 1024 || len(mimeType) > 256 || len(sourceURI) > 4096 {
		return failure()
	}
	root, _, err := s.resolveRootURI(ctx)
	if err != nil {
		return failure()
	}
	id := uuid.NewString()
	target := afsurl.Join(root, "artifacts", id+".bin")
	manifestURL := afsscratchpad.NoteURL(root, afsscratchpad.ArtifactKey(id))
	exists, err := s.fs.Exists(ctx, target)
	if err != nil || exists {
		return failure()
	}
	exists, err = s.fs.Exists(ctx, manifestURL)
	if err != nil || exists {
		return failure()
	}
	if err = s.fs.Create(ctx, afsurl.Join(root, "artifacts"), 0700, true); err != nil {
		return failure()
	}
	published := false
	defer func() {
		if !published {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_ = s.fs.Delete(cleanup, target)
			_ = s.fs.Delete(cleanup, manifestURL)
		}
	}()
	stream := &artifactStreamReader{ctx: ctx, reader: body, hash: sha256.New()}
	if err = s.fs.Upload(ctx, target, 0600, stream); err != nil || stream.err != nil || ctx.Err() != nil {
		return failure()
	}
	if !stream.eof {
		var probe [1]byte
		n, readErr := stream.Read(probe[:])
		if n != 0 || readErr != io.EOF {
			return failure()
		}
	}
	if stream.size == 0 {
		return failure()
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	digest := hex.EncodeToString(stream.hash.Sum(nil))
	manifest := artifactManifest{Artifact: afsscratchpad.Artifact{Kind: "artifact", ArtifactID: id, Name: name, ContentType: mimeType, SourceURL: target}, SizeBytes: stream.size, SHA256: digest, SourceURI: sourceURI}
	encoded, _ := json.Marshal(manifest)
	if _, err = s.client(ctx).Memorize(ctx, &afsscratchpad.MemorizeInput{Key: afsscratchpad.ArtifactKey(id), Description: "Artifact " + name, Body: string(encoded)}); err != nil {
		return failure()
	}
	published = true
	return &ArtifactDescriptor{URI: ArtifactURI(id), ID: id, Name: name, MimeType: mimeType, SizeBytes: stream.size, SHA256: digest, SourceURI: sourceURI}, nil
}

type verifiedArtifactStream struct {
	*os.File
	temporary string
}

func (r *verifiedArtifactStream) Close() error {
	err := r.File.Close()
	_ = os.Remove(r.temporary)
	return err
}

// OpenVerifiedArtifactStream verifies the configured backing object into a
// private temporary spool before releasing bytes. It has no input-upload size
// cap and supports seeking/range downloads without a decoded in-memory copy.
func (s *Service) OpenVerifiedArtifactStream(ctx context.Context, uri string) (*ArtifactDescriptor, io.ReadSeekCloser, error) {
	failure := func() (*ArtifactDescriptor, io.ReadSeekCloser, error) {
		return nil, nil, fmt.Errorf("artifact stream unavailable")
	}
	if strings.TrimSpace(authctx.EffectiveUserID(ctx)) == "" {
		return failure()
	}
	descriptor, err := s.DescribeArtifact(ctx, uri)
	if err != nil || descriptor.SizeBytes <= 0 || len(descriptor.SHA256) != 64 {
		return failure()
	}
	root, _, err := s.resolveRootURI(ctx)
	if err != nil {
		return failure()
	}
	note, err := s.client(ctx).Fetch(ctx, afsscratchpad.ArtifactKey(descriptor.ID))
	if err != nil {
		return failure()
	}
	var manifest artifactManifest
	expected := afsurl.Join(root, "artifacts", descriptor.ID+".bin")
	if json.Unmarshal([]byte(note.Body), &manifest) != nil || manifest.SourceURL != expected {
		return failure()
	}
	reader, err := s.fs.OpenURL(ctx, expected)
	if err != nil {
		return failure()
	}
	defer reader.Close()
	spool, err := os.CreateTemp("", "agently-verified-artifact-*")
	if err != nil {
		return failure()
	}
	success := false
	defer func() {
		if !success {
			_ = spool.Close()
			_ = os.Remove(spool.Name())
		}
	}()
	if descriptor.SizeBytes == int64(^uint64(0)>>1) {
		return failure()
	}
	stream := &artifactStreamReader{ctx: ctx, reader: io.LimitReader(reader, descriptor.SizeBytes+1), hash: sha256.New()}
	_, err = io.Copy(spool, stream)
	if err != nil || ctx.Err() != nil || stream.size != descriptor.SizeBytes || hex.EncodeToString(stream.hash.Sum(nil)) != descriptor.SHA256 {
		return failure()
	}
	current, err := s.DescribeArtifact(ctx, uri)
	if err != nil || *current != *descriptor {
		return failure()
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return failure()
	}
	success = true
	return descriptor, &verifiedArtifactStream{File: spool, temporary: spool.Name()}, nil
}
