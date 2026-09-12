package scratchpad

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/viant/afs"
	afsscratchpad "github.com/viant/afs/scratchpad"
	afsurl "github.com/viant/afs/url"
	authctx "github.com/viant/agently-core/internal/auth"
)

const MaxArtifactBytes int64 = 64 << 20

var publicationMu sync.Mutex

// ArtifactDescriptor is public resource metadata, never a backing storage URL.
type ArtifactDescriptor struct {
	URI       string `json:"uri"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	MimeType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256,omitempty"`
	SourceURI string `json:"sourceURI,omitempty"`
}
type artifactManifest struct {
	afsscratchpad.Artifact
	SizeBytes int64  `json:"sizeBytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	SourceURI string `json:"sourceURI,omitempty"`
}

// WithArtifactClient reuses an already configured trusted publisher (reporting).
func WithArtifactClient(client *afsscratchpad.Service, fs afs.Service) Option {
	return func(s *Service) {
		s.artifactClient = client
		if fs != nil {
			s.fs = fs
		}
	}
}

func ArtifactID(uri string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(uri))
	if err != nil || u.Scheme != "scratchpad" || u.Host != "artifact" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid scratchpad artifact URI")
	}
	id := strings.TrimPrefix(u.Path, "/")
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\x00") {
		return "", fmt.Errorf("invalid scratchpad artifact id")
	}
	return id, nil
}
func ArtifactURI(id string) string { return "scratchpad://artifact/" + url.PathEscape(id) }

// RegisterProvider initializes AFS once per runtime, never with a fixed user.
func RegisterProvider() {
	s := New()
	template := s.effectiveRootTemplate()
	base, macros := workspaceBindingsForTemplate(template)
	afsscratchpad.Register(afsscratchpad.WithRootURI(template), afsscratchpad.WithBasePath(base), afsscratchpad.WithMacros(macros), afsscratchpad.WithUserIDProvider(authctx.EffectiveUserID))
}

func (s *Service) DescribeArtifact(ctx context.Context, uri string) (*ArtifactDescriptor, error) {
	id, err := ArtifactID(uri)
	if err != nil {
		return nil, err
	}
	note, err := s.client(ctx).Fetch(ctx, afsscratchpad.ArtifactKey(id))
	if err != nil {
		return nil, fmt.Errorf("artifact unavailable")
	}
	var m artifactManifest
	if err = json.Unmarshal([]byte(note.Body), &m); err != nil || m.SourceURL == "" {
		return nil, fmt.Errorf("invalid artifact manifest")
	}
	if m.ArtifactID != "" && m.ArtifactID != id {
		return nil, fmt.Errorf("artifact identity mismatch")
	}
	if m.ExpiresAt != "" {
		expires, e := time.Parse(time.RFC3339, m.ExpiresAt)
		if e != nil || !expires.After(s.now()) {
			return nil, fmt.Errorf("artifact expired")
		}
	}
	return &ArtifactDescriptor{URI: ArtifactURI(id), ID: id, Name: m.Name, MimeType: m.ContentType, SizeBytes: m.SizeBytes, SHA256: m.SHA256, SourceURI: m.SourceURI}, nil
}

func (s *Service) OpenArtifact(ctx context.Context, uri string) (*ArtifactDescriptor, io.ReadCloser, error) {
	d, err := s.DescribeArtifact(ctx, uri)
	if err != nil {
		return nil, nil, err
	}
	_, r, err := s.client(ctx).OpenArtifact(ctx, d.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("artifact unavailable")
	}
	return d, r, nil
}

// PublishArtifact writes immutable bytes before exposing the private manifest.
// A repeated id is accepted only for identical content. IDs should be unique
// across producers; the lock coordinates publication in this process.
func (s *Service) PublishArtifact(ctx context.Context, id, name, mime, sourceURI string, body io.Reader) (*ArtifactDescriptor, error) {
	if _, _, err := s.resolveRootURI(ctx); err != nil {
		return nil, err
	}
	if len(name) > 1024 || len(mime) > 256 || len(sourceURI) > 4096 || len(id) > 256 {
		return nil, fmt.Errorf("artifact metadata limit exceeded")
	}
	if body == nil {
		return nil, fmt.Errorf("artifact body is required")
	}
	data, err := io.ReadAll(io.LimitReader(body, MaxArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxArtifactBytes {
		return nil, fmt.Errorf("artifact exceeds %d bytes", MaxArtifactBytes)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("artifact body is empty")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if id == "" {
		id = uuid.NewString()
	}
	uri := ArtifactURI(id)
	if _, err = ArtifactID(uri); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	publicationMu.Lock()
	defer publicationMu.Unlock()
	client := s.client(ctx)
	root, _, err := s.resolveRootURI(ctx)
	if err != nil {
		return nil, err
	}
	exists, err := s.fs.Exists(ctx, afsscratchpad.NoteURL(root, afsscratchpad.ArtifactKey(id)))
	if err != nil {
		return nil, fmt.Errorf("artifact lookup failed")
	}
	if exists {
		existing, e := client.Fetch(ctx, afsscratchpad.ArtifactKey(id))
		if e != nil {
			return nil, fmt.Errorf("artifact unavailable")
		}
		var m artifactManifest
		if json.Unmarshal([]byte(existing.Body), &m) == nil {
			if m.SHA256 == digest {
				return s.DescribeArtifact(ctx, uri)
			}
			// Read compatibility for artifacts published before digest metadata existed.
			if m.SHA256 == "" {
				_, r, e := client.OpenArtifact(ctx, id)
				if e == nil {
					old, e := io.ReadAll(io.LimitReader(r, MaxArtifactBytes+1))
					r.Close()
					if e == nil && bytes.Equal(old, data) {
						return s.DescribeArtifact(ctx, uri)
					}
				}
			}
		}
		return nil, fmt.Errorf("artifact already exists with different content")
	}

	if err = s.fs.Create(ctx, afsurl.Join(root, "artifacts"), 0700, true); err != nil {
		return nil, fmt.Errorf("create artifacts root failed")
	}
	target := afsurl.Join(root, "artifacts", id+".bin")
	if err = s.fs.Upload(ctx, target, 0600, bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("artifact storage failed")
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	m := artifactManifest{Artifact: afsscratchpad.Artifact{Kind: "artifact", ArtifactID: id, Name: name, ContentType: mime, SourceURL: target}, SizeBytes: int64(len(data)), SHA256: digest, SourceURI: sourceURI}
	encoded, _ := json.Marshal(m)
	if _, err = client.Memorize(ctx, &afsscratchpad.MemorizeInput{Key: afsscratchpad.ArtifactKey(id), Description: "Artifact " + name, Body: string(encoded)}); err != nil {
		_ = s.fs.Delete(ctx, target)
		return nil, fmt.Errorf("artifact publication failed")
	}
	return s.DescribeArtifact(ctx, uri)
}

// ListArtifacts returns user-owned public descriptors in stable ID order.
// cursor is the last returned id, not a filesystem path.
func (s *Service) ListArtifacts(ctx context.Context, cursor string, limit int) ([]*ArtifactDescriptor, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	listed, err := s.client(ctx).List(ctx)
	if err != nil {
		return nil, "", err
	}
	keys := []string{}
	for _, entry := range listed.Entries {
		if strings.HasPrefix(entry.Key, "artifact/") {
			id := strings.TrimPrefix(entry.Key, "artifact/")
			if id > cursor {
				keys = append(keys, id)
			}
		}
	}
	sort.Strings(keys)
	result := []*ArtifactDescriptor{}
	for _, id := range keys {
		if err = ctx.Err(); err != nil {
			return nil, "", err
		}
		d, e := s.DescribeArtifact(ctx, ArtifactURI(id))
		if e != nil {
			continue
		}
		if len(result) == limit {
			return result, result[len(result)-1].ID, nil
		}
		result = append(result, d)
	}
	return result, "", nil
}
