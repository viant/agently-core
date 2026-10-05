package sdk

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	nativeconv "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core/modelcall"
	"github.com/viant/agently-core/service/shared/toolexec"
)

type parallelPersistenceRegistry struct {
	entered atomic.Int32
	ready   chan struct{}
}

func (*parallelPersistenceRegistry) Definitions() []llm.ToolDefinition            { return nil }
func (*parallelPersistenceRegistry) MatchDefinition(string) []*llm.ToolDefinition { return nil }
func (*parallelPersistenceRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) {
	return nil, false
}
func (*parallelPersistenceRegistry) MustHaveTools([]string) ([]llm.Tool, error) { return nil, nil }
func (*parallelPersistenceRegistry) SetDebugLogger(io.Writer)                   {}
func (*parallelPersistenceRegistry) Initialize(context.Context)                 {}
func (r *parallelPersistenceRegistry) Execute(ctx context.Context, _ string, _ map[string]interface{}) (string, error) {
	if r.entered.Add(1) == 15 {
		close(r.ready)
	}
	select {
	case <-r.ready:
		return "completed fixture", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Exercise real linked Datly writers with the same 15-way tool fanout and
// transcript reads used by streaming observers. No business tools or raw SQL.
func TestNativeParallelToolPersistenceWithTranscriptReaders(t *testing.T) {
	for _, driver := range []string{"sqlite", "sqlite3"} {
		t.Run(driver, func(t *testing.T) { testNativeParallelToolPersistence(t, driver) })
	}
}

func testNativeParallelToolPersistence(t *testing.T, driver string) {
	root := t.TempDir()
	t.Setenv("AGENTLY_DB_DRIVER", driver)
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", filepath.Join(root, "parallel.db"))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	backend, err := native.New(ctx, native.Options{WorkspaceRoot: root})
	require.NoError(t, err)
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })
	store, err := nativeconv.New(ctx, backend)
	require.NoError(t, err)
	conv := apiconv.NewConversation()
	conv.SetId("parallel-tools")
	conv.SetCreatedByUserID("test-owner")
	require.NoError(t, store.PatchConversations(ctx, conv))
	for round := 0; round < 3; round++ {
		meta := requestctx.TurnMeta{ConversationID: conv.Id, TurnID: fmt.Sprintf("round-%d", round), Assistant: "test-agent"}
		turn := apiconv.NewTurn()
		turn.SetId(meta.TurnID)
		turn.SetConversationID(conv.Id)
		turn.SetStatus("running")
		require.NoError(t, store.PatchTurn(ctx, turn))
		runCtx := requestctx.WithTurnMeta(requestctx.WithConversationID(ctx, conv.Id), meta)
		observed := modelcall.WithRecorderObserver(runCtx, store)
		observer := modelcall.ObserverFromContext(observed)
		observed, err = observer.OnCallStart(observed, modelcall.Info{Provider: "fixture", Model: "fixture", ModelKind: "chat", StartedAt: time.Now(), LLMRequest: &llm.GenerateRequest{Messages: []llm.Message{llm.NewUserMessage("parallel fixture")}}})
		require.NoError(t, err)
		start := make(chan struct{})
		registry := &parallelPersistenceRegistry{ready: make(chan struct{})}
		errs := make(chan error, 64)
		var writers, readers sync.WaitGroup
		readCtx, stopReads := context.WithCancel(ctx)
		for i := 0; i < 2; i++ {
			readers.Add(1)
			go func() {
				defer readers.Done()
				<-start
				for readCtx.Err() == nil {
					_, e := store.GetConversation(readCtx, conv.Id, apiconv.WithIncludeToolCall(true), apiconv.WithIncludeModelCall(true))
					if e != nil && readCtx.Err() == nil {
						select {
						case errs <- fmt.Errorf("transcript read: %w", e):
						default:
						}
						return
					}
				}
			}()
		}
		for i := 0; i < 15; i++ {
			writers.Add(1)
			go func(i int) {
				defer writers.Done()
				<-start
				_, _, e := toolexec.ExecuteToolStep(observed, registry, toolexec.StepInfo{ID: fmt.Sprintf("op-%d-%d", round, i), Name: "fixture/parallel", Args: map[string]interface{}{"index": i, "scope": []int{101}}, ResponseID: fmt.Sprintf("response-%d", round)}, store)
				if e != nil {
					errs <- e
				}
			}(i)
		}
		close(start)
		writers.Wait()
		stopReads()
		readers.Wait()
		err = observer.OnCallEnd(observed, modelcall.Info{Provider: "fixture", Model: "fixture", ModelKind: "chat", CompletedAt: time.Now(), LLMResponse: &llm.GenerateResponse{ResponseID: fmt.Sprintf("response-%d", round), Choices: []llm.Choice{{Message: llm.NewTextMessage(llm.RoleAssistant, "fixture finished"), FinishReason: "stop"}}}})
		require.NoError(t, err)
		close(errs)
		failures := []string{}
		for e := range errs {
			failures = append(failures, e.Error())
		}
		require.Empty(t, failures, "round %d native persistence failures", round)
		require.EqualValues(t, 15, registry.entered.Load(), "all tool bodies must overlap; database coordination must not serialize tool execution")
	}
	fresh, err := store.GetConversation(ctx, conv.Id, apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	calls := 0
	for _, tr := range fresh.GetTranscript() {
		for _, msg := range tr.Message {
			tc := msg.MessageToolCall
			if tc == nil {
				continue
			}
			calls++
			require.NotNil(t, tc.RequestPayloadId)
			require.NotNil(t, tc.ResponsePayloadId)
			require.Equal(t, "completed", tc.Status)
		}
	}
	require.Equal(t, 45, calls)
}
