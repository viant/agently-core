package executionprotection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	execconfig "github.com/viant/agently-core/app/executor/config"
	toolprotection "github.com/viant/agently-core/protocol/tool/protection"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

type memoryRepository struct {
	mu        sync.Mutex
	claims    map[string]ClaimRecord
	finishes  map[string]toolprotection.State
	claimErr  error
	finishErr error
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{claims: map[string]ClaimRecord{}, finishes: map[string]toolprotection.State{}}
}

func (r *memoryRepository) Claim(_ context.Context, record ClaimRecord) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimErr != nil {
		return false, r.claimErr
	}
	if _, exists := r.claims[record.ClaimKey]; exists {
		return false, nil
	}
	r.claims[record.ClaimKey] = record
	return true, nil
}

func (r *memoryRepository) Finish(_ context.Context, key string, state toolprotection.State, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finishErr != nil {
		return r.finishErr
	}
	r.finishes[key] = state
	return nil
}

func protectionConfig(semantic *execconfig.ToolExecutionSemanticArguments) execconfig.ToolExecutionProtectionDefaults {
	return execconfig.ToolExecutionProtectionDefaults{Enabled: true, Rules: []execconfig.ToolExecutionProtectionRule{{
		ID: "rule-1", Tool: "service/tool", Mode: execconfig.ToolExecutionProtectionModeAtMostOnce, SemanticArguments: semantic,
	}}}
}

func turnContext(turnID string) context.Context {
	return runtimerequestctx.WithTurnMeta(context.Background(), runtimerequestctx.TurnMeta{ConversationID: "conv", TurnID: turnID})
}

func TestServiceClaimScopeAliasesAndSelection(t *testing.T) {
	repository := newMemoryRepository()
	service, err := New(protectionConfig(&execconfig.ToolExecutionSemanticArguments{
		Fields: []string{"recipient", "subject", "ignored"}, ExcludeFields: []string{"ignored"},
	}), repository)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Claim(turnContext("turn-1"), "service:tool|data.value", map[string]interface{}{
		"recipient": "a@example.com", "subject": "hello", "ignored": "x", "other": "not semantic",
	})
	if err != nil || !first.Acquired {
		t.Fatalf("first Claim() = %#v, %v", first, err)
	}
	duplicate, err := service.Claim(turnContext("turn-1"), "service-tool", map[string]interface{}{
		"subject": "hello", "recipient": "a@example.com", "ignored": "different", "other": "different",
	})
	if err != nil || duplicate.Acquired || duplicate.ClaimKey != first.ClaimKey {
		t.Fatalf("duplicate Claim() = %#v, %v; first %#v", duplicate, err, first)
	}
	newTurn, err := service.Claim(turnContext("turn-2"), "service/tool", map[string]interface{}{"recipient": "a@example.com", "subject": "hello"})
	if err != nil || !newTurn.Acquired || newTurn.ClaimKey == first.ClaimKey {
		t.Fatalf("new turn Claim() = %#v, %v", newTurn, err)
	}
	differentArgs, err := service.Claim(turnContext("turn-1"), "service/tool", map[string]interface{}{"recipient": "b@example.com", "subject": "hello"})
	if err != nil || !differentArgs.Acquired || differentArgs.ClaimKey == first.ClaimKey {
		t.Fatalf("different args Claim() = %#v, %v", differentArgs, err)
	}
}

func TestServiceSelectionDefaultsUseAllFinalArguments(t *testing.T) {
	semantics := []*execconfig.ToolExecutionSemanticArguments{nil, {}, {Fields: []string{"*"}}}
	for index, semantic := range semantics {
		t.Run(fmt.Sprintf("case-%d", index), func(t *testing.T) {
			repository := newMemoryRepository()
			service, err := New(protectionConfig(semantic), repository)
			if err != nil {
				t.Fatal(err)
			}
			claim, err := service.Claim(turnContext("turn"), "service/tool", map[string]interface{}{"b": 2, "a": 1})
			if err != nil || !claim.Acquired {
				t.Fatalf("Claim() = %#v, %v", claim, err)
			}
			canonical, _ := CanonicalJSON(map[string]interface{}{"a": 1, "b": 2})
			sum := sha256.Sum256(canonical)
			record := repository.claims[claim.ClaimKey]
			if record.SemanticHash != hex.EncodeToString(sum[:]) {
				t.Fatalf("semantic hash = %q", record.SemanticHash)
			}
		})
	}
}

func TestServiceFailsClosedBeforeRepository(t *testing.T) {
	repository := newMemoryRepository()
	service, err := New(protectionConfig(nil), repository)
	if err != nil {
		t.Fatal(err)
	}
	contexts := []context.Context{
		context.Background(),
		runtimerequestctx.WithTurnMeta(context.Background(), runtimerequestctx.TurnMeta{}),
		runtimerequestctx.WithTurnMeta(context.Background(), runtimerequestctx.TurnMeta{ConversationID: "conv"}),
	}
	for _, ctx := range contexts {
		if _, err := service.Claim(ctx, "service/tool", map[string]interface{}{"a": 1}); err == nil {
			t.Fatal("Claim() without turn ID error = nil")
		}
	}
	if len(repository.claims) != 0 {
		t.Fatalf("repository claims = %d, want 0", len(repository.claims))
	}
	if _, err := service.Claim(turnContext("turn"), "service/tool", map[string]interface{}{}); err == nil {
		t.Fatal("Claim() with empty selection error = nil")
	}
	if _, err := service.Claim(turnContext("turn"), "service/tool", map[string]interface{}{"bad": func() {}}); err == nil {
		t.Fatal("Claim() with unsupported value error = nil")
	}
}

func TestServiceRepositoryUnavailableAndFinishFailureRemainFailClosed(t *testing.T) {
	service, err := New(protectionConfig(nil), NewDAORepository(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(turnContext("turn"), "service/tool", map[string]interface{}{"a": 1}); err == nil {
		t.Fatal("unavailable repository Claim() error = nil")
	}

	repository := newMemoryRepository()
	repository.finishErr = fmt.Errorf("update unavailable")
	service, _ = New(protectionConfig(nil), repository)
	first, err := service.Claim(turnContext("turn"), "service/tool", map[string]interface{}{"a": 1})
	if err != nil || !first.Acquired {
		t.Fatalf("first Claim() = %#v, %v", first, err)
	}
	service.Finish(context.Background(), first, toolprotection.StateCompleted)
	second, err := service.Claim(turnContext("turn"), "service/tool", map[string]interface{}{"a": 1})
	if err != nil || second.Acquired {
		t.Fatalf("second Claim() = %#v, %v", second, err)
	}
}

func TestServiceDisabledDoesNotUseRepository(t *testing.T) {
	repository := newMemoryRepository()
	repository.claimErr = fmt.Errorf("must not be called")
	service, err := New(execconfig.ToolExecutionProtectionDefaults{
		Enabled: false,
		Rules: []execconfig.ToolExecutionProtectionRule{{
			ID: "invalid-while-disabled", Tool: "*", Mode: "unsupported",
		}},
	}, repository)
	if err != nil {
		t.Fatalf("New() disabled error = %v", err)
	}
	claim, err := service.Claim(context.Background(), "service/tool", map[string]interface{}{"body": "x"})
	if err != nil || claim.Protected {
		t.Fatalf("disabled Claim() = %#v, %v", claim, err)
	}
	if len(repository.claims) != 0 {
		t.Fatalf("disabled repository claims = %d", len(repository.claims))
	}
}
