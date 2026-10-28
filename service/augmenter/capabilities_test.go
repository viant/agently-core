package augmenter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	baseembed "github.com/viant/agently-core/genai/embedder/provider/base"
	auth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/capability"
	"github.com/viant/agently-core/runtime/requestctx"
)

type capabilityTestEmbedder struct {
	mu                sync.Mutex
	calls             int
	texts             [][]string
	entered           chan struct{}
	release           chan struct{}
	failure           error
	documentDelay     time.Duration
	active, maxActive int
}

func (e *capabilityTestEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, int, error) {
	e.mu.Lock()
	e.calls++
	e.texts = append(e.texts, append([]string(nil), texts...))
	e.mu.Unlock()
	if e.entered != nil {
		select {
		case e.entered <- struct{}{}:
		default:
		}
	}
	if e.release != nil {
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
	if len(texts) > 0 && strings.HasPrefix(texts[0], "Kind:") && e.documentDelay > 0 {
		e.mu.Lock()
		e.active++
		if e.active > e.maxActive {
			e.maxActive = e.active
		}
		e.mu.Unlock()
		timer := time.NewTimer(e.documentDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		}
		e.mu.Lock()
		e.active--
		e.mu.Unlock()
	}
	if e.failure != nil {
		return nil, 0, e.failure
	}
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		if strings.Contains(strings.ToLower(text), "calendar") {
			vectors[i] = []float32{1, 0}
		} else {
			vectors[i] = []float32{0, 1}
		}
	}
	return vectors, 0, nil
}
func (e *capabilityTestEmbedder) count() int { e.mu.Lock(); defer e.mu.Unlock(); return e.calls }

type capabilityTestFinder struct{ client *capabilityTestEmbedder }

func (f capabilityTestFinder) Find(context.Context, string) (baseembed.Embedder, error) {
	return f.client, nil
}
func (capabilityTestFinder) Ids() []string { return []string{"fixture"} }
func capabilityTestContext(user, conversation string) context.Context {
	ctx := context.Background()
	if user != "" {
		ctx = auth.WithUserInfo(ctx, &auth.UserInfo{Subject: user})
	}
	return requestctx.WithConversationID(ctx, conversation)
}
func capabilityTestCandidates() []capability.Candidate {
	return []capability.Candidate{
		{Kind: "tool", Name: "north-calendar", Description: "Calendar scheduling", Namespace: "north", Provider: "owner-a"},
		{Kind: "skill", Name: "south/review", Description: "Review calendar scheduling", URI: "skill://south/review/SKILL.md", Namespace: "south", Provider: "owner-b"},
		{Kind: "tool", Name: "exact-opaque", Description: "Opaque metadata operation", Namespace: "north", Provider: "owner-a"},
		{Kind: "tool", Name: "helpers-discover", Description: "Authorized discovery", Namespace: "default", Retained: true},
		{Kind: "tool", Name: "other-neutral", Description: "Unrelated operation", Namespace: "default"},
	}
}

func TestCapabilityRankingRetrievesToolsAndSkillsAndRetainsExactReferences(t *testing.T) {
	embedder := &capabilityTestEmbedder{}
	service := New(capabilityTestFinder{embedder})
	request := CapabilityRequest{Model: "fixture", Query: "calendar and exact:opaque", Limit: 2, Candidates: capabilityTestCandidates()}
	result, err := service.RankCapabilities(capabilityTestContext("alice", "one"), request)
	require.NoError(t, err)
	var names []string
	for _, candidate := range result {
		names = append(names, candidate.Name)
	}
	require.ElementsMatch(t, []string{"north-calendar", "south/review", "exact-opaque", "helpers-discover"}, names)
	require.Equal(t, "Review calendar scheduling", result[0].Description, "ranked metadata preserves authored descriptions")
	require.Equal(t, 4, embedder.count(), "one query and three independent namespace partitions")
}

