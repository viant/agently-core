package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/viant/afs"
	_ "github.com/viant/afs/file"
	"github.com/viant/agently-core/adapter/http/ui"
	"github.com/viant/agently-core/app/executor"
	execconfig "github.com/viant/agently-core/app/executor/config"
	embedprovider "github.com/viant/agently-core/genai/embedder/provider"
	provider "github.com/viant/agently-core/genai/llm/provider"
	embedderfinder "github.com/viant/agently-core/internal/finder/embedder"
	modelfinder "github.com/viant/agently-core/internal/finder/model"
	agentfinder "github.com/viant/agently-core/protocol/agent/finder"
	agentloader "github.com/viant/agently-core/protocol/agent/loader"
	"github.com/viant/agently-core/protocol/tool"
	toolPlan "github.com/viant/agently-core/protocol/tool/service/orchestration/plan"
	toolExec "github.com/viant/agently-core/protocol/tool/service/system/exec"
	toolImage "github.com/viant/agently-core/protocol/tool/service/system/image"
	toolOS "github.com/viant/agently-core/protocol/tool/service/system/os"
	toolPatch "github.com/viant/agently-core/protocol/tool/service/system/patch"
	"github.com/viant/agently-core/sdk"
	"github.com/viant/agently-core/service/scheduler"
	svcworkspace "github.com/viant/agently-core/service/workspace"
	"github.com/viant/agently-core/workspace"
	embedderloader "github.com/viant/agently-core/workspace/loader/embedder"
	wsfs "github.com/viant/agently-core/workspace/loader/fs"
	modelloader "github.com/viant/agently-core/workspace/loader/model"
	"github.com/viant/agently-core/workspace/service/meta"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		serve(os.Args[2:])
		return
	}
	serve(os.Args[1:])
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", envOr("AGENTLY_ADDR", ":8585"), "listen address")
	workspacePath := fs.String("workspace", envOr("AGENTLY_WORKSPACE", defaultWorkspace()), "workspace path")
	uiDist := fs.String("ui-dist", strings.TrimSpace(os.Getenv("AGENTLY_UI_DIST")), "optional local ui dist directory override")
	_ = fs.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	workspace.SetRoot(*workspacePath)
	setBootstrapHook()
	workspace.EnsureDefault(afs.New())

	rt, client, err := newRuntime(ctx)
	if err != nil {
		log.Fatalf("failed to initialize runtime: %v", err)
	}
	defer workspace.SetBootstrapHook(nil)

	schedStore := newMemoryScheduleStore()
	schedSvc := scheduler.New(schedStore, rt.Agent)
	schedHandler := scheduler.NewHandler(schedSvc)
	metadataHandler := svcworkspace.NewMetadataHandler(rt.Defaults, rt.Store, "agently-core-e2e")

	sdkHandler := sdk.NewHandler(client,
		sdk.WithMetadataHandler(metadataHandler),
		sdk.WithScheduler(schedSvc, schedHandler, &sdk.SchedulerOptions{
			EnableAPI:    true,
			EnableRunNow: true,
		}),
	)
	metaRoot := "embed://localhost/"
	metaHandler := ui.NewEmbeddedHandler(metaRoot, nil)

	h := newRouter(sdkHandler, metaHandler, strings.TrimSpace(*uiDist))
	srv := &http.Server{Addr: *addr, Handler: h}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	log.Printf("agently-app serve listening on %s (workspace=%s)", *addr, workspace.Root())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}

func newRuntime(ctx context.Context) (*executor.Runtime, sdk.Client, error) {
	fs := afs.New()
	wsMeta := meta.New(fs, workspace.Root())
	agentLdr := agentloader.New(agentloader.WithMetaService(wsMeta))
	agentFndr := agentfinder.New(agentfinder.WithLoader(agentLdr))
	modelLdr := modelloader.New(wsfs.WithMetaService[provider.Config](wsMeta))
	modelFndr := modelfinder.New(modelfinder.WithConfigLoader(modelLdr))
	embedderLdr := embedderloader.New(wsfs.WithMetaService[embedprovider.Config](wsMeta))
	embedderFndr := embedderfinder.New(embedderfinder.WithConfigLoader(embedderLdr))

	rt, err := executor.NewBuilder().
		WithAgentFinder(agentFndr).
		WithModelFinder(modelFndr).
		WithEmbedderFinder(embedderFndr).
		WithDefaults(&execconfig.Defaults{Model: "openai_gpt4o_mini", Embedder: "openai_text", Agent: "simple"}).
		Build(ctx)
	if err != nil {
		return nil, nil, err
	}
	registerInternalMCPServices(rt)
	client, err := sdk.NewEmbeddedFromRuntime(rt)
	if err != nil {
		return nil, nil, err
	}
	return rt, client, nil
}

func registerInternalMCPServices(rt *executor.Runtime) {
	if rt == nil || rt.Registry == nil {
		return
	}
	tool.AddInternalService(rt.Registry, toolExec.New())
	tool.AddInternalService(rt.Registry, toolOS.New())
	tool.AddInternalService(rt.Registry, toolPatch.New())
	tool.AddInternalService(rt.Registry, toolImage.New())
	tool.AddInternalService(rt.Registry, toolPlan.New())
}

func newRouter(api http.Handler, meta http.Handler, uiDist string) http.Handler {
	localIndex := ""

	var local http.Handler
	if uiDist != "" {
		local = http.FileServer(http.Dir(uiDist))
		localIndex = filepath.Join(uiDist, "index.html")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/v1/api/agently/forge/") {
			http.StripPrefix("/v1/api/agently/forge", meta).ServeHTTP(w, r)
			return
		}
		if path == "/healthz" || strings.HasPrefix(path, "/v1/") {
			api.ServeHTTP(w, r)
			return
		}

		if path == "/" || path == "/ui" || path == "/ui/" || strings.HasPrefix(path, "/conversation/") {
			if localIndex == "" {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, localIndex)
			return
		}

		if local == nil {
			http.NotFound(w, r)
			return
		}
		local.ServeHTTP(w, r)
	})
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func defaultWorkspace() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".agently"
	}
	return filepath.Join(home, ".agently")
}

// memoryScheduleStore is a simple in-memory implementation of scheduler.ScheduleStore for e2e testing.
type memoryScheduleStore struct {
	mu   sync.RWMutex
	data map[string]*scheduler.Schedule
}

func newMemoryScheduleStore() *memoryScheduleStore {
	return &memoryScheduleStore{data: make(map[string]*scheduler.Schedule)}
}

func (m *memoryScheduleStore) Get(id string) (*scheduler.Schedule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.data[id]
	if !ok {
		return nil, nil
	}
	return s, nil
}

func (m *memoryScheduleStore) List() ([]*scheduler.Schedule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*scheduler.Schedule, 0, len(m.data))
	for _, s := range m.data {
		result = append(result, s)
	}
	return result, nil
}

func (m *memoryScheduleStore) Upsert(s *scheduler.Schedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[s.ID] = s
	return nil
}

func (m *memoryScheduleStore) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, id)
	return nil
}

func (m *memoryScheduleStore) ListDue(now time.Time) ([]*scheduler.Schedule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*scheduler.Schedule
	for _, s := range m.data {
		if s.Enabled && s.NextRunAt != nil && !s.NextRunAt.After(now) {
			result = append(result, s)
		}
	}
	return result, nil
}
