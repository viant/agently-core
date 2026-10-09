package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/viant/agently-core/genai/llm"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/protocol/tool"
	view "github.com/viant/agently-core/protocol/tool/service/ui/view"
	viewproto "github.com/viant/agently-core/protocol/ui/view"
	"github.com/viant/agently-core/runtime/requestctx"
	ui "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/service/ui/window/registry"
	repo "github.com/viant/agently-core/workspace/repository/forgewindow"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Mirrors the actual private shared AccountUserInfoProvider contract: clearing
// metadata also removes pure decision facts. It never issues permission results.
type sharedWindowMetadataFacts struct{ source *slowWindowIdentity }

func (m sharedWindowMetadataFacts) WithoutMetadataRead(ctx context.Context) context.Context {
	return m.source.WithoutDecision(ctx)
}
func (m sharedWindowMetadataFacts) BeginMetadataRead(ctx context.Context) (context.Context, func() error, error) {
	return m.source.BeginDecision(m.source.WithoutDecision(ctx))
}

func actualWindowFactFixture(t *testing.T) (*slowWindowIdentity, *ui.Service, *windowWireSource, context.Context, func()) {
	t.Helper()
	source, old, bytes, ctx, cleanup := windowDecisionFixture(t, true)
	var actual *resources.WindowCatalog
	old.ConfigureWindowCatalog(func(c ui.WindowDefinitionCatalog) ui.WindowDefinitionCatalog {
		actual = c.(windowWireCatalog).WindowCatalog
		return c
	})
	actual.ContentCurrent = map[string]resources.WindowContentCheck{"internal": func(ctx context.Context, pin identity.ResolvedResource) error {
		if pin.ProviderIdentity != "internal" || pin.URI != "window://platform/overview" {
			return identity.ErrResourceDenied
		}
		uri, err := identity.ParseResourceURI(pin.URI)
		if err != nil {
			return err
		}
		raw, err := bytes.ReadCandidate(ctx, uri, pin.ResourceCandidate)
		if err != nil {
			return err
		}
		if identity.ContentFingerprint(raw) != pin.ContentFingerprint {
			return identity.ErrResourceStale
		}
		return nil
	}}
	composite := &resources.CompositeWindowCatalog{Remote: actual}
	service := ui.NewService(&ui.Config{WindowDefinitions: composite, MetadataScope: sharedWindowMetadataFacts{source}, WindowReadDecisionScope: source.prepared.provider.WindowReadDecisionScope, ResolvedWindowAuthorizer: func(context.Context, identity.ResolvedResource, string, map[string]any) error { return nil }})
	service.ConfigureNamespaceResolver(trustedUINamespace(source.prepared.provider.AuthoritySnapshot, source.prepared.provider.ExecutionContext), source.prepared.provider.ExecutionContext)
	return source, service, bytes, ctx, cleanup
}
func TestWindowPureFactsActualCompositeSharedMetadataProvider(t *testing.T) {
	source, service, _, ctx, cleanup := actualWindowFactFixture(t)
	defer cleanup()
	source.calls.Store(0)
	started := time.Now()
	if err := runWindowDecisionOpen(t, service, ctx, nil); err != nil {
		t.Fatal(err)
	}
	t.Logf("actualWindowCatalog+Composite sharedMetadataAndDecisionHTTPCalls=%d elapsed=%s", source.calls.Load(), time.Since(started))
	if source.calls.Load() > 14 {
		t.Fatalf("pure window facts were discarded: %d source HTTP lookups", source.calls.Load())
	}
}

type declaredCompositeWindowNames struct {
	*resources.CompositeWindowCatalog
	id string
}

func (c declaredCompositeWindowNames) ConfiguredWindowIDs() []string { return []string{c.id} }

func TestWindowPureFactsActualUIViewOpenAndInstanceRegistry(t *testing.T) {
	testWindowActualUIView(t, false, "")
}
func TestWindowPureFactsActualBuilderToolRegistryOpen(t *testing.T) {
	testWindowActualUIView(t, true, "")
}

type noWindowModelFinder struct{}

func (noWindowModelFinder) Find(context.Context, string) (llm.Model, error) {
	return nil, fmt.Errorf("LLM calls forbidden")
}

