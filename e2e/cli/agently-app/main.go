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
	"syscall"
	"time"

	"github.com/viant/afs"
	_ "github.com/viant/afs/file"
	"github.com/viant/agently-core/adapter/http/ui"
	"github.com/viant/agently-core/app/executor"
	execconfig "github.com/viant/agently-core/app/executor/config"
	embedprovider "github.com/viant/agently-core/genai/embedder/provider"
	provider "github.com/viant/agently-core/genai/llm/provider"
	iauth "github.com/viant/agently-core/internal/auth"
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
	svcauth "github.com/viant/agently-core/service/auth"
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
	jwtPub := fs.String("jwt-pub", envOr("AGENTLY_JWT_PUB", ""), "RSA public key path for JWT verification")
	jwtPriv := fs.String("jwt-priv", envOr("AGENTLY_JWT_PRIV", ""), "RSA private key path for JWT signing")
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

	schedStore, err := scheduler.NewDatlyStore(ctx, rt.DAO, rt.Data)
	if err != nil {
		log.Fatalf("failed to initialize scheduler store: %v", err)
	}
	schedSvc := scheduler.New(schedStore, rt.Agent,
		scheduler.WithConversationClient(rt.Conversation),
		scheduler.WithTokenProvider(rt.TokenProvider),
	)
	if embedded, ok := client.(*sdk.EmbeddedClient); ok {
		embedded.SetScheduler(schedSvc)
	}
	schedHandler := scheduler.NewHandler(schedSvc)
	metadataHandler := svcworkspace.NewMetadataHandler(rt.Defaults, rt.Store, "agently-core-e2e")

	handlerOpts := []sdk.HandlerOption{
		sdk.WithMetadataHandler(metadataHandler),
		sdk.WithScheduler(schedSvc, schedHandler, &sdk.SchedulerOptions{
			EnableAPI:      true,
			EnableRunNow:   true,
			EnableWatchdog: true,
		}),
	}

	// Configure auth when JWT keys are provided.
	var authCfg *iauth.Config
	var sessions *svcauth.Manager
	var jwtSvc *svcauth.JWTService
	if pubKey := strings.TrimSpace(*jwtPub); pubKey != "" {
		authCfg = &iauth.Config{
			Enabled:    true,
			IpHashKey:  "e2e-test-hash-key",
			CookieName: "agently_session",
			Local:      &iauth.Local{Enabled: true},
			JWT: &iauth.JWT{
				Enabled:       true,
				RSA:           []string{pubKey},
				RSAPrivateKey: strings.TrimSpace(*jwtPriv),
			},
		}
		sessions = svcauth.NewManager(7*24*time.Hour, nil)
		jwtSvc = svcauth.NewJWTService(authCfg.JWT)
		if err := jwtSvc.Init(ctx); err != nil {
			log.Fatalf("failed to initialize JWT service: %v", err)
		}
		handlerOpts = append(handlerOpts, sdk.WithAuth(authCfg, sessions))
		log.Printf("auth enabled: JWT (pub=%s)", pubKey)
	}

	sdkHandler, err := sdk.NewHandlerWithContext(ctx, client, handlerOpts...)
	if err != nil {
		log.Fatalf("failed to initialize sdk handler: %v", err)
	}

	// Apply additional auth protection with JWT verification when configured.
	if authCfg != nil && jwtSvc != nil {
		sdkHandler = svcauth.Protect(authCfg, sessions, svcauth.WithJWTService(jwtSvc))(sdkHandler)
	}
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
	wd, err := os.Getwd()
	if err != nil {
		return ".agently"
	}
	return filepath.Join(wd, ".agently")
}
