package toolselection

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/primitive"
	"github.com/viant/agently-core/runtime/requestctx"
)

func TestCatalogIndexReusedAndCandidateRefreshInvalidatesCursor(t *testing.T) {
	candidates := []llm.Tool{candidate("north-Alpha"), candidate("north-Beta"), candidate("south-Gamma")}
	ctx := WithState(turnContext(), candidates)
	s, _ := FromContext(ctx)
	index := s.catalog
	query := primitive.CatalogQuery{Namespace: "north", NameTerms: []string{"alpha", "beta"}, Limit: 1}
	first, err := s.ListPage(ctx, query, 0)
	if err != nil || first.Complete || first.Total != 2 {
		t.Fatalf("first: %+v %v", first, err)
	}
	s.SetCandidates(candidates)
	if s.catalog != index {
		t.Fatal("unchanged candidate metadata rebuilt index")
	}
	query.Cursor = first.NextCursor
	if _, err = s.ListPage(ctx, query, 0); err != nil {
		t.Fatal(err)
	}
	if s.catalog != index {
		t.Fatal("list rebuilt candidate index")
	}
	other := WithState(turnContext(), candidates)
	different, _ := FromContext(other)
	if _, err = different.ListPage(other, query, 0); err != primitive.ErrCatalogCursor {
		t.Fatalf("cross-state cursor: %v", err)
	}
	s.SetCandidates(candidates[:1])
	if _, err = s.ListPage(ctx, query, 0); err != primitive.ErrCatalogCursor {
		t.Fatalf("stale candidate cursor: %v", err)
	}
}

func turnContext() context.Context {
	ctx := auth.WithCanonicalUserID(context.Background(), "alice")
	return requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{TurnID: "turn-1", ConversationID: "conversation-1"})
}
func candidate(name string) llm.Tool {
	return llm.Tool{Definition: llm.ToolDefinition{Name: name, Description: "Read calendar events", Parameters: map[string]interface{}{"secretSchema": "not metadata"}}}
}

func TestActivationUsesExactAuthorizedCandidatesAndRefreshRevokes(t *testing.T) {
	ctx := WithState(turnContext(), []llm.Tool{candidate("calendar/events:list"), candidate("mail/message:get")})
	s, _ := FromContext(ctx)
	if err := s.Activate([]string{"calendar/events:list", "calendar/*"}); err == nil {
		t.Fatal("wildcard activation accepted")
	}
	if len(s.SelectedNames()) != 0 {
		t.Fatal("invalid batch partially activated")
	}
	if err := s.Activate([]string{"calendar/events:list"}); err != nil {
		t.Fatal(err)
	}
	s.SetCandidates([]llm.Tool{candidate("mail/message:get")})
	if len(s.SelectedNames()) != 0 {
		t.Fatal("revoked candidate retained activation")
	}
	if err := s.Activate([]string{"calendar/events:list"}); err == nil {
		t.Fatal("revoked candidate activated")
	}
	other := WithState(turnContext(), []llm.Tool{candidate("mail/message:get")})
	otherState, _ := FromContext(other)
	if len(otherState.SelectedNames()) != 0 {
		t.Fatal("separate turn state leaked")
	}
}

func TestContextIsolation(t *testing.T) {
	ctx := WithState(turnContext(), []llm.Tool{candidate("calendar/events:list")})
	changed := []context.Context{
		auth.WithCanonicalUserID(ctx, "bob"),
		auth.WithUserInfo(ctx, &auth.UserInfo{Subject: "bob"}),
		auth.WithProvider(ctx, "another-provider"),
		requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{TurnID: "turn-2", ConversationID: "conversation-1"}),
		requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{TurnID: "turn-1", ConversationID: "conversation-2"}),
		requestctx.WithoutConversationTracking(ctx),
	}
	for i, other := range changed {
		if _, ok := FromContext(other); ok {
			t.Fatalf("ownership change %d retained state", i)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok := FromContext(canceled); ok {
		t.Fatal("canceled turn retained state")
	}
}

func TestDiscoveryBoundsAndConcurrentRefresh(t *testing.T) {
	var candidates []llm.Tool
	for i := 0; i < 75; i++ {
		candidates = append(candidates, candidate(fmt.Sprintf("calendar/tool%02d", i)))
	}
	ctx := WithState(turnContext(), candidates)
	s, _ := FromContext(ctx)
	items, more, err := s.List("CALENDAR", 0, 1000)
	if err != nil || !more || len(items) != 50 {
		t.Fatalf("page %d %v %v", len(items), more, err)
	}
	items, more, err = s.List("calendar", 50, 50)
	if err != nil || more || len(items) != 25 {
		t.Fatalf("last page %d %v %v", len(items), more, err)
	}
	if _, _, err = s.List("", -1, 20); err == nil {
		t.Fatal("negative offset accepted")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				s.SetCandidates(candidates)
				_ = s.Activate([]string{"calendar/tool00"})
				s.SelectedNames()
				_, _, _ = s.List("", 0, 20)
			}
		}()
	}
	wg.Wait()
}
