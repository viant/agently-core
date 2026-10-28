package augmenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/sync/errgroup"

	baseembed "github.com/viant/agently-core/genai/embedder/provider/base"
	auth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/runtime/capability"
	"github.com/viant/agently-core/runtime/requestctx"
)

// CapabilityRequest contains only the caller's current authorized metadata.
// Ranking changes the initial prompt; it does not change discovery permissions.
type CapabilityRequest struct {
	Model                 string
	Query                 string
	Limit                 int
	Candidates            []capability.Candidate
	NamespaceDescriptions map[string]string
}

type capabilityVectors struct {
	vectors [][]float32
	expires time.Time
	used    time.Time
	size    int
}

type capabilityVectorCache struct {
	mu      sync.Mutex
	entries map[[32]byte]*capabilityVectors
	bytes   int
}

// RankCapabilities retrieves candidates from independently retained namespace
// partitions, then merges their cosine scores into one initial candidate budget.
// Exact references and retained capabilities are additional to that budget.
func (s *Service) RankCapabilities(ctx context.Context, request CapabilityRequest) ([]capability.Candidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit := request.Limit
	if limit <= 0 {
		limit = capability.DefaultCandidateLimit
	}
	if len(request.Candidates) <= limit {
		return append([]capability.Candidate(nil), request.Candidates...), nil
	}
	if s == nil || s.finder == nil || strings.TrimSpace(request.Model) == "" {
		return nil, fmt.Errorf("capability embedder unavailable")
	}
	embedder, err := s.finder.Find(ctx, request.Model)
	if err != nil {
		return nil, err
	}
	if embedder == nil {
		return nil, fmt.Errorf("capability embedder unavailable")
	}
	modelIdentity := request.Model + fmt.Sprintf(":%T:%p", embedder, embedder)
	persistent := false
	if stable, ok := s.finder.(interface {
		EmbeddingCacheIdentity(context.Context, string) (string, error)
	}); ok {
		identity, identityErr := stable.EmbeddingCacheIdentity(ctx, request.Model)
		if identityErr == nil && identity != "" {
			modelIdentity = request.Model + ":" + identity
			persistent = true
		} else {
			traceCapabilitySnapshot("model_identity", identityErr)
		}
	} else if versioned, ok := s.finder.(interface{ Version() int64 }); ok {
		modelIdentity += fmt.Sprintf(":version:%d", versioned.Version())
	}
	scope, err := capabilityEmbeddingScope(ctx, modelIdentity, "query")
	if err != nil {
		return nil, err
	}
	queryVectors, err := s.cachedCapabilityVectors(ctx, embedder, scope, "query", []string{request.Query}, false, 0)
	if err != nil {
		return nil, err
	}
	partitions := map[string][]capability.Candidate{}
	for _, candidate := range request.Candidates {
		namespace := candidate.Namespace
		if namespace == "" {
			namespace = capability.DefaultNamespace
			candidate.Namespace = namespace
		}
		visibility := candidate.CacheScope
		if visibility != "public" && visibility != "conversation" {
			visibility = "user"
		}
		partition := namespace + "\x00" + visibility
		partitions[partition] = append(partitions[partition], candidate)
	}
	namespaces := make([]string, 0, len(partitions))
	for namespace := range partitions {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	type scored struct {
		candidate capability.Candidate
		score     float64
	}
	var ranked []scored
	var rankedMu sync.Mutex
	group, workerCtx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for _, partition := range namespaces {
		partition := partition
		group.Go(func() error {
			candidates := partitions[partition]
			namespace := candidates[0].Namespace
			visibility := strings.SplitN(partition, "\x00", 2)[1]
			scope, err := capabilityEmbeddingScope(ctx, modelIdentity, visibility)
			if err != nil {
				return err
			}
			sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID() < candidates[j].ID() })
			texts := make([]string, len(candidates))
			for i, candidate := range candidates {
				texts[i] = fmt.Sprintf("Kind: %s\nNamespace: %s\nNamespace description: %s\nName: %s\nDescription: %s\nURI: %s", candidate.Kind, namespace, request.NamespaceDescriptions[namespace], candidate.Name, candidate.Description, candidate.URI)
			}
			identities := make([]string, len(candidates))
			for i, candidate := range candidates {
				identities[i] = candidate.ID()
			}
			identityBytes, _ := json.Marshal(identities)
			vectors, err := s.cachedCapabilityVectors(workerCtx, embedder, scope, fmt.Sprintf("namespace:%s:%x", namespace, sha256.Sum256(identityBytes)), texts, persistent, len(queryVectors[0]))
			if err != nil {
				return err
			}
			partitionResults := make([]scored, 0, len(candidates))
			for i, candidate := range candidates {
				score, err := capabilityCosine(queryVectors[0], vectors[i])
				if err != nil {
					return err
				}
				partitionResults = append(partitionResults, scored{candidate, score})
			}
			rankedMu.Lock()
			ranked = append(ranked, partitionResults...)
			rankedMu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].candidate.ID() < ranked[j].candidate.ID()
	})
	result := make([]capability.Candidate, 0, limit)
	selected := map[string]bool{}
	for i, item := range ranked {
		if i >= limit && !item.candidate.Retained && !exactCapabilityReference(request.Query, item.candidate) {
			continue
		}
		if !selected[item.candidate.ID()] {
			result = append(result, item.candidate)
			selected[item.candidate.ID()] = true
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func capabilityEmbeddingScope(ctx context.Context, model, visibility string) ([32]byte, error) {
	parts := []any{model, visibility}
	if visibility != "public" {
		// Canonical identity is stable across token refreshes. Provider and
		// effective identity retain the framework's principal isolation fence.
		principal := auth.CanonicalUserID(ctx)
		if principal == "" {
			principal = auth.EffectiveUserID(ctx)
		}
		parts = append(parts, principal, auth.Provider(ctx))
		if visibility == "conversation" || visibility == "query" || auth.EffectiveUserID(ctx) == "" && auth.CanonicalUserID(ctx) == "" {
			parts = append(parts, requestctx.ConversationIDFromContext(ctx))
		}
		if visibility == "query" {
			parts = append(parts, auth.Bearer(ctx), auth.IDToken(ctx), auth.MCPAuthToken(ctx, false), auth.MCPAuthToken(ctx, true))
		}
	}
	raw, err := json.Marshal(parts)
	return sha256.Sum256(raw), err
}

func (s *Service) cachedCapabilityVectors(ctx context.Context, embedder baseembed.Embedder, scope [32]byte, partition string, texts []string, persistent bool, dimensions int) ([][]float32, error) {
	raw, _ := json.Marshal([]any{scope, partition, texts})
	key := sha256.Sum256(raw)
	// Single-flight is limited to one authorized scope and exact metadata digest.
	pending := s.capabilityLoads.DoChan(fmt.Sprintf("%x", key), func() (any, error) {
		now := time.Now()
		s.capabilityVectors.mu.Lock()
		if held := s.capabilityVectors.entries[key]; held != nil && (held.expires.IsZero() || held.expires.After(now)) {
			held.used = now
			vectors := held.vectors
			s.capabilityVectors.mu.Unlock()
			return vectors, nil
		}
		s.capabilityVectors.mu.Unlock()
		var vectors [][]float32
		path := ""
		if persistent && partition != "query" {
			var pathErr error
			path, pathErr = s.capabilitySnapshotPath(ctx, scope, key)
			traceCapabilitySnapshot("path", pathErr)
			if pathErr == nil {
				var readErr error
				vectors, readErr = readCapabilitySnapshot(ctx, path, key, len(texts), dimensions)
				traceCapabilitySnapshot("read", readErr)
			}
		}
		loaded := len(vectors) == len(texts)
		if !loaded {
			// Respect common provider batch/token bounds without dropping text.
			for start := 0; start < len(texts); {
				end, bytes := start, 0
				for end < len(texts) && end-start < 128 {
					if end > start && bytes+len(texts[end]) > 128*1024 {
						break
					}
					bytes += len(texts[end])
					end++
				}
				batch, _, err := embedder.Embed(ctx, texts[start:end])
				if err != nil {
					return nil, err
				}
				if len(batch) != end-start {
					return nil, fmt.Errorf("capability embedding count mismatch")
				}
				// Providers may reuse scratch vectors between calls.
				for _, vector := range batch {
					vectors = append(vectors, append([]float32(nil), vector...))
				}
				start = end
			}
		}
		for _, vector := range vectors {
			if len(vector) == 0 || dimensions > 0 && len(vector) != dimensions {
				return nil, fmt.Errorf("empty capability embedding")
			}
			for _, value := range vector {
				if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
					return nil, fmt.Errorf("invalid capability embedding")
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !loaded && path != "" {
			traceCapabilitySnapshot("write", writeCapabilitySnapshot(ctx, path, key, vectors))
		}
		s.capabilityVectors.mu.Lock()
		defer s.capabilityVectors.mu.Unlock()
		if s.capabilityVectors.entries == nil {
			s.capabilityVectors.entries = map[[32]byte]*capabilityVectors{}
		}
		size := 0
		for _, vector := range vectors {
			size += len(vector) * 4
		}
		if size > 64<<20 {
			return vectors, nil
		}
		for heldKey, held := range s.capabilityVectors.entries {
			if !held.expires.IsZero() && !held.expires.After(now) {
				s.capabilityVectors.bytes -= held.size
				delete(s.capabilityVectors.entries, heldKey)
			}
		}
		for len(s.capabilityVectors.entries) >= 256 || s.capabilityVectors.bytes+size > 64<<20 {
			var oldest [32]byte
			var oldestTime time.Time
			for heldKey, held := range s.capabilityVectors.entries {
				if oldestTime.IsZero() || held.used.Before(oldestTime) {
					oldest = heldKey
					oldestTime = held.used
				}
			}
			s.capabilityVectors.bytes -= s.capabilityVectors.entries[oldest].size
			delete(s.capabilityVectors.entries, oldest)
		}
		owned := make([][]float32, len(vectors))
		for i, vector := range vectors {
			owned[i] = append([]float32(nil), vector...)
		}
		expires := capabilitySnapshotExpiry(partition, now)
		if old := s.capabilityVectors.entries[key]; old != nil {
			s.capabilityVectors.bytes -= old.size
		}
		s.capabilityVectors.entries[key] = &capabilityVectors{vectors: owned, expires: expires, used: now, size: size}
		s.capabilityVectors.bytes += size
		return owned, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-pending:
		if result.Err != nil {
			return nil, result.Err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return result.Val.([][]float32), nil
	}
}

func capabilityCosine(a, b []float32) (float64, error) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, fmt.Errorf("capability embedding dimensions mismatch")
	}
	var dot, aa, bb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		aa += x * x
		bb += y * y
	}
	if aa == 0 || bb == 0 {
		return 0, nil
	}
	return dot / math.Sqrt(aa*bb), nil
}

func exactCapabilityReference(query string, candidate capability.Candidate) bool {
	references := append([]string{candidate.Name, candidate.URI}, candidate.LookupNames...)
	for _, reference := range references {
		if reference == "" {
			continue
		}
		for _, token := range strings.FieldsFunc(query, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("\"'`()[]{}<>,;", r) }) {
			token = strings.TrimSuffix(token, ".")
			if strings.EqualFold(token, reference) || candidate.Kind == "tool" && strings.EqualFold(mcpname.Canonical(token), mcpname.Canonical(reference)) {
				return true
			}
		}
	}
	return false
}
