package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	"github.com/viant/agently-core/protocol/primitive/observe"
	identity "github.com/viant/agently-core/protocol/resource"
	producer "github.com/viant/agently-core/service/resource/provider"
	"github.com/viant/forge/backend/types"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/logger"
	"github.com/viant/mcp-protocol/schema"
	protocol "github.com/viant/mcp-protocol/server"
	mcpserver "github.com/viant/mcp/server"
)

var primitiveBenchKinds = []string{"window", "report", "datasource", "intent", "skill", "template", "prompt", "eval", "menu", "toolfeed"}

type primitiveBenchActorKey struct{}
type primitiveBenchPolicy struct{ actor identity.VerifiedActor }

func (p primitiveBenchPolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if len(c) != 1 {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "synthetic-benchmark-owner", ValidUntil: p.actor.ValidUntil}, nil
}

type primitiveBenchJob struct {
	server, namespace, kind string
	uris                    []string
}
type primitiveBenchCounts struct {
	mu                          sync.Mutex
	requests                    map[string]int
	requestBytes, responseBytes int64
	events                      map[string]int
}

func (c *primitiveBenchCounts) Observe(e observe.Event) {
	c.mu.Lock()
	c.events[string(e.Phase)+":"+string(e.Outcome)+":"+string(e.Cache)]++
	c.mu.Unlock()
}
func (c *primitiveBenchCounts) reset() {
	c.mu.Lock()
	c.requests = map[string]int{}
	c.events = map[string]int{}
	c.requestBytes, c.responseBytes = 0, 0
	c.mu.Unlock()
}
func (c *primitiveBenchCounts) snapshot() (map[string]int, map[string]int, int64, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	requests, events := map[string]int{}, map[string]int{}
	for k, v := range c.requests {
		requests[k] = v
	}
	for k, v := range c.events {
		events[k] = v
	}
	return requests, events, c.requestBytes, c.responseBytes
}

type primitiveBenchResponse struct {
	http.ResponseWriter
	counts *primitiveBenchCounts
}

func (w *primitiveBenchResponse) Write(p []byte) (int, error) {
	n, e := w.ResponseWriter.Write(p)
	w.counts.mu.Lock()
	w.counts.responseBytes += int64(n)
	w.counts.mu.Unlock()
	return n, e
}
func (w *primitiveBenchResponse) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type primitiveBenchFixture struct {
	gateway         *Gateway
	manager         *manager.Manager
	servers         []*httptest.Server
	jobs            []primitiveBenchJob
	counts          *primitiveBenchCounts
	ctx             context.Context
	definitionBytes int64
}

