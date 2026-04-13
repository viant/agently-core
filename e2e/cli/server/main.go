package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/viant/afs"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/app/executor/config"
	cancels "github.com/viant/agently-core/app/store/conversation/cancel"
	"github.com/viant/agently-core/genai/llm/provider"
	modelfinder "github.com/viant/agently-core/internal/finder/model"
	"github.com/viant/agently-core/internal/sdkbackend"
	agentfinder "github.com/viant/agently-core/protocol/agent/finder"
	agentloader "github.com/viant/agently-core/protocol/agent/loader"
	"github.com/viant/agently-core/sdk"
	wsfs "github.com/viant/agently-core/workspace/loader/fs"
	modelloader "github.com/viant/agently-core/workspace/loader/model"
	meta "github.com/viant/agently-core/workspace/service/meta"
)

func main() {
	port := flag.Int("port", 8090, "HTTP server port")
	testdata := flag.String("testdata", "", "path to testdata directory")
	flag.Parse()

	if *testdata == "" {
		exe, _ := os.Executable()
		*testdata = filepath.Join(filepath.Dir(exe), "..", "..", "query", "testdata")
	}

	absTestdata, err := filepath.Abs(*testdata)
	if err != nil {
		log.Fatalf("resolve testdata: %v", err)
	}

	ctx := context.Background()

	tmp, err := os.MkdirTemp("", "agently-e2e-*")
	if err != nil {
		log.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmp)

	os.Setenv("AGENTLY_WORKSPACE", tmp)
	os.Setenv("AGENTLY_DB_DRIVER", "")
	os.Setenv("AGENTLY_DB_DSN", "")
	if err := copyTestdataMCP(absTestdata, tmp); err != nil {
		log.Fatalf("prepare mcp configs: %v", err)
	}

	fs := afs.New()
	wsMeta := meta.New(fs, absTestdata)
	agentLdr := agentloader.New(agentloader.WithMetaService(wsMeta))
	agentFndr := agentfinder.New(agentfinder.WithLoader(agentLdr))

	modelLdr := modelloader.New(wsfs.WithMetaService[provider.Config](wsMeta))
	modelFndr := modelfinder.New(modelfinder.WithConfigLoader(modelLdr))

	rt, err := executor.NewBuilder().
		WithAgentFinder(agentFndr).
		WithModelFinder(modelFndr).
		WithCancelRegistry(cancels.NewMemory()).
		WithDefaults(&config.Defaults{
			Model:                 "openai_gpt4o_mini",
			ElicitationTimeoutSec: 30,
			ToolApproval: config.ToolApprovalDefaults{
				Mode: "best_path",
			},
		}).
		Build(ctx)
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}

	client, err := sdkbackend.FromRuntime(rt)
	if err != nil {
		log.Fatalf("create backend client: %v", err)
	}

	handler := sdk.NewHandler(client)
	addr := fmt.Sprintf(":%d", *port)
	srv := &http.Server{Addr: addr, Handler: handler}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		srv.Shutdown(context.Background())
	}()

	log.Printf("e2e server listening on %s (testdata=%s)", addr, absTestdata)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func copyTestdataMCP(testdataRoot, workspaceRoot string) error {
	srcDir := filepath.Join(testdataRoot, "mcp")
	info, err := os.Stat(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}
	dstDir := filepath.Join(workspaceRoot, "mcp")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		ext := strings.ToLower(path.Ext(name))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}
		srcPath := filepath.Join(srcDir, name)
		dstPath := filepath.Join(dstDir, name)
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
