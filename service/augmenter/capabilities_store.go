package augmenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/workspace"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

const capabilitySnapshotVersion = 1
const capabilitySnapshotLimit = 128 << 20

type capabilitySnapshot struct {
	Version    int         `json:"version"`
	Key        string      `json:"key"`
	Count      int         `json:"count"`
	Dimensions int         `json:"dimensions"`
	Vectors    [][]float32 `json:"vectors"`
	Digest     string      `json:"digest"`
}

// SetCapabilityStateStore is startup-only. A custom store must return local
// filesystem paths for atomic snapshot publication; unsupported paths retain
// ordinary memory caching without falling back to another state destination.
func (s *Service) SetCapabilityStateStore(store workspace.StateStore) { s.capabilityStateStore = store }
func WithCapabilityStateStore(store workspace.StateStore) func(*Service) {
	return func(s *Service) { s.SetCapabilityStateStore(store) }
}

func (s *Service) capabilitySnapshotPath(ctx context.Context, scope, key [32]byte) (string, error) {
	store := s.capabilityStateStore
	if store == nil {
		store = fsstore.NewStateStore(workspace.StateRoot())
	}
	directory, err := store.StatePath(ctx, filepath.Join("discovery", "embeddings", "v1", hex.EncodeToString(scope[:])))
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(directory) {
		return "", fmt.Errorf("capability snapshot store requires local filesystem paths")
	}
	return filepath.Join(directory, hex.EncodeToString(key[:])+".json"), nil
}

func readCapabilitySnapshot(ctx context.Context, path string, key [32]byte, count, dimensions int) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var snapshot capabilitySnapshot
	decoder := json.NewDecoder(io.LimitReader(file, capabilitySnapshotLimit+1))
	if err = decoder.Decode(&snapshot); err != nil {
		return nil, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("invalid capability snapshot trailing data")
	}
	if snapshot.Version != capabilitySnapshotVersion || snapshot.Key != hex.EncodeToString(key[:]) || snapshot.Count != count || len(snapshot.Vectors) != count || snapshot.Dimensions <= 0 || dimensions > 0 && snapshot.Dimensions != dimensions {
		return nil, fmt.Errorf("incompatible capability snapshot")
	}
	for _, vector := range snapshot.Vectors {
		if len(vector) != snapshot.Dimensions {
			return nil, fmt.Errorf("invalid capability snapshot dimensions")
		}
		for _, value := range vector {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return nil, fmt.Errorf("invalid capability snapshot vector")
			}
		}
	}
	encoded, err := json.Marshal(snapshot.Vectors)
	if err != nil {
		return nil, err
	}
	if snapshot.Digest != fmt.Sprintf("%x", sha256.Sum256(encoded)) {
		return nil, fmt.Errorf("capability snapshot checksum mismatch")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return snapshot.Vectors, nil
}

func writeCapabilitySnapshot(ctx context.Context, path string, key [32]byte, vectors [][]float32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(vectors) == 0 {
		return nil
	}
	vectorBytes, err := json.Marshal(vectors)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(capabilitySnapshot{Version: capabilitySnapshotVersion, Key: hex.EncodeToString(key[:]), Count: len(vectors), Dimensions: len(vectors[0]), Vectors: vectors, Digest: fmt.Sprintf("%x", sha256.Sum256(vectorBytes))})
	if err != nil {
		return err
	}
	if len(raw) > capabilitySnapshotLimit {
		return fmt.Errorf("capability snapshot size limit exceeded")
	}
	directory := filepath.Dir(path)
	if err = os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".vectors-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	defer file.Close()
	if _, err = file.Write(raw); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(temporary, path); err != nil {
		return err
	}
	// The file and its directory entry must both survive a process restart.
	if dir, openErr := os.Open(directory); openErr == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func traceCapabilitySnapshot(stage string, err error) {
	if err == nil || os.IsNotExist(err) {
		return
	}
	debugtrace.Write("augmenter", "capability_snapshot_unavailable", map[string]any{"stage": stage, "errorType": fmt.Sprintf("%T", err)})
}

// Metadata retention is content-based. Query snapshots are never persisted.
func capabilitySnapshotExpiry(partition string, now time.Time) time.Time {
	if partition == "query" {
		return now.Add(5 * time.Minute)
	}
	return time.Time{}
}