func (f *primitiveBenchFixture) close() {
	f.gateway.Close()
	f.manager.CloseConversation("")
	for _, s := range f.servers {
		s.Close()
	}
}
func primitiveBenchJSON(version int64, raw json.RawMessage) error {
	if version != 1 || !json.Valid(raw) {
		return identity.ErrResource
	}
	return nil
}
func primitiveBenchDefinition(t *testing.T, kind, id string) (json.RawMessage, int64, LocalResourceValidator) {
	t.Helper()
	var value any
	version := int64(1)
	validator := LocalResourceValidator(primitiveBenchJSON)
	switch kind {
	case "window":
		window := &types.Window{View: types.View{Content: &types.Container{ID: id}}}
		variant := types.WindowResourceVariant{Window: window, DataSources: map[string]json.RawMessage{}}
		fingerprint, err := types.WindowVariantFingerprint(variant)
		if err != nil {
			t.Fatal(err)
		}
		value = types.WindowResourceEnvelope{SchemaVersion: 2, Format: types.WindowBundleFormat, Targets: []types.WindowTargetBinding{{Variant: fingerprint}}, Variants: map[string]types.WindowResourceVariant{fingerprint: variant}}
		version, validator = 2, ValidateWindowBundle
	case "report":
		value = map[string]any{"schemaVersion": 1, "reportDocument": map[string]any{"title": id, "description": strings.Repeat("synthetic report ", 128)}}
		validator = ValidateReportEnvelope
	case "datasource":
		value = map[string]any{"schemaVersion": 1, "dataSource": map[string]any{"id": id, "selectors": map[string]any{"data": "rows"}, "backend": map[string]any{"kind": "mcp_tool", "service": "synthetic-provider", "method": "query"}}}
		validator = ValidateDatasourceDefinition
	case "intent":
		value = map[string]any{"id": id, "name": id, "instructions": "Synthetic intent instructions"}
		validator = ValidateIntentDefinition
	case "skill":
		value = NativeSkillDefinition{Document: []byte("---\nname: " + id + "\ndescription: Synthetic benchmark skill\n---\n# Synthetic\nRead synthetic metadata.\n")}
		validator = ValidateNativeSkillDefinition
	case "template":
		properties := map[string]any{}
		for i := 0; i < 200; i++ {
			properties[fmt.Sprintf("field%03d", i)] = map[string]any{"type": "string", "description": "Synthetic field"}
		}
		value = map[string]any{"id": id, "name": id, "instructions": strings.Repeat("Synthetic template instructions. ", 256), "schema": map[string]any{"type": "object", "properties": properties}}
		validator = ValidateTemplateDefinition
	case "prompt":
		value = map[string]any{"schemaVersion": 1, "id": id, "text": "Synthetic prompt", "kind": "prompt"}
	case "eval":
		value = map[string]any{"schemaVersion": 1, "id": id, "kind": "eval", "cases": []any{map[string]any{"input": "synthetic", "expected": "synthetic"}}}
	case "menu":
		value = map[string]any{"schemaVersion": 1, "applications": []any{map[string]any{"id": id, "title": id, "menus": []any{map[string]any{"id": "open", "action": map[string]any{"type": "window", "windowKey": "synthetic-window"}}}}}}
		validator = producer.ValidateMenu
	case "toolfeed":
		value = primitive.ToolfeedDefinition{ID: id, Match: primitive.ToolfeedMatch{Service: "synthetic-provider", Method: "query"}, UI: json.RawMessage(`{"title":"Synthetic feed"}`)}
		validator = ValidateToolfeedDefinition
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = validator(version, raw); err != nil {
		t.Fatalf("%s definition invalid: %v", kind, err)
	}
	return raw, version, validator
}

type primitiveBenchHandler struct {
	*protocol.DefaultHandler
	owner    string
	eventBus *primitiveBenchEventBus
}

func (h *primitiveBenchHandler) Initialize(ctx context.Context, in *schema.InitializeRequestParams, out *schema.InitializeResult) {
	h.DefaultHandler.Initialize(ctx, in, out)
	h.metadata(&out.Capabilities)
}
func (h *primitiveBenchHandler) Discover(ctx context.Context, out *schema.DiscoverResult) {
	h.DefaultHandler.Discover(ctx, out)
	h.metadata(&out.Capabilities)
}
func (h *primitiveBenchHandler) metadata(out *schema.ServerCapabilities) {
	if out.Extensions == nil {
		out.Extensions = map[string]map[string]interface{}{}
	}
	out.Extensions[primitive.AuthoringExtension] = map[string]interface{}{"version": 1, "providerIdentity": h.owner, "transport": "tools/call"}
	if h.eventBus != nil {
		yes := true
		out.Resources = &schema.ServerCapabilitiesResources{ListChanged: &yes, Subscribe: &yes}
	}
}
func primitiveBenchSDKHandler(t *testing.T, provider *producer.LocalProvider, name string, protect func(http.Handler) http.Handler, eventBuses ...*primitiveBenchEventBus) (http.Handler, error) {
	t.Helper()
	var normal schema.ToolInputSchema
	if err := normal.Load(struct {
		Query          string                      `json:"query"`
		Range          struct{ Start, End string } `json:"range"`
		GroupBy        []string                    `json:"groupBy"`
		Limit          int                         `json:"limit"`
		Offset         int                         `json:"offset"`
		Filters        map[string]string           `json:"filters"`
		IncludeMetrics bool                        `json:"includeMetrics"`
		Fields         []string                    `json:"fields"`
	}{}); err != nil {
		return nil, err
	}
	normalRaw, _ := json.Marshal(normal)
	var normalOutput schema.ToolOutputSchema
	if err := json.Unmarshal(normalRaw, &normalOutput); err != nil {
		return nil, err
	}
	props := map[string]interface{}{}
	for field := 0; field < 200; field++ {
		props[fmt.Sprintf("field_%03d", field)] = map[string]interface{}{"type": "number", "description": fmt.Sprintf("Analytic dimension or metric %03d with a stable schema field.", field)}
	}
	raw, _ := json.Marshal(map[string]interface{}{"type": "object", "properties": props})
	var wide schema.ToolInputSchema
	var wideOutput schema.ToolOutputSchema
	if err := json.Unmarshal(raw, &wide); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &wideOutput); err != nil {
		return nil, err
	}
	server, err := mcpserver.New(mcpserver.WithImplementation(schema.Implementation{Name: name, Version: "1"}), mcpserver.WithStreamableURI("/mcp"), mcpserver.WithRootRedirect(false), mcpserver.WithNewHandler(func(ctx context.Context, n transport.Notifier, l logger.Logger, c mcpclient.Operations) (protocol.Handler, error) {
		handler := &primitiveBenchHandler{DefaultHandler: protocol.NewDefaultHandler(n, l, c), owner: name}
		if len(eventBuses) > 0 && eventBuses[0] != nil {
			handler.eventBus = eventBuses[0]
			handler.eventBus.register(n, handler.DefaultHandler)
			handler.Methods.Put("resources/subscribe", true)
			handler.Methods.Put("resources/unsubscribe", true)
			if err := handler.RegisterResource(schema.Resource{Name: "Synthetic catalog", Uri: "benchmark://catalog/manifest"}, func(context.Context, *schema.ReadResourceRequest) (*schema.ReadResourceResult, *jsonrpc.Error) {
				return &schema.ReadResourceResult{Contents: []schema.ReadResourceResultContentsElem{{Uri: "benchmark://catalog/manifest", Text: `{"synthetic":true}`}}}, nil
			}); err != nil {
				return nil, err
			}
		}
		for _, method := range provider.InstalledTools() {
			method := method
			var input schema.ToolInputSchema
			var sample any = primitive.ListRequest{}
			if method == "namespaces/list" {
				sample = primitive.NamespaceListRequest{}
			} else if method == "namespaces/get" {
				sample = primitive.NamespaceGetRequest{}
			} else if strings.HasSuffix(method, "/get") {
				sample = primitive.GetRequest{}
			}
			if err := input.Load(sample); err != nil {
				return nil, err
			}
			handler.RegisterTool(&protocol.ToolEntry{Metadata: schema.Tool{Name: method, InputSchema: input, Meta: map[string]interface{}{primitive.AuthoringExtension: map[string]interface{}{"version": 1, "providerIdentity": name, "transport": "tools/call"}}}, Handler: func(callCtx context.Context, request *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
				raw, err := json.Marshal(request.Params.Arguments)
				if err != nil {
					return nil, jsonrpc.NewInvalidParamsError("invalid request", nil)
				}
				value, err := provider.Call(callCtx, method, raw)
				if err != nil {
					return nil, jsonrpc.NewInternalError(err.Error(), nil)
				}
				return &schema.CallToolResult{StructuredContent: value}, nil
			}})
		}
		extra := 100 - len(provider.InstalledTools())
		for i := 0; i < extra; i++ {
			input, output := normal, &normalOutput
			if i < 20 {
				input, output = wide, &wideOutput
			}
			description := "Read account analytics with date range,filters,grouped metrics and bounded rows."
			handler.RegisterTool(&protocol.ToolEntry{Metadata: schema.Tool{Name: fmt.Sprintf("%s_ordinary_%03d", name, i), Description: &description, InputSchema: input, OutputSchema: output}, Handler: func(context.Context, *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
				return &schema.CallToolResult{StructuredContent: map[string]interface{}{"rows": []interface{}{}}}, nil
			}})
		}
		return handler, nil
	}))
	if err != nil {
		return nil, err
	}
	server.UseStreamableHTTP(true)
	return protect(server.HTTP(context.Background(), "").Handler), nil
}

