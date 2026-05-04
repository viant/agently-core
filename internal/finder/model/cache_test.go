package model

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm/provider"
)

// TestCandidatesCache_VersionKeyed verifies the cache contract:
//
//   - First call rebuilds and stores under the registry's current version.
//   - Subsequent calls at the same version return the cached slice
//     (identity-equal to the previous result).
//   - Mutations via AddConfig/Remove bump the version, invalidating the
//     cache and forcing the next call to rebuild.
//
// This is the behavioral lock-in for H2 — the optimization must not
// drift over time as new mutation paths get added.
func TestCandidatesCache_VersionKeyed(t *testing.T) {
	f := New()
	f.AddConfig("a", &provider.Config{ID: "a", Options: provider.Options{Model: "claude-haiku"}, Intelligence: 0.4, Speed: 0.9})
	f.AddConfig("b", &provider.Config{ID: "b", Options: provider.Options{Model: "claude-sonnet"}, Intelligence: 0.7, Speed: 0.6})

	first := f.Candidates()
	assert.Len(t, first, 2, "two configs registered → two candidates")

	// Same version → cache hit. Identity check (same backing array) proves
	// the second call did not rebuild.
	second := f.Candidates()
	assert.Len(t, second, 2)
	if len(first) > 0 && len(second) > 0 {
		assert.Same(t, &first[0], &second[0],
			"cache hit must return the same backing slice (no rebuild)")
	}

	// Mutate → version bumps → cache invalidates → next call rebuilds and
	// returns a fresh slice with the new size.
	f.AddConfig("c", &provider.Config{ID: "c", Options: provider.Options{Model: "claude-opus"}, Intelligence: 0.95, Speed: 0.3})
	third := f.Candidates()
	assert.Len(t, third, 3, "after AddConfig the cache must rebuild and include the new candidate")

	// Subsequent call at the new version is a cache hit again.
	fourth := f.Candidates()
	if len(third) > 0 && len(fourth) > 0 {
		assert.Same(t, &third[0], &fourth[0],
			"second call at new version must be a cache hit")
	}

	// Removal also bumps version and invalidates.
	f.Remove("a")
	fifth := f.Candidates()
	assert.Len(t, fifth, 2, "after Remove the cache must rebuild")
}

// TestCandidatesCache_Concurrent verifies the cache is safe under
// concurrent reads + a single mutator. Race detector catches any
// improperly-locked access path.
func TestCandidatesCache_Concurrent(t *testing.T) {
	f := New()
	f.AddConfig("a", &provider.Config{ID: "a", Options: provider.Options{Model: "claude-haiku"}, Intelligence: 0.4, Speed: 0.9})

	const readers = 16
	const reads = 200
	var wg sync.WaitGroup
	var totalCalls int64

	// Reader goroutines hammer Candidates() concurrently.
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < reads; j++ {
				_ = f.Candidates()
				atomic.AddInt64(&totalCalls, 1)
			}
		}()
	}

	// Mutator bumps the version periodically.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			f.AddConfig("dynamic", &provider.Config{
				ID:           "dynamic",
				Options:      provider.Options{Model: "x"},
				Intelligence: 0.5,
			})
		}
	}()

	wg.Wait()
	assert.Equal(t, int64(readers*reads), atomic.LoadInt64(&totalCalls))
	// One last call must reflect the final mutated state.
	final := f.Candidates()
	assert.NotEmpty(t, final)
}

// TestInvalidateCandidatesCache covers the explicit invalidation lever.
// Useful for tests / unusual reload paths that bypass the registry's
// normal mutation surface.
func TestInvalidateCandidatesCache(t *testing.T) {
	f := New()
	f.AddConfig("a", &provider.Config{ID: "a", Options: provider.Options{Model: "x"}})
	first := f.Candidates()
	assert.Len(t, first, 1)

	// Without an Add/Remove, version is unchanged. Manual invalidate must
	// still force a rebuild.
	f.invalidateCandidatesCache()
	second := f.Candidates()
	assert.Len(t, second, 1)
	if len(first) > 0 && len(second) > 0 {
		assert.NotSame(t, &first[0], &second[0],
			"invalidate must force a fresh build (different backing slice)")
	}
}
