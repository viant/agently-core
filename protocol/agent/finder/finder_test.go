package finder

import (
	"context"
	"errors"
	"testing"

	agentmdl "github.com/viant/agently-core/protocol/agent"
)

type stubLoader struct {
	items map[string]*agentmdl.Agent
	err   error
	calls int
}

func (s *stubLoader) Add(name string, agent *agentmdl.Agent) {
	if s.items == nil {
		s.items = map[string]*agentmdl.Agent{}
	}
	s.items[name] = agent
}

func (s *stubLoader) Load(ctx context.Context, name string) (*agentmdl.Agent, error) {
	_ = ctx
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.items[name], nil
}

func TestFinder_Find_DataDriven(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name           string
		preload        map[string]*agentmdl.Agent
		loaderItems    map[string]*agentmdl.Agent
		loaderErr      error
		findKey        string
		wantErr        bool
		wantNil        bool
		wantName       string
		wantLoadCalls  int
		expectCacheSet bool
	}{
		{
			name:          "cache hit no loader call",
			preload:       map[string]*agentmdl.Agent{"a1": {Identity: agentmdl.Identity{Name: "cached"}}},
			findKey:       "a1",
			wantName:      "cached",
			wantLoadCalls: 0,
		},
		{
			name:           "cache miss loads from loader",
			loaderItems:    map[string]*agentmdl.Agent{"a2": {Identity: agentmdl.Identity{Name: "loaded"}}},
			findKey:        "a2",
			wantName:       "loaded",
			wantLoadCalls:  1,
			expectCacheSet: true,
		},
		{
			name:          "cache miss loader error",
			loaderErr:     errors.New("boom"),
			findKey:       "a3",
			wantErr:       true,
			wantLoadCalls: 1,
		},
		{
			name:    "cache miss no loader returns not found",
			findKey: "a4",
			wantErr: true,
		},
		{
			name:           "stub cache entry is refreshed via loader",
			preload:        map[string]*agentmdl.Agent{"a5": {Identity: agentmdl.Identity{ID: "a5"}}},
			loaderItems:    map[string]*agentmdl.Agent{"a5": {Identity: agentmdl.Identity{ID: "a5", Name: "hydrated"}, Source: &agentmdl.Source{URL: "file://a5.yaml"}}},
			findKey:        "a5",
			wantName:       "hydrated",
			wantLoadCalls:  1,
			expectCacheSet: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ldr := &stubLoader{items: tc.loaderItems, err: tc.loaderErr}
			var opts []Option
			if tc.loaderItems != nil || tc.loaderErr != nil {
				opts = append(opts, WithLoader(ldr))
			}
			f := New(opts...)
			for key, value := range tc.preload {
				f.Add(key, value)
			}

			got, err := f.Find(ctx, tc.findKey)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
			} else if err != nil {
				t.Fatalf("Find() error: %v", err)
			}

			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil result, got %#v", got)
				}
			} else if !tc.wantErr {
				if got == nil {
					t.Fatalf("expected non-nil result")
				}
				if tc.wantName != "" && got.Name != tc.wantName {
					t.Fatalf("unexpected name: got=%q want=%q", got.Name, tc.wantName)
				}
			}

			if ldr.calls != tc.wantLoadCalls {
				t.Fatalf("unexpected loader calls: got=%d want=%d", ldr.calls, tc.wantLoadCalls)
			}

			if tc.expectCacheSet {
				cached, ok := f.items[tc.findKey]
				if !ok || cached == nil {
					t.Fatalf("expected cached item for %q", tc.findKey)
				}
			}
		})
	}
}

func TestFinder_VersionAndRemove(t *testing.T) {
	f := New()
	if got := f.Version(); got != 0 {
		t.Fatalf("unexpected initial version: %d", got)
	}

	f.Add("a1", &agentmdl.Agent{Identity: agentmdl.Identity{ID: "a1"}})
	v1 := f.Version()
	if v1 == 0 {
		t.Fatalf("expected version increment after add")
	}

	f.Remove("a1")
	v2 := f.Version()
	if v2 <= v1 {
		t.Fatalf("expected version increment after remove, got v1=%d v2=%d", v1, v2)
	}
}

func TestWithInitial_UsesNameWhenIDMissing(t *testing.T) {
	f := New(WithInitial(
		&agentmdl.Agent{Identity: agentmdl.Identity{Name: "named-only"}},
	))

	got, err := f.Find(context.Background(), "named-only")
	if err != nil {
		t.Fatalf("Find() error: %v", err)
	}
	if got == nil || got.Name != "named-only" {
		t.Fatalf("unexpected finder result: %#v", got)
	}
}