func newPrimitiveBenchFixture(t *testing.T, delay time.Duration) *primitiveBenchFixture {
	t.Helper()
	actor := identity.VerifiedActor{Subject: "synthetic-user", Issuer: "synthetic-issuer", TenantID: "synthetic-tenant", AccountID: "synthetic-account", IdentityRevision: "fixture-v1", ValidUntil: time.Now().Add(time.Hour)}
	counts := &primitiveBenchCounts{}
	counts.reset()
	options := fixtureOptions{}
	f := &primitiveBenchFixture{counts: counts}
	for server := 0; server < 5; server++ {
		name := fmt.Sprintf("primitive-bench-%d", server)
		bindings := []producer.LocalResourceBinding{}
		validators := map[string]producer.LocalResourceValidator{}
		for ns := 0; ns < 3; ns++ {
			namespace := fmt.Sprintf("provider%d-ns%d", server, ns)
			for offset := 0; offset < 2; offset++ {
				kind := primitiveBenchKinds[server*2+offset]
				job := primitiveBenchJob{server: name, namespace: namespace, kind: kind}
				for item := 0; item < 15; item++ {
					id := fmt.Sprintf("%s-%02d", kind, item)
					uri := identity.ResourceURI{Kind: kind, Namespace: namespace, Name: id}
					raw, version, validator := primitiveBenchDefinition(t, kind, id)
					f.definitionBytes += int64(len(raw))
					validators[kind] = validator
					source := &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return raw, nil }}
					bindings = append(bindings, producer.LocalResourceBinding{URI: uri.String(), Title: id, Description: "Synthetic benchmark metadata", FormatVersion: version, Resolver: func(_ context.Context, a identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
						return &identity.ResourceResolver{ProviderIdentity: name, Source: source, Policy: primitiveBenchPolicy{actor: a}}, nil
					}})
					job.uris = append(job.uris, uri.String())
				}
				f.jobs = append(f.jobs, job)
			}
		}
		provider, err := producer.NewLocalProvider(producer.LocalConfig{ProviderIdentity: name, Bindings: bindings, Validators: validators, Actor: func(ctx context.Context) (identity.VerifiedActor, error) {
			a, ok := ctx.Value(primitiveBenchActorKey{}).(identity.VerifiedActor)
			if !ok {
				return a, identity.ErrResourceDenied
			}
			return a, nil
		}, Verify: func(context.Context, identity.VerifiedActor) error { return nil }, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, MenuActionVisibility: func(context.Context, identity.VerifiedActor, identity.ResourceURI, json.RawMessage) (bool, error) {
			return true, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		handler, err := primitiveBenchSDKHandler(t, provider, name, func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
					r.Body = io.NopCloser(bytes.NewReader(raw))
					var message struct {
						Method string `json:"method"`
						Params struct {
							Name string `json:"name"`
						} `json:"params"`
					}
					_ = json.Unmarshal(raw, &message)
					operation := message.Method
					if operation == "tools/call" {
						operation = message.Params.Name
					}
					counts.mu.Lock()
					counts.requests[operation]++
					counts.requestBytes += int64(len(raw))
					counts.mu.Unlock()
					if delay > 0 {
						time.Sleep(delay)
					}
				}
				ctx := observe.WithObserver(context.WithValue(r.Context(), primitiveBenchActorKey{}, actor), counts)
				next.ServeHTTP(&primitiveBenchResponse{ResponseWriter: w, counts: counts}, r.WithContext(ctx))
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		endpoint := httptest.NewServer(handler)
		f.servers = append(f.servers, endpoint)
		options[name] = &cfg.MCPClient{PrimitiveProviderIdentity: name, ToolsListVisibility: "public", ClientOptions: &mcp.ClientOptions{Name: name, Version: "1", ProtocolVersion: schema.LatestProtocolVersion, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: endpoint.URL + "/mcp"}}}}
	}
	mgr, err := manager.New(options)
	if err != nil {
		t.Fatal(err)
	}
	f.manager = mgr
	f.gateway = NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, func(context.Context, identity.VerifiedActor) error { return nil }, "benchmark-consumer")
	f.ctx = observe.WithObserver(context.Background(), counts)
	return f
}
func primitiveBenchWorkers(ctx context.Context, workers, count int, run func(int) error) error {
	if workers == 1 {
		for i := 0; i < count; i++ {
			if err := run(i); err != nil {
				return err
			}
		}
		return nil
	}
	queue := make(chan int)
	var wait sync.WaitGroup
	var once sync.Once
	var failure error
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range queue {
				if err := run(index); err != nil {
					once.Do(func() { failure = err })
				}
			}
		}()
	}
	for i := 0; i < count; i++ {
		select {
		case queue <- i:
		case <-ctx.Done():
			close(queue)
			wait.Wait()
			return ctx.Err()
		}
	}
	close(queue)
	wait.Wait()
	return failure
}

