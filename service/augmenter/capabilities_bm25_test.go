package augmenter

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	auth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/capability"
)

func lexicalFixture() CapabilityRequest {
	items := []capability.Candidate{{Kind: "tool", Name: "calendar:create", Description: "Create calendar events", LookupNames: []string{"calendar_create"}}, {Kind: "skill", Name: "calendar-help", Description: "Instructions for calendar scheduling"}, {Kind: "tool", Name: "files:read", Description: "Read document contents", Retained: true}}
	for i := 0; i < 50; i++ {
		items = append(items, capability.Candidate{Kind: "tool", Name: fmt.Sprintf("misc:item%d", i), Description: "Unrelated catalog operation"})
	}
	return CapabilityRequest{Model: "bm25", Query: "calendar scheduling", Limit: 1, Candidates: items}
}
func TestBM25CapabilitiesReuseAndVisibility(t *testing.T) {
	s := &Service{}
	ctx := auth.WithCanonicalUserID(context.Background(), "alice")
	r := lexicalFixture()
	got, err := s.RankCapabilities(ctx, r)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "files:read", got[1].Name)
	require.Len(t, s.capabilityVectors.entries, 1)
	var first *lexicalCapabilities
	for _, e := range s.capabilityVectors.entries {
		first = e.lexical
	}
	_, err = s.RankCapabilities(ctx, r)
	require.NoError(t, err)
	require.Len(t, s.capabilityVectors.entries, 1)
	for _, e := range s.capabilityVectors.entries {
		require.Same(t, first, e.lexical)
	}
	other := auth.WithCanonicalUserID(context.Background(), "bob")
	_, err = s.RankCapabilities(other, r)
	require.NoError(t, err)
	require.Len(t, s.capabilityVectors.entries, 2)
	// Removing a capability must not reuse the old ranked presentation.
	r.Candidates = r.Candidates[1:]
	r.Query = "calendar:create"
	got, err = s.RankCapabilities(ctx, r)
	require.NoError(t, err)
	for _, c := range got {
		require.NotEqual(t, "calendar:create", c.Name)
	}
}
func TestBM25CapabilitiesExactUnknownAndConcurrent(t *testing.T) {
	s := &Service{}
	ctx := auth.WithCanonicalUserID(context.Background(), "alice")
	r := lexicalFixture()
	r.Query = "calendar_create."
	got, err := s.RankCapabilities(ctx, r)
	require.NoError(t, err)
	found := false
	for _, c := range got {
		found = found || c.Name == "calendar:create"
	}
	require.True(t, found)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.RankCapabilities(ctx, r)
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	r.Query = "zxqnotfound"
	got, err = s.RankCapabilities(ctx, r)
	require.NoError(t, err)
	require.Empty(t, got, "no lexical match lets the caller retain ordinary compact discovery")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.RankCapabilities(cancelled, r)
	require.ErrorIs(t, err, context.Canceled)
}

func BenchmarkBM25CapabilitiesWarm(b *testing.B) {
	s := &Service{}
	ctx := auth.WithCanonicalUserID(context.Background(), "benchmark-user")
	r := lexicalFixture()
	for i := len(r.Candidates); i < 500; i++ {
		r.Candidates = append(r.Candidates, capability.Candidate{Kind: "tool", Name: fmt.Sprintf("documents:search%d", i), Description: "Search documents and return matching excerpts"})
	}
	_, err := s.RankCapabilities(ctx, r)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.RankCapabilities(ctx, r); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if len(s.capabilityVectors.entries) != 1 {
		b.Fatal("warm requests rebuilt catalog identities")
	}
}

func TestBM25CanonicalToolReferenceDoesNotRetainSkillAlias(t *testing.T) {
	s := &Service{}
	r := lexicalFixture()
	r.Limit = 1
	r.Query = "calendar scheduling ops:guide"
	r.Candidates = append(r.Candidates, capability.Candidate{Kind: "skill", Name: "ops-guide", Description: "Opaque procedure"})
	got, err := s.RankCapabilities(context.Background(), r)
	require.NoError(t, err)
	for _, c := range got {
		require.NotEqual(t, "ops-guide", c.Name, "tool canonicalization must not create exact skill aliases")
	}
}