func TestCapabilityVectorCacheIsolatesScopesAndInvalidatesMetadata(t *testing.T) {
	embedder := &capabilityTestEmbedder{}
	service := New(capabilityTestFinder{embedder})
	request := CapabilityRequest{Model: "fixture", Query: "calendar", Limit: 2, Candidates: capabilityTestCandidates()}
	ctx := capabilityTestContext("alice", "one")
	_, err := service.RankCapabilities(ctx, request)
	require.NoError(t, err)
	initial := embedder.count()
	_, err = service.RankCapabilities(ctx, request)
	require.NoError(t, err)
	require.Equal(t, initial, embedder.count(), "same caller/conversation metadata reuses retained embeddings")
	request.Candidates[1].Description += " updated"
	_, err = service.RankCapabilities(ctx, request)
	require.NoError(t, err)
	require.Equal(t, initial+1, embedder.count(), "one changed namespace invalidates only its embedding partition")
	request.NamespaceDescriptions = map[string]string{"default": "Workspace discovery guidance"}
	_, err = service.RankCapabilities(ctx, request)
	require.NoError(t, err)
	require.Equal(t, initial+2, embedder.count(), "default description participates in the vector digest")
	previous := embedder.count()
	for _, tc := range []struct {
		ctx   context.Context
		calls int
	}{
		{capabilityTestContext("bob", "one"), 4},
		{capabilityTestContext("alice", "two"), 1},
		{capabilityTestContext("", "one"), 4},
		{auth.WithProvider(ctx, "another-issuer"), 4},
		{auth.WithBearer(ctx, "synthetic-revised-credential"), 1},
	} {
		_, err = service.RankCapabilities(tc.ctx, request)
		require.NoError(t, err)
		require.Equal(t, previous+tc.calls, embedder.count(), "private catalog vectors follow stable user scope; query vectors additionally follow conversation and credentials")
		previous = embedder.count()
	}
	request.Model = "another-model"
	_, err = service.RankCapabilities(ctx, request)
	require.NoError(t, err)
	require.Equal(t, previous+4, embedder.count())
}

func TestCapabilityRankingPreservesCancellationWhileWaiting(t *testing.T) {
	embedder := &capabilityTestEmbedder{entered: make(chan struct{}, 1), release: make(chan struct{})}
	service := New(capabilityTestFinder{embedder})
	request := CapabilityRequest{Model: "fixture", Query: "calendar", Limit: 2, Candidates: capabilityTestCandidates()}
	ctx := capabilityTestContext("alice", "one")
	done := make(chan error, 1)
	go func() { _, err := service.RankCapabilities(ctx, request); done <- err }()
	select {
	case <-embedder.entered:
	case <-time.After(time.Second):
		t.Fatal("embedding did not start")
	}
	canceled, cancel := context.WithCancel(ctx)
	waiter := make(chan error, 1)
	go func() { _, err := service.RankCapabilities(canceled, request); waiter <- err }()
	cancel()
	select {
	case err := <-waiter:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("canceled request blocked behind another scope load")
	}
	close(embedder.release)
	require.NoError(t, <-done)
}

func TestCapabilityRankingRejectsEmbeddingFailure(t *testing.T) {
	failure := errors.New("synthetic embedder offline")
	service := New(capabilityTestFinder{&capabilityTestEmbedder{failure: failure}})
	_, err := service.RankCapabilities(context.Background(), CapabilityRequest{Model: "fixture", Query: "calendar", Limit: 2, Candidates: capabilityTestCandidates()})
	require.ErrorIs(t, err, failure)
}

func BenchmarkCapabilityRankingRetainedPartitions(b *testing.B) {
	embedder := &capabilityTestEmbedder{}
	service := New(capabilityTestFinder{embedder})
	candidates := make([]capability.Candidate, 1000)
	for i := range candidates {
		candidates[i] = capability.Candidate{Kind: "tool", Name: fmt.Sprintf("tool-%04d", i), Description: "Synthetic calendar capability", Namespace: fmt.Sprintf("namespace-%d", i%10)}
	}
	request := CapabilityRequest{Model: "fixture", Query: "calendar", Limit: 40, Candidates: candidates}
	ctx := capabilityTestContext("alice", "one")
	if _, err := service.RankCapabilities(ctx, request); err != nil {
		b.Fatal(err)
	}
	initial := embedder.count()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := service.RankCapabilities(ctx, request); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(embedder.count()-initial), "new_embedding_calls")
}