type primitiveBenchResult struct {
	Stage                                        string `json:"stage"`
	Repeat, Workers, Page, DelayMS               int
	ElapsedMS                                    float64
	Entries, Pages                               int
	RequestBytes, ResponseBytes, DefinitionBytes int64
	Requests, Observations                       map[string]int
}

func (f *primitiveBenchFixture) measure(t *testing.T, stage string, repeat, workers, page int, delay time.Duration, run func() (int, int, error)) {
	t.Helper()
	f.counts.reset()
	started := time.Now()
	entries, pages, err := run()
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("%s: %v", stage, err)
	}
	expected := 450
	if strings.Contains(stage, "namespace") || stage == "cold.discovery" {
		expected = 15
	}
	if stage == "rtt.sample-fresh-get" || stage == "rtt.sample-fresh-get-repeat" {
		expected = 30
	}
	if entries != expected {
		t.Fatalf("%s returned%d resources,expected%d", stage, entries, expected)
	}
	requests, events, input, output := f.counts.snapshot()
	result := primitiveBenchResult{Stage: stage, Repeat: repeat, Workers: workers, Page: page, DelayMS: int(delay / time.Millisecond), ElapsedMS: float64(elapsed) / float64(time.Millisecond), Entries: entries, Pages: pages, RequestBytes: input, ResponseBytes: output, DefinitionBytes: f.definitionBytes, Requests: requests, Observations: events}
	raw, _ := json.Marshal(result)
	t.Log("PRIMITIVE_BENCH " + string(raw))
}
func (f *primitiveBenchFixture) discoverNamespaces(workers int) (int, int, error) {
	actor, err := f.gateway.actor(f.ctx)
	if err != nil {
		return 0, 0, err
	}
	names, err := f.manager.Names(f.ctx)
	if err != nil {
		return 0, 0, err
	}
	var total int
	var lock sync.Mutex
	err = primitiveBenchWorkers(f.ctx, workers, len(names), func(index int) error {
		caps, err := f.gateway.discover(f.ctx, actor, names[index])
		if err != nil {
			return err
		}
		lock.Lock()
		total += len(caps.namespaces)
		lock.Unlock()
		return nil
	})
	return total, len(names), err
}
func (f *primitiveBenchFixture) namespacePages(limit int) (int, int, error) {
	cursor := ""
	entries, pages := 0, 0
	for {
		page, err := f.gateway.NamespacePage(f.ctx, CatalogQuery{Limit: limit, Cursor: cursor})
		if err != nil {
			return 0, 0, err
		}
		entries += len(page.Items)
		pages++
		if page.Complete {
			return entries, pages, nil
		}
		cursor = page.NextCursor
	}
}
func (f *primitiveBenchFixture) genericLists(workers, limit int) (int, int, error) {
	var total, pages int
	var lock sync.Mutex
	err := primitiveBenchWorkers(f.ctx, workers, len(f.jobs), func(index int) error {
		job := f.jobs[index]
		cursor := ""
		for {
			page, err := ListPage[json.RawMessage](f.ctx, f.gateway, job.kind, CatalogQuery{Namespace: job.namespace, Limit: limit, Cursor: cursor})
			if err != nil {
				return err
			}
			lock.Lock()
			total += len(page.Items)
			pages++
			lock.Unlock()
			if page.Complete {
				break
			}
			cursor = page.NextCursor
		}
		return nil
	})
	return total, pages, err
}
func (f *primitiveBenchFixture) freshGets(workers int) (int, int, error) {
	return f.freshGetSample(workers, 15)
}
func (f *primitiveBenchFixture) freshGetSample(workers, perNamespaceKind int) (int, int, error) {
	var total int
	var lock sync.Mutex
	err := primitiveBenchWorkers(f.ctx, workers, len(f.jobs), func(index int) error {
		job := f.jobs[index]
		connection, err := f.gateway.ConnectionForProvider(f.ctx, job.server)
		if err != nil {
			return err
		}
		for _, uri := range job.uris[:perNamespaceKind] {
			got, err := Get[json.RawMessage](f.ctx, f.gateway, connection, identity.ResourceRef{URI: uri}, nil)
			if err != nil {
				return err
			}
			if got.ResolvedResource == nil || len(got.Definition) == 0 {
				return identity.ErrResource
			}
			lock.Lock()
			total++
			lock.Unlock()
		}
		return nil
	})
	return total, 0, err
}
func (f *primitiveBenchFixture) wireLists(workers, limit int) (int, int, error) {
	var total, pages int
	var lock sync.Mutex
	err := primitiveBenchWorkers(f.ctx, workers, len(f.jobs), func(index int) error {
		job := f.jobs[index]
		client, err := f.manager.Get(f.ctx, "", job.server)
		if err != nil {
			return err
		}
		cursor := ""
		for {
			raw, err := toolRaw(f.ctx, client, primitive.Plural(job.kind)+"/list", primitive.ListRequest{Namespace: job.namespace, Limit: limit, Cursor: cursor})
			if err != nil {
				return err
			}
			result, err := primitive.DecodeList(raw, job.kind)
			if err != nil {
				return err
			}
			lock.Lock()
			total += len(result.Resources)
			pages++
			lock.Unlock()
			if result.Complete {
				break
			}
			cursor = result.NextCursor
		}
		return nil
	})
	return total, pages, err
}

