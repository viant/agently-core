package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	manager "github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	workspacewindow "github.com/viant/agently-core/service/ui/window"
	meta "github.com/viant/forge/backend/service/meta"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type countedNativeSource struct {
	source            identity.ResourceSource
	candidates, reads atomic.Int64
}

func (s *countedNativeSource) Candidates(ctx context.Context, uri identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	s.candidates.Add(1)
	return s.source.Candidates(ctx, uri)
}
func (s *countedNativeSource) ReadCandidate(ctx context.Context, uri identity.ResourceURI, c identity.ResourceCandidate) (json.RawMessage, error) {
	s.reads.Add(1)
	return s.source.ReadCandidate(ctx, uri, c)
}
func TestStewardReadonlyNativeWindowNormalPhaseTimings(t *testing.T) {
	root := os.Getenv("STEWARD_NATIVE_YAML_ROOT")
	if root == "" || os.Getenv("STEWARD_NATIVE_YAML_TIMINGS") != "1" {
		t.Skip("opt-in unchanged Steward normal-mode phase timing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	before := stewardAssetDigest(t, root)
	defer func() { require.Equal(t, before, stewardAssetDigest(t, root)) }()
	raw, err := os.ReadFile(filepath.Join(root, "authorization/window-resources.json"))
	require.NoError(t, err)
	var entries []workspacewindow.ResourceBinding
	require.NoError(t, json.Unmarshal(raw, &entries))
	var binding workspacewindow.ResourceBinding
	for _, entry := range entries {
		if entry.WindowKey == "advertiserList" {
			binding = entry
		}
	}
	require.NotEmpty(t, binding.URI)
	uri, err := identity.ParseResourceURI(binding.URI)
	require.NoError(t, err)
	actor := identity.VerifiedActor{Subject: "synthetic-timing", Issuer: "https://synthetic.invalid", TenantID: "fixture", AccountID: "fixture", IdentityRevision: "r1", ValidUntil: time.Now().Add(time.Hour)}
	start := time.Now()
	legacy, err := workspacewindow.LoadWorkspaceWindowWithEnricherAt(ctx, root, binding.WindowKey, &meta.TargetContext{Platform: "web"}, nil)
	require.NoError(t, err)
	require.NotNil(t, legacy)
	t.Logf("legacy single web target=%s", time.Since(start))
	baseline, err := workspacewindow.NewWorkspaceResourceSource(root, []workspacewindow.ResourceBinding{binding}, nil)
	require.NoError(t, err)
	start = time.Now()
	legacyCandidates, err := baseline.Candidates(ctx, uri)
	require.NoError(t, err)
	t.Logf("legacy whole native bundle=%s", time.Since(start))
	local, err := ConfinedWindowBindings(ctx, root, []workspacewindow.ResourceBinding{binding}, extensionPolicy, nil)
	require.NoError(t, err)
	resolver, err := local[0].Resolver(ctx, actor, "resource.get")
	require.NoError(t, err)
	counted := &countedNativeSource{source: resolver.Source}
	originalResolver := local[0].Resolver
	local[0].Resolver = func(ctx context.Context, a identity.VerifiedActor, action string) (*identity.ResourceResolver, error) {
		r, err := originalResolver(ctx, a, action)
		if err == nil {
			r.Source = counted
		}
		return r, err
	}
	if output := os.Getenv("STEWARD_NATIVE_YAML_CPU_PROFILE"); output != "" {
		file, err := os.Create(output)
		require.NoError(t, err)
		require.NoError(t, pprof.StartCPUProfile(file))
		defer file.Close()
	}
	start = time.Now()
	nativeCandidates, err := counted.Candidates(ctx, uri)
	pprof.StopCPUProfile()
	require.NoError(t, err)
	t.Logf("confined whole native bundle=%s", time.Since(start))
	require.Equal(t, legacyCandidates, nativeCandidates)
	actorFn := func(context.Context) (identity.VerifiedActor, error) { return actor, nil }
	verify := func(context.Context, identity.VerifiedActor) error { return nil }
	provider, err := NewLocalProvider(LocalConfig{ProviderIdentity: "internal", Actor: actorFn, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: local, Validators: map[string]LocalResourceValidator{"window": ValidateWindowBundle}})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	require.NoError(t, mgr.RegisterLocal(ctx, "internal", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "internal"}, func(context.Context) (mcpclient.Interface, error) { return NewLocalMCPClient(provider) }))
	gateway := NewGateway(mgr, actorFn, verify, "timing-gateway")
	defer gateway.Close()
	phase := func(name string, run func()) {
		counted.candidates.Store(0)
		counted.reads.Store(0)
		start := time.Now()
		run()
		t.Logf("%s elapsed=%s candidate-materializations=%d read-materializations=%d", name, time.Since(start), counted.candidates.Load(), counted.reads.Load())
	}
	phase("direct local list", func() {
		rows, err := provider.List(ctx, "window", primitive.ListRequest{Namespace: uri.Namespace})
		require.NoError(t, err)
		require.Len(t, rows.Resources, 1)
	})
	var located []LocatedResource
	phase("gateway list", func() {
		located, err = gateway.List(ctx, "window", uri.Namespace)
		require.NoError(t, err)
		require.Len(t, located, 1)
	})
	phase("gateway get", func() {
		result, err := gateway.Get(ctx, located[0].Connection, identity.ResourceRef{URI: uri.String()}, nil)
		require.NoError(t, err)
		require.Equal(t, nativeCandidates[0].ContentFingerprint, result.ResolvedResource.ContentFingerprint)
	})
}

func TestStewardReadonlyPreloadedSameGetTiming(t *testing.T) {
	root := os.Getenv("STEWARD_NATIVE_YAML_ROOT")
	if root == "" || os.Getenv("STEWARD_NATIVE_YAML_PRELOADED_TIMINGS") != "1" {
		t.Skip("opt-in unchanged Steward normal-mode phase timing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	before := stewardAssetDigest(t, root)
	defer func() { require.Equal(t, before, stewardAssetDigest(t, root)) }()
	raw, err := os.ReadFile(filepath.Join(root, "authorization/window-resources.json"))
	require.NoError(t, err)
	var entries []workspacewindow.ResourceBinding
	require.NoError(t, json.Unmarshal(raw, &entries))
	var binding workspacewindow.ResourceBinding
	windowKey := os.Getenv("STEWARD_NATIVE_YAML_TIMING_WINDOW")
	if windowKey == "" {
		windowKey = "advertiserList"
	}
	for _, entry := range entries {
		if entry.WindowKey == windowKey {
			binding = entry
		}
	}
	require.NotEmpty(t, binding.URI)
	uri, err := identity.ParseResourceURI(binding.URI)
	require.NoError(t, err)
	actor := identity.VerifiedActor{Subject: "synthetic-timing", Issuer: "https://synthetic.invalid", TenantID: "fixture", AccountID: "fixture", IdentityRevision: "r1", ValidUntil: time.Now().Add(time.Hour)}
	setupStart := time.Now()
	definitions, err := SnapshotWindowDefinitions(ctx, root, []workspacewindow.ResourceBinding{binding}, nil)
	require.NoError(t, err)
	snapshot, err := NewNativeAssetSnapshot(ctx, root, definitions)
	require.NoError(t, err)
	local, err := snapshot.Bindings("internal", extensionPolicy)
	require.NoError(t, err)
	t.Logf("startup preload elapsed=%s compile-count=%d", time.Since(setupStart), snapshot.CompileCount())
	actorFn := func(context.Context) (identity.VerifiedActor, error) { return actor, nil }
	verify := func(context.Context, identity.VerifiedActor) error { return nil }
	provider, err := NewLocalProvider(LocalConfig{ProviderIdentity: "internal", Actor: actorFn, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: local, Validators: map[string]LocalResourceValidator{"window": ValidateWindowBundle}})
	require.NoError(t, err)
	client, err := NewLocalMCPClient(provider)
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	require.NoError(t, mgr.RegisterLocal(ctx, "internal", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "internal"}, func(context.Context) (mcpclient.Interface, error) { return NewLocalMCPClient(provider) }))
	gateway := NewGateway(mgr, actorFn, verify, "timing-gateway")
	defer gateway.Close()
	warmStart := time.Now()
	namespaces, err := gateway.Discover(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, namespaces)
	t.Logf("discovery/setup warmup elapsed=%s", time.Since(warmStart))
	connection, err := gateway.ConnectionForProvider(ctx, "internal")
	require.NoError(t, err)
	baseline, err := provider.Get(ctx, "window", primitive.GetRequest{URI: uri.String()})
	require.NoError(t, err)
	expected := baseline.Resource.DefinitionBytes
	startCount := snapshot.CompileCount()
	for _, mode := range []string{"direct-get", "sdk-toolcall-get", "gateway-get"} {
		var profileFile *os.File
		if prefix := os.Getenv("STEWARD_NATIVE_YAML_GET_PROFILE_PREFIX"); prefix != "" {
			profileFile, err = os.Create(prefix + "-" + mode + ".cpu")
			require.NoError(t, err)
			require.NoError(t, pprof.StartCPUProfile(profileFile))
		}
		start := time.Now()
		for iteration := 0; iteration < 3; iteration++ {
			switch mode {
			case "direct-get":
				got, err := provider.Get(ctx, "window", primitive.GetRequest{URI: uri.String()})
				require.NoError(t, err)
				require.Equal(t, expected, got.Resource.DefinitionBytes)
			case "sdk-toolcall-get":
				got, err := client.CallTool(ctx, &schema.CallToolRequestParams{Name: "windows/get", Arguments: map[string]interface{}{"uri": uri.String()}})
				require.NoError(t, err)
				require.False(t, got.IsError != nil && *got.IsError)
				encoded, err := json.Marshal(got.StructuredContent)
				require.NoError(t, err)
				var result primitive.GetResult
				require.NoError(t, json.Unmarshal(encoded, &result))
				require.Equal(t, expected, result.Resource.DefinitionBytes)
			case "gateway-get":
				got, err := gateway.Get(ctx, connection, identity.ResourceRef{URI: uri.String()}, nil)
				require.NoError(t, err)
				require.Equal(t, expected, got.Resource.DefinitionBytes)
			}
		}
		elapsed := time.Since(start)
		if profileFile != nil {
			pprof.StopCPUProfile()
			profileFile.Close()
		}
		t.Logf("same payload window=%s %s average=%s bytes=%d steady-native-compiles=%d", windowKey, mode, elapsed/3, len(expected), snapshot.CompileCount()-startCount)
		require.Equal(t, startCount, snapshot.CompileCount())
	}
}

func TestStewardPreloadedTwentyItemWindowListTiming(t *testing.T) {
	root := os.Getenv("STEWARD_NATIVE_YAML_ROOT")
	if root == "" || os.Getenv("STEWARD_NATIVE_YAML_PRELOADED_TIMINGS") != "1" {
		t.Skip("opt-in20cached entries backed by originalStewardwindow payload")
	}
	ctx := context.Background()
	before := stewardAssetDigest(t, root)
	defer func() { require.Equal(t, before, stewardAssetDigest(t, root)) }()
	actualBinding := workspacewindow.ResourceBinding{WindowKey: "advertiserList", URI: "window://steward/advertiserList"}
	definitions, err := SnapshotWindowDefinitions(ctx, root, []workspacewindow.ResourceBinding{actualBinding}, nil)
	require.NoError(t, err)
	actual, err := NewNativeAssetSnapshot(ctx, root, definitions, NativeSnapshotOptions{ImmutableUntilRestart: true})
	require.NoError(t, err)
	uri, _ := identity.ParseResourceURI(actualBinding.URI)
	candidates, err := actual.Candidates(ctx, uri)
	require.NoError(t, err)
	raw, err := actual.ReadCandidate(ctx, uri, candidates[0])
	require.NoError(t, err)
	var bodyLoads atomic.Int64
	entries := make([]NativeSnapshotDefinition, 20)
	for i := range entries {
		entries[i] = NativeSnapshotDefinition{URI: fmt.Sprintf("window://steward/list%d", i), Title: fmt.Sprintf("Cached window %d", i), FormatVersion: 2, File: definitions[0].File, Load: func(context.Context, *ExtensionReader, string) (json.RawMessage, error) {
			bodyLoads.Add(1)
			return raw, nil
		}}
	}
	snapshot, err := NewNativeAssetSnapshot(ctx, root, entries, NativeSnapshotOptions{ImmutableUntilRestart: true})
	require.NoError(t, err)
	var policyCalls atomic.Int64
	bindings, err := snapshot.Bindings("internal", func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) (identity.ResourceRevisionPolicy, error) {
		policyCalls.Add(1)
		return nil, identity.ErrResourceDenied
	})
	require.NoError(t, err)
	actor := identity.VerifiedActor{Subject: "synthetic-list", Issuer: "https://synthetic.invalid", TenantID: "fixture", AccountID: "fixture", IdentityRevision: "r1", ValidUntil: time.Now().Add(time.Hour)}
	actorFn := func(context.Context) (identity.VerifiedActor, error) { return actor, nil }
	verify := func(context.Context, identity.VerifiedActor) error { return nil }
	var groupChecks atomic.Int64
	provider, err := NewLocalProvider(LocalConfig{ProviderIdentity: "internal", Actor: actorFn, Verify: verify, Authorize: func(_ context.Context, _ identity.VerifiedActor, uri identity.ResourceURI, _ string) error {
		if uri.Namespace != "steward" {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: bindings, Validators: map[string]LocalResourceValidator{"window": ValidateWindowBundle}, WindowIndex: snapshot.WindowIndex, WindowListVisibility: func(context.Context, identity.VerifiedActor, LocalResourceBinding) (bool, error) {
		groupChecks.Add(1)
		return true, nil
	}})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	require.NoError(t, mgr.RegisterLocal(ctx, "internal", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "internal"}, func(context.Context) (mcpclient.Interface, error) { return NewLocalMCPClient(provider) }))
	gateway := NewGateway(mgr, actorFn, verify, "list-timing")
	defer gateway.Close()
	_, err = gateway.Discover(ctx)
	require.NoError(t, err)
	startupLoads := bodyLoads.Load()
	startupCompiles := snapshot.CompileCount()
	for _, mode := range []string{"direct-list", "gateway-list"} {
		checks := groupChecks.Load()
		start := time.Now()
		for i := 0; i < 5; i++ {
			if mode == "direct-list" {
				got, err := provider.List(ctx, "window", primitive.ListRequest{Namespace: "steward", Limit: 20})
				require.NoError(t, err)
				require.Len(t, got.Resources, 20)
			} else {
				got, err := gateway.List(ctx, "window", "steward")
				require.NoError(t, err)
				require.Len(t, got, 20)
			}
		}
		t.Logf("20cached original-payload entries %s average=%s body-loads=%d revision-selection-calls=%d native-compiles=%d visibility-callbacks=%d", mode, time.Since(start)/5, bodyLoads.Load()-startupLoads, policyCalls.Load(), snapshot.CompileCount()-startupCompiles, groupChecks.Load()-checks)
	}
	require.Equal(t, startupLoads, bodyLoads.Load())
	require.Equal(t, startupCompiles, snapshot.CompileCount())
	require.Zero(t, policyCalls.Load())
}
