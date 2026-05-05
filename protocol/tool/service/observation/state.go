package observation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/viant/afs/file"
	afsurl "github.com/viant/afs/url"
)

type stateKey struct{}

type State struct {
	mu      sync.RWMutex
	records map[string]Record
}

type Metadata struct {
	Token           string `json:"token"`
	SHA256          string `json:"sha256"`
	Size            int    `json:"size"`
	ContentComplete bool   `json:"contentComplete"`
	ObservedAt      string `json:"observedAt,omitempty"`
}

type Record struct {
	URI        string
	Canonical  string
	SHA256     string
	Size       int
	ObservedAt string
}

func WithState(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if StateFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, stateKey{}, &State{records: map[string]Record{}})
}

func StateFromContext(ctx context.Context) *State {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(stateKey{}).(*State)
	return state
}

func IsEnforced(ctx context.Context) bool {
	return StateFromContext(ctx) != nil
}

func RecordRead(ctx context.Context, uri string, data []byte, contentComplete bool) Metadata {
	metadata := MetadataFor(data, contentComplete, time.Now().UTC())
	state := StateFromContext(ctx)
	if state == nil {
		return metadata
	}
	canonical := CanonicalURI(uri)
	if canonical == "" {
		return metadata
	}
	state.mu.Lock()
	state.records[canonical] = Record{
		URI:        strings.TrimSpace(uri),
		Canonical:  canonical,
		SHA256:     metadata.SHA256,
		Size:       metadata.Size,
		ObservedAt: metadata.ObservedAt,
	}
	state.mu.Unlock()
	return metadata
}

func MetadataFor(data []byte, contentComplete bool, observedAt time.Time) Metadata {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	return Metadata{
		Token:           "sha256:" + hash,
		SHA256:          hash,
		Size:            len(data),
		ContentComplete: contentComplete,
		ObservedAt:      observedAt.UTC().Format(time.RFC3339Nano),
	}
}

func VerifyCurrent(ctx context.Context, uri string, data []byte) error {
	state := StateFromContext(ctx)
	if state == nil {
		return nil
	}
	canonical := CanonicalURI(uri)
	if canonical == "" {
		return fmt.Errorf("target file must be read with resources:read before patching")
	}
	current := MetadataFor(data, true, time.Now().UTC())
	state.mu.RLock()
	record, ok := state.records[canonical]
	state.mu.RUnlock()
	if !ok {
		return fmt.Errorf("target file must be read with resources:read before patching")
	}
	if record.SHA256 != current.SHA256 || record.Size != current.Size {
		return fmt.Errorf("target file changed after resources:read; read it again before patching")
	}
	return nil
}

func CanonicalURI(uri string) string {
	value := strings.TrimSpace(uri)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "file://") {
		_, path := afsurl.Base(value, file.Scheme)
		if strings.TrimSpace(path) == "" {
			return ""
		}
		return filepath.Clean(path)
	}
	if filepath.IsAbs(value) || isWindowsAbsPath(value) {
		return filepath.Clean(value)
	}
	return strings.TrimRight(value, "/")
}

func isWindowsAbsPath(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	return (value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')
}