func TestPrimitiveGatewayBenchmarkMatrix(t *testing.T) {
	if os.Getenv("AGENTLY_PRIMITIVE_BENCH") != "1" {
		t.Skip("explicit synthetic real-MCP performance matrix")
	}
	t.Log("FIXTURE:5 real SDK HTTP servers;100 tools/server(20with200input+200output fields,80narrow including6primitive/namespace tools);3 namespaces/server;30 resources/namespace;2 partial kinds/server;15 resources/kind/namespace;45 per each supported kind;450 entries total;45 templates include200-field schemas and8KiB instructions. Prompt/eval use provider-registered JSON envelopes (no application-domain/UI proof). Gateway upstream list Limit=100;requested10/20/100 alter local ListPage cursor count only;wire-list stage separately varies actual managing request Limit. Bytes count HTTP application bodies,not headers/TCP.")
	delays := []time.Duration{0}
	if os.Getenv("AGENTLY_PRIMITIVE_BENCH_RTT") == "1" {
		delays = append(delays, 20*time.Millisecond)
	}
	for _, delay := range delays {
		pages := []int{10, 20, 100}
		if delay > 0 {
			pages = []int{20}
		}
		for _, page := range pages {
			for _, workers := range []int{1, 4} {
				for repeat := 1; repeat <= 3; repeat++ {
					f := newPrimitiveBenchFixture(t, delay)
					f.measure(t, "cold.discovery", repeat, workers, page, delay, func() (int, int, error) { return f.discoverNamespaces(workers) })
					f.measure(t, "cold.namespace-page", repeat, workers, page, delay, func() (int, int, error) { return f.namespacePages(page) })
					f.measure(t, "warm.namespace-page", repeat, workers, page, delay, func() (int, int, error) { return f.namespacePages(page) })
					f.measure(t, "cold.list", repeat, workers, page, delay, func() (int, int, error) { return f.genericLists(workers, page) })
					f.measure(t, "warm.list", repeat, workers, page, delay, func() (int, int, error) { return f.genericLists(workers, page) })
					f.measure(t, "cold.fresh-get", repeat, workers, page, delay, func() (int, int, error) { return f.freshGets(workers) })
					f.measure(t, "warm.fresh-get", repeat, workers, page, delay, func() (int, int, error) { return f.freshGets(workers) })
					f.measure(t, "wire.list", repeat, workers, page, delay, func() (int, int, error) { return f.wireLists(workers, page) })
					f.close()
				}
			}
		}
	}
}