func TestCapabilityVectorsUseDeclaredPublicUserAndConversationScopes(t *testing.T) {
	embedder := &capabilityTestEmbedder{}
	service := New(capabilityTestFinder{embedder})
	candidates := capabilityTestCandidates()
	candidates[0].CacheScope = "public"
	candidates[2].CacheScope = "public"
	candidates[1].CacheScope = "user"
	candidates[3].CacheScope = "conversation"
	candidates[4].CacheScope = "conversation"
	request := CapabilityRequest{Model: "fixture", Query: "calendar", Limit: 2, Candidates: candidates}
	ctx := capabilityTestContext("alice", "one")
	_, err := service.RankCapabilities(ctx, request)
	require.NoError(t, err)
	calls := embedder.count()
	_, err = service.RankCapabilities(capabilityTestContext("bob", "one"), request)
	require.NoError(t, err)
	require.Equal(t, calls+3, embedder.count(), "only query, user and conversation vectors reload; public namespace vectors are shared")
	calls = embedder.count()
	_, err = service.RankCapabilities(capabilityTestContext("alice", "two"), request)
	require.NoError(t, err)
	require.Equal(t, calls+2, embedder.count(), "new conversation reuses both public and same-user vectors")
	calls = embedder.count()
	_, err = service.RankCapabilities(auth.WithBearer(ctx, "refreshed-synthetic-token"), request)
	require.NoError(t, err)
	require.Equal(t, calls+1, embedder.count(), "token refresh changes query scope without reembedding unchanged metadata")
	service.capabilityVectors.mu.Lock()
	for _, entry := range service.capabilityVectors.entries {
		if len(entry.vectors) > 1 {
			require.True(t, entry.expires.IsZero(), "metadata embedding retention is content-based, not a periodic TTL")
		}
	}
	service.capabilityVectors.mu.Unlock()
}

func TestCapabilityEmbeddingBatchesLargeNamespace(t *testing.T) {
	embedder := &capabilityTestEmbedder{}
	service := New(capabilityTestFinder{embedder})
	candidates := make([]capability.Candidate, 300)
	for i := range candidates {
		candidates[i] = capability.Candidate{Kind: "tool", Name: fmt.Sprintf("calendar-%d", i), Namespace: "one", CacheScope: "public"}
	}
	_, err := service.RankCapabilities(context.Background(), CapabilityRequest{Model: "fixture", Query: "calendar", Limit: 2, Candidates: candidates})
	require.NoError(t, err)
	require.Equal(t, 4, embedder.count(), "one query and three provider-sized document batches")
	for _, batch := range embedder.texts {
		require.LessOrEqual(t, len(batch), 128)
	}
}

func TestCapabilityNamespacesPopulateWithBoundedParallelism(t *testing.T) {
	embedder := &capabilityTestEmbedder{documentDelay: 10 * time.Millisecond}
	service := New(capabilityTestFinder{embedder})
	var candidates []capability.Candidate
	for i := 0; i < 12; i++ {
		candidates = append(candidates, capability.Candidate{Kind: "tool", Name: fmt.Sprintf("calendar-%d", i), Namespace: fmt.Sprintf("partition-%d", i), CacheScope: "public"})
	}
	_, err := service.RankCapabilities(context.Background(), CapabilityRequest{Model: "fixture", Query: "calendar", Limit: 2, Candidates: candidates})
	require.NoError(t, err)
	require.Greater(t, embedder.maxActive, 1)
	require.LessOrEqual(t, embedder.maxActive, 4)
}

func TestCapabilityExactSkillAliasSurvivesCandidateBudget(t *testing.T) {
	embedder := &capabilityTestEmbedder{}
	service := New(capabilityTestFinder{embedder})
	candidates := capabilityTestCandidates()
	candidates = append(candidates, capability.Candidate{Kind: "skill", Name: "remote/opaque", Description: "Opaque procedure", URI: "skill://remote/opaque/SKILL.md", Namespace: "remote", LookupNames: []string{"opaque"}})
	result, err := service.RankCapabilities(context.Background(), CapabilityRequest{Model: "fixture", Query: "calendar and opaque", Limit: 1, Candidates: candidates})
	require.NoError(t, err)
	have := false
	for _, candidate := range result {
		have = have || candidate.Name == "remote/opaque"
	}
	require.True(t, have, "a qualified skill retains its exact unqualified lookup alias beyond the initial budget")
}