type noWindowAgentFinder struct{}

func (noWindowAgentFinder) Find(context.Context, string) (*agentmodel.Agent, error) {
	return nil, fmt.Errorf("agent calls forbidden")
}
func testWindowActualUIView(t *testing.T, builderPath bool, fault string) {
	source, service, authoredBytes, ctx, cleanup := actualWindowFactFixture(t)
	defer cleanup()
	var phases atomic.Int64
	if fault != "" {
		original := source.prepared.provider.WindowReadDecisionScope
		service.ConfigureWindowReadDecisionScope(func(ctx context.Context) (context.Context, func() error, error) {
			scoped, finish, err := original(ctx)
			phase := phases.Add(1)
			if err != nil {
				return scoped, finish, err
			}
			return scoped, func() error {
				if phase == 4 || fault == "drift-before-queue" && phase == 2 || fault == "drift-after-ack" && phase == 3 {
					switch fault {
					case "revoked-at-final":
						source.revoked.Store(true)
					case "expired-at-final":
						time.Sleep(120 * time.Millisecond)
					case "drift-at-final", "drift-before-queue", "drift-after-ack":
						authoredBytes.mu.Lock()
						authoredBytes.raw = append(authoredBytes.raw, byte(32))
						authoredBytes.mu.Unlock()
					}
				}
				return finish()
			}, nil
		})
	}
	var actual *resources.CompositeWindowCatalog
	service.ConfigureWindowCatalog(func(c ui.WindowDefinitionCatalog) ui.WindowDefinitionCatalog {
		actual = c.(*resources.CompositeWindowCatalog)
		return c
	})
	page, err := actual.List(ctx, &ui.WindowDefinitionListInput{})
	if err != nil || len(page.Windows) != 1 {
		t.Fatal("actual native window locator missing")
	}
	locator := page.Windows[0].WindowID
	root := t.TempDir()
	store := fsstore.New(root)
	repository := repo.NewWithStore(store)
	if err := repository.Save(ctx, "overview", &viewproto.Spec{ID: "overview", WindowKey: locator}); err != nil {
		t.Fatal(err)
	}
	service.ConfigureWindowCatalog(func(c ui.WindowDefinitionCatalog) ui.WindowDefinitionCatalog {
		return declaredCompositeWindowNames{CompositeWindowCatalog: c.(*resources.CompositeWindowCatalog), id: locator}
	})
	viewService := view.New(repository, service)
	var runtime *Runtime
	if builderPath {
		t.Setenv("AGENTLY_DB_DRIVER", "sqlite")
		t.Setenv("AGENTLY_DB_DSN", "")
		t.Setenv("AGENTLY_DB_PATH", filepath.Join(root, "owned.db"))
		t.Setenv("AGENTLY_RUNTIME_ROOT", root)
		var buildErr error
		runtime, buildErr = NewBuilder().WithUIBridge(service).WithStore(store).WithKnowledgeStore(fsstore.NewKnowledgeStore(root)).WithStateStore(fsstore.NewStateStore(root)).WithModelFinder(noWindowModelFinder{}).WithAgentFinder(noWindowAgentFinder{}).WithSkipRegistryInitialize(true).Build(ctx)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		defer runtime.Close(context.Background())
		if err := tool.AddInternalService(runtime.Registry, viewService); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		service.Hub().ServeWS(w, r.WithContext(context.WithValue(r.Context(), windowActorKey{}, ctx.Value(windowActorKey{}))))
	}))
	defer server.Close()
	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	if err := socket.WriteJSON(map[string]any{"type": "ui.hello", "clientId": "owned-view-client"}); err != nil {
		t.Fatal(err)
	}
	if err := socket.WriteJSON(map[string]any{"type": "ui.snapshot", "clientId": "owned-view-client", "data": map[string]any{"conversationId": "owned-view-conversation", "windows": []any{}}}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); len(service.Hub().SnapshotEntries()) == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	ctx = requestctx.WithConversationID(ctx, "owned-view-conversation")
	commandReceived := make(chan int64, 1)
	go func() {
		var command struct {
			ID string `json:"id"`
		}
		socket.SetReadDeadline(time.Now().Add(20 * time.Second))
		if socket.ReadJSON(&command) != nil {
			commandReceived <- -1
			return
		}
		commandReceived <- source.calls.Load()
		socket.WriteJSON(map[string]any{"id": command.ID, "ok": true, "result": map[string]any{"windowId": "owned-view-window"}})
	}()
	var traceMu sync.Mutex
	trace := map[string]int{}
	source.httpTrace.Store(&windowHTTPTrace{call: func(context.Context) {
		pcs := make([]uintptr, 48)
		n := goruntime.Callers(2, pcs)
		frames := goruntime.CallersFrames(pcs[:n])
		label := "other"
		for {
			f, more := frames.Next()
			name := f.Function
			if strings.Contains(name, "slowWindowIdentity).BeginDecision.func") {
				label = "phase-finish"
				break
			}
			if strings.Contains(name, "slowWindowIdentity).BeginDecision") {
				label = "phase-begin"
				break
			}
			if strings.Contains(name, "trustedUINamespace") {
				label = "namespace"
				break
			}
			if !more {
				break
			}
		}
		traceMu.Lock()
		trace[label]++
		traceMu.Unlock()
	}})
	if builderPath {
		for _, name := range []string{"ui/view:list", "ui/view:get"} {
			source.calls.Store(0)
			t0 := time.Now()
			args := map[string]any{}
			if name == "ui/view:get" {
				args["id"] = "overview"
			}
			_, e := runtime.Registry.Execute(ctx, name, args)
			if e != nil {
				t.Fatal(e)
			}
			t.Logf("actualBuilderSDK %s sourceHTTP=%d elapsed=%s", name, source.calls.Load(), time.Since(t0))
		}
	}
	traceMu.Lock()
	trace = map[string]int{}
	traceMu.Unlock()
	source.calls.Store(0)
	if fault == "expired-at-final" {
		source.lease.Store(int64(80 * time.Millisecond))
	}
	started := time.Now()
	execute, _ := viewService.Method("open")
	var output view.OpenOutput
	if builderPath {
		var result string
		result, err = runtime.Registry.Execute(ctx, "ui/view:open", map[string]any{"id": "overview", "clientId": "owned-view-client", "timeoutMs": 20000})
		if err == nil {
			err = json.Unmarshal([]byte(result), &output)
		}
	} else {
		err = execute(ctx, &view.OpenInput{ID: "overview", ClientID: "owned-view-client", TimeoutMs: 20000}, &output)
	}
	var beforeACK int64
	if err != nil {
		socket.Close()
	}
	beforeACK = <-commandReceived
	traceMu.Lock()
	t.Logf("sourceHTTPbreakdown=%v", trace)
	traceMu.Unlock()
	t.Logf("actualUIView+Registry totalHTTP=%d beforeACK=%d postACK=%d elapsed=%s", source.calls.Load(), beforeACK, source.calls.Load()-beforeACK, time.Since(started))
	if fault != "" {
		if err == nil || output.OK {
			t.Fatal("failed final registry admission released output")
		}
		source.revoked.Store(false)
		source.lease.Store(int64(time.Minute))
		if fault == "drift-before-queue" && beforeACK != -1 {
			t.Fatal("changed original content queued command")
		}
		// Post-operation raw event inspection only: this cannot hide stale/expired
		// writes behind the normal read-admission filter.
		service.ConfigureWindowCatalog(func(c ui.WindowDefinitionCatalog) ui.WindowDefinitionCatalog {
			return onlyWindowCRUDForStateInspection{c}
		})
		events := registry.New(service).ListConversationEvents("owned-view-conversation")
		if len(events) != 0 {
			t.Fatal("event write preceded fresh final admission")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if source.calls.Load() > 20 {
		t.Fatalf("pure window open facts were discarded outside operation phase: %d", source.calls.Load())
	}
}

func TestWindowRegistryFinalReadRejectsBeforeEventMutation(t *testing.T) {
	for _, fault := range []string{"revoked-at-final", "expired-at-final", "drift-at-final", "drift-before-queue", "drift-after-ack"} {
		t.Run(fault, func(t *testing.T) { testWindowActualUIView(t, false, fault) })
	}
}

type onlyWindowCRUDForStateInspection struct{ ui.WindowDefinitionCatalog }