func TestPrimitiveGatewayBenchmarkFixtureSmoke(t *testing.T) {
	if os.Getenv("AGENTLY_PRIMITIVE_BENCH_SMOKE") != "1" {
		t.Skip("explicit real-MCP benchmark fixture validation")
	}
	f := newPrimitiveBenchFixture(t, 0)
	defer f.close()
	entries, _, err := f.namespacePages(10)
	if err != nil || entries != 15 {
		t.Fatalf("namespace fixture:%d %v", entries, err)
	}
	entries, _, err = f.genericLists(1, 10)
	if err != nil || entries != 450 {
		t.Fatalf("list fixture:%d %v", entries, err)
	}
	kinds := map[string]bool{}
	for _, job := range f.jobs {
		if kinds[job.kind] {
			continue
		}
		kinds[job.kind] = true
		connection, err := f.gateway.ConnectionForProvider(f.ctx, job.server)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Get[json.RawMessage](f.ctx, f.gateway, connection, identity.ResourceRef{URI: job.uris[0]}, nil); err != nil {
			t.Fatalf("%s Get:%v", job.kind, err)
		}
	}
	names := make([]string, 0, len(kinds))
	for kind := range kinds {
		names = append(names, kind)
	}
	sort.Strings(names)
	t.Logf("VALIDATED kinds=%v entries=%d definitionBytes=%d", names, entries, f.definitionBytes)
}

