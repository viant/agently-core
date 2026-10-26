package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/viant/afs"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/service/meta"
	"github.com/viant/forge/backend/types"
)

type countedWindowSource struct {
	*canonicalSource
	reads, enumerations int
	readDelay           time.Duration
}

func (s *countedWindowSource) CheckCandidate(ctx context.Context, uri identity.ResourceURI, candidate identity.ResourceCandidate) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	current, err := s.canonicalSource.Candidates(ctx, uri)
	if err != nil {
		return err
	}
	for _, item := range current {
		if item == candidate {
			return nil
		}
	}
	return identity.ErrResourceStale
}

func (s *countedWindowSource) Candidates(ctx context.Context, uri identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	s.enumerations++
	return s.canonicalSource.Candidates(ctx, uri)
}

func (s *countedWindowSource) ReadCandidate(ctx context.Context, uri identity.ResourceURI, candidate identity.ResourceCandidate) (json.RawMessage, error) {
	s.reads++
	if s.readDelay > 0 {
		select {
		case <-time.After(s.readDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.canonicalSource.ReadCandidate(ctx, uri, candidate)
}

func windowRevalidationFixture(t *testing.T, source *countedWindowSource, policy *windowPolicyFixture, enrich func()) *MetadataWindowCatalog {
	t.Helper()
	resolver := &identity.ResourceResolver{Source: source, Policy: policy}
	root := t.TempDir()
	options := []WindowCatalogOption{WithWindowResourceResolver(func(context.Context, string) (*identity.ResourceResolver, identity.ResourceRef, error) {
		return resolver, identity.ResourceRef{URI: "window://example/overview"}, nil
	})}
	if enrich != nil {
		options = append(options, WithWindowResolvedDefinitionLoader(func(_ context.Context, _ string, _ identity.ResolvedResource, raw json.RawMessage) (*types.Window, error) {
			enrich()
			var window types.Window
			return &window, json.Unmarshal(raw, &window)
		}))
	}
	catalog, err := NewMetadataWindowCatalog(meta.New(afs.New(), root), root,
		[]SavedWindow{{WindowDefinitionSummary: WindowDefinitionSummary{WindowID: "overview", ResourceURI: "window://example/overview"}, Key: "unused"}}, options...)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func TestWindowGetRevalidatesWithoutDuplicateBodyRead(t *testing.T) {
	original := json.RawMessage(`{"windowKey":"overview","view":{"content":{"id":"approved"}}}`)
	for _, scenario := range []struct {
		name   string
		mutate func(*countedWindowSource, *windowPolicyFixture)
		denied bool
	}{
		{name: "unchanged"},
		{name: "revoked after first read", mutate: func(_ *countedWindowSource, policy *windowPolicyFixture) { policy.allowed = false }, denied: true},
		{name: "source changed after first read", mutate: func(source *countedWindowSource, _ *windowPolicyFixture) {
			source.content["1"] = json.RawMessage(`{"windowKey":"overview","view":{"content":{"id":"changed"}}}`)
		}, denied: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			source := &countedWindowSource{canonicalSource: &canonicalSource{content: map[string]json.RawMessage{"1": original}}}
			policy := &windowPolicyFixture{binding: "verified-account", omitted: "1", allowed: true}
			catalog := windowRevalidationFixture(t, source, policy, func() {
				if scenario.mutate != nil {
					scenario.mutate(source, policy)
				}
			})
			got, err := catalog.Get(context.Background(), &WindowDefinitionGetInput{WindowID: "overview"})
			if scenario.denied {
				if err == nil || got != nil {
					t.Fatalf("late authority/source change was accepted: result=%+v err=%v", got, err)
				}
			} else if err != nil || got == nil || got.Definition.View.Content.ID != "approved" {
				t.Fatalf("stable source failed: result=%+v err=%v", got, err)
			}
			wantEnumerations := 2
			if scenario.name == "source changed after first read" {
				wantEnumerations = 1 // CheckCandidate rejects before a second policy selection.
			}
			if source.reads != 1 || source.enumerations != wantEnumerations {
				t.Fatalf("source accesses: reads=%d enumerations=%d, want one body read and %d enumerations", source.reads, source.enumerations, wantEnumerations)
			}
		})
	}
}

type shorteningWindowPolicy struct{ calls int }

func (p *shorteningWindowPolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	p.calls++
	lease := time.Minute
	if p.calls >= 4 {
		lease = 10 * time.Second
	}
	return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: "verified-account", ValidUntil: time.Now().Add(lease)}, nil
}

func TestWindowGetLateRevalidationCapsReturnedLease(t *testing.T) {
	raw := json.RawMessage(`{"windowKey":"overview","view":{"content":{"id":"approved"}}}`)
	source := &countedWindowSource{canonicalSource: &canonicalSource{content: map[string]json.RawMessage{"1": raw}}}
	policy := &shorteningWindowPolicy{}
	resolver := &identity.ResourceResolver{Source: source, Policy: policy}
	root := t.TempDir()
	catalog, err := NewMetadataWindowCatalog(meta.New(afs.New(), root), root,
		[]SavedWindow{{WindowDefinitionSummary: WindowDefinitionSummary{WindowID: "overview", ResourceURI: "window://example/overview"}, Key: "unused"}},
		WithWindowResourceResolver(func(context.Context, string) (*identity.ResourceResolver, identity.ResourceRef, error) {
			return resolver, identity.ResourceRef{URI: "window://example/overview"}, nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	got, err := catalog.Get(context.Background(), &WindowDefinitionGetInput{WindowID: "overview"})
	if err != nil {
		t.Fatal(err)
	}
	if policy.calls < 4 || got.Definition.Resource == nil || got.Definition.Resource.ValidUntil.After(time.Now().Add(15*time.Second)) {
		t.Fatalf("late shorter authority lease was not retained: calls=%d pin=%+v", policy.calls, got.Definition.Resource)
	}
}

func BenchmarkWindowGetRevalidationSingleBodyRead(b *testing.B) {
	original := json.RawMessage(`{"windowKey":"overview","view":{"content":{"id":"approved"}}}`)
	source := &countedWindowSource{canonicalSource: &canonicalSource{content: map[string]json.RawMessage{"1": original}}, readDelay: 10 * time.Millisecond}
	policy := &windowPolicyFixture{binding: "verified-account", omitted: "1", allowed: true}
	resolver := &identity.ResourceResolver{Source: source, Policy: policy}
	root := b.TempDir()
	catalog, err := NewMetadataWindowCatalog(meta.New(afs.New(), root), root,
		[]SavedWindow{{WindowDefinitionSummary: WindowDefinitionSummary{WindowID: "overview", ResourceURI: "window://example/overview"}, Key: "unused"}},
		WithWindowResourceResolver(func(context.Context, string) (*identity.ResourceResolver, identity.ResourceRef, error) {
			return resolver, identity.ResourceRef{URI: "window://example/overview"}, nil
		}))
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := catalog.Get(context.Background(), &WindowDefinitionGetInput{WindowID: "overview"}); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if source.reads != b.N {
		b.Fatalf("body reads=%d want=%d", source.reads, b.N)
	}
}