func TestPrimitiveGatewayLatencySimulationBenchmark(t *testing.T) {
	if os.Getenv("AGENTLY_PRIMITIVE_BENCH_RTT") != "1" {
		t.Skip("explicit20ms-per-request latency simulation")
	}
	t.Log("SIMULATION:not measuredWAN RTT;fixture adds20ms to everyrealSDK HTTP POST;5servers100tools each,450entries;freshGet sample30(one pernamespace/kind),not all450;page20;3repeats.")
	for _, workers := range []int{1, 4} {
		for repeat := 1; repeat <= 3; repeat++ {
			f := newPrimitiveBenchFixture(t, 20*time.Millisecond)
			f.measure(t, "cold.discovery", repeat, workers, 20, 20*time.Millisecond, func() (int, int, error) { return f.discoverNamespaces(workers) })
			f.measure(t, "cold.list", repeat, workers, 20, 20*time.Millisecond, func() (int, int, error) { return f.genericLists(workers, 20) })
			f.measure(t, "warm.list", repeat, workers, 20, 20*time.Millisecond, func() (int, int, error) { return f.genericLists(workers, 20) })
			f.measure(t, "rtt.sample-fresh-get", repeat, workers, 20, 20*time.Millisecond, func() (int, int, error) { return f.freshGetSample(workers, 1) })
			f.measure(t, "rtt.sample-fresh-get-repeat", repeat, workers, 20, 20*time.Millisecond, func() (int, int, error) { return f.freshGetSample(workers, 1) })
			f.close()
		}
	}
}
