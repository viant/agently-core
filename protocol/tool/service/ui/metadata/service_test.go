package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	identity "github.com/viant/agently-core/protocol/resource"
	mcp "github.com/viant/agently-core/protocol/tool/adapter/mcp"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	bridge "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/forge/backend/types"
	"github.com/viant/forge/backend/uimetadata"
	"strings"
	"testing"
	"time"
)

type scopeKey struct{}
type principalKey struct{}
type scope struct {
	begins, finishes int
	finishErr        error
}

func (s *scope) BeginMetadataRead(ctx context.Context) (context.Context, func() error, error) {
	s.begins++
	if ctx.Value(principalKey{}) != "current-user" {
		return nil, nil, errors.New("verified current principal required")
	}
	return context.WithValue(ctx, scopeKey{}, true), func() error { s.finishes++; return s.finishErr }, nil
}
func (*scope) WithoutMetadataRead(ctx context.Context) context.Context {
	panic("metadata must never bypass its scope")
}

type reader struct {
	scope       *scope
	gets, lists int
	deny        bool
	last        *bridge.WindowDefinitionGetInput
}

func (r *reader) UsesWindowResourceResolution() bool      { return true }
func (r *reader) MetadataScope() bridge.MetadataReadScope { return r.scope }
func (r *reader) WindowDefinitionsList(ctx context.Context, in *bridge.WindowDefinitionListInput) (*bridge.WindowDefinitionListOutput, error) {
	r.lists++
	if ctx.Value(scopeKey{}) != true {
		return nil, errors.New("scope lost")
	}
	if r.deny {
		return nil, identity.ErrResourceDenied
	}
	return &bridge.WindowDefinitionListOutput{Windows: []bridge.WindowDefinitionSummary{{WindowID: "record", Title: "Record", ResourceURI: "window://team/record"}}, HasMore: true}, nil
}
func (r *reader) WindowDefinitionGet(ctx context.Context, in *bridge.WindowDefinitionGetInput) (*bridge.WindowDefinitionGetOutput, error) {
	r.gets++
	r.last = in
	if ctx.Value(scopeKey{}) != true {
		return nil, errors.New("scope lost")
	}
	if r.deny || in.WindowID != "record" && in.WindowID != "window://team/record" {
		return nil, identity.ErrResourceDenied
	}
	if in.Resource != nil && (in.Resource.URI != "window://team/record" || in.Resource.Revision != "stamp1") {
		return nil, identity.ErrResourceDenied
	}
	w := &types.Window{WindowKey: "record", Resource: &identity.ResolvedResource{URI: "window://team/record", ResourceCandidate: identity.ResourceCandidate{Kind: identity.StampedCandidate, Revision: "stamp1", ContentFingerprint: strings.Repeat("a", 64)}, AuthorityBinding: "PRIVATE_AUTHORITY_TOKEN", ValidUntil: time.Now().Add(time.Minute)}, ResourceTarget: &types.WindowTarget{Platform: "web", SelectionToken: "PRIVATE_SELECTION_TOKEN", DependencyPins: map[string]identity.ResolvedResource{"secret": {AuthorityBinding: "PRIVATE_CHILD_PIN"}}}, View: types.View{Content: &types.Container{ID: "root", Items: []types.Item{{ID: "name", Label: "Name", Value: "PRIVATE_LIVE_VALUE"}}}}, Actions: &types.Actions{Code: "PRIVATE_JS"}}
	choices := make([]types.Option, 0, 27)
	for i := 0; i < 27; i++ {
		choices = append(choices, types.Option{Label: fmt.Sprintf("Currency%d", i), Value: fmt.Sprintf("currency%d", i)})
	}
	choices[25].Value = "USD"
	w.View.Content.Items = append(w.View.Content.Items, types.Item{ID: "currency", Options: choices})
	return &bridge.WindowDefinitionGetOutput{WindowID: "record", Definition: w}, nil
}
func currentContext() context.Context {
	return requestctx.WithConversationID(context.WithValue(context.Background(), principalKey{}, "current-user"), "conversation-one")
}
func TestUnopenedMetadataUsesOnlyAuthorizedDefinitionReader(t *testing.T) {
	r := &reader{scope: &scope{}}
	s := New(r)
	listed := &ListOutput{}
	list, _ := s.Method("list")
	if err := list(currentContext(), &ListInput{Limit: 1}, listed); err != nil {
		t.Fatal(err)
	}
	if r.gets != 0 || r.lists != 1 || len(listed.Windows) != 1 || listed.NextOffset != 1 {
		t.Fatalf("list fetched a definition or lost bounds: %+v", listed)
	}
	get, _ := s.Method("get")
	out := &GetOutput{}
	target := &uimetadata.Target{Platform: "web"}
	ref := &identity.ResourceRef{URI: "window://team/record", Revision: "stamp1"}
	if err := get(currentContext(), &GetInput{ID: "record", Resource: ref, Target: target, MaxNodes: 1}, out); err != nil {
		t.Fatal(err)
	}
	if r.gets != 1 || r.last.Target.Platform != target.Platform || r.last.Resource != ref || out.Resource.Revision != "stamp1" || len(out.Metadata.Nodes) != 1 || out.Metadata.NextCursor == "" {
		t.Fatalf("selected resource/target/bounds lost %+v", out)
	}
	data, _ := json.Marshal(out)
	for _, private := range []string{"PRIVATE_AUTHORITY_TOKEN", "PRIVATE_SELECTION_TOKEN", "PRIVATE_CHILD_PIN", "PRIVATE_JS", "PRIVATE_LIVE_VALUE", "authorityBinding", "validUntil"} {
		if strings.Contains(string(data), private) {
			t.Fatal("leaked " + private)
		}
	}
	if r.scope.begins != 2 || r.scope.finishes != 2 {
		t.Fatal("metadata scope not finalized")
	}
}
func TestScopeIdentityAndFinalizationFailuresDiscardOutput(t *testing.T) {
	r := &reader{scope: &scope{}}
	s := New(r)
	get, _ := s.Method("get")
	for _, ctx := range []context.Context{context.Background(), requestctx.WithConversationID(context.Background(), "other-conversation")} {
		out := &GetOutput{Resource: &identity.ResourceRef{URI: "stale"}}
		if err := get(ctx, &GetInput{ID: "record"}, out); err == nil || out.Resource != nil {
			t.Fatal("unverified context retained metadata")
		}
	}
	if r.gets != 0 {
		t.Fatal("definition reached without verified identity")
	}
	r.scope.finishErr = errors.New("authority changed")
	out := &GetOutput{Resource: &identity.ResourceRef{URI: "stale"}}
	if err := get(currentContext(), &GetInput{ID: "record"}, out); err == nil || out.Resource != nil || out.Metadata != nil {
		t.Fatal("authority finalization failure published metadata")
	}
	r.scope.finishErr = nil
	r.deny = true
	if err := get(currentContext(), &GetInput{ID: "record"}, out); err == nil || out.Metadata != nil {
		t.Fatal("denied resource leaked metadata")
	}
}
func TestBoundsTraversalResourceAndMethodValidation(t *testing.T) {
	r := &reader{scope: &scope{}}
	s := New(r)
	get, _ := s.Method("get")
	for _, in := range []*GetInput{{ID: "../private"}, {ID: "file:///private"}, {ID: "record", Resource: &identity.ResourceRef{URI: "window://team/../private"}}, {ID: "record", Resource: &identity.ResourceRef{URI: "https://private/record"}}} {
		if err := get(currentContext(), in, &GetOutput{}); err == nil {
			t.Fatalf("unsafe identity accepted %+v", in)
		}
	}
	if r.gets != 0 {
		t.Fatal("unsafe path reached reader")
	}
	for _, in := range []*GetInput{{ID: "record", Resource: &identity.ResourceRef{URI: "window://team/record", Revision: "wrong"}}, {ID: "record", Path: "../private"}, {ID: "record", MaxNodes: 101}} {
		if err := get(currentContext(), in, &GetOutput{}); err == nil {
			t.Fatal("invalid scope/revision accepted")
		}
	}
	list, _ := s.Method("list")
	for _, in := range []*ListInput{{Limit: 101}, {Offset: -1}, {Offset: 100001}, {Query: strings.Repeat("x", 241)}} {
		if err := list(currentContext(), in, &ListOutput{}); err == nil {
			t.Fatal("unbounded list accepted")
		}
	}
	if _, err := s.Method("open"); err == nil {
		t.Fatal("metadata exposes OPEN")
	}
}
func TestMCPDiscoveryAdvertisesMetadataReadBoundary(t *testing.T) {
	tools := mcp.FromService(New(&reader{scope: &scope{}}))
	if len(tools) != 2 {
		t.Fatal("metadata tools not discoverable")
	}
	for _, tool := range tools {
		if tool.Description == nil || !strings.Contains(*tool.Description, "Metadata only") || strings.Contains(*tool.Description, "ui/window:open") {
			t.Fatalf("unclear metadata boundary: %v", tool.Description)
		}
		if len(tool.InputSchema.Properties) == 0 {
			t.Fatal("MCP query schema missing")
		}
		bytes, _ := json.Marshal(tool.InputSchema)
		for _, private := range []string{"selectionToken", "executionProof", "dependencyPins", "authorityBinding", "clientSecret"} {
			if strings.Contains(string(bytes), private) {
				t.Fatal("private input schema exposed: " + private)
			}
		}
	}
}

var _ DefinitionReader = (*bridge.Service)(nil)

func TestMetadataAdapterPagesAllStaticChoicesAndReauthorizesEveryPage(t *testing.T) {
	r := &reader{scope: &scope{}}
	s := New(r)
	get, _ := s.Method("get")
	out := &GetOutput{}
	requireErr := get(currentContext(), &GetInput{ID: "record", NodeID: "currency", MaxNodes: 1}, out)
	if requireErr != nil {
		t.Fatal(requireErr)
	}
	options := out.Metadata.Nodes[0].Options
	if options == nil || options.Path == "" || options.Total != 27 {
		t.Fatal("option continuation missing")
	}
	path := options.Path
	cursor := ""
	seen := 0
	foundUSD := false
	for i := 0; i < 10; i++ {
		page := &GetOutput{}
		if err := get(currentContext(), &GetInput{ID: "record", Path: path, Cursor: cursor, MaxNodes: 6}, page); err != nil {
			t.Fatal(err)
		}
		for _, node := range page.Metadata.Nodes {
			seen++
			if node.Option.Value == "USD" {
				foundUSD = true
			}
		}
		if page.Metadata.NextCursor == "" {
			break
		}
		cursor = page.Metadata.NextCursor
	}
	if seen != 27 || !foundUSD || r.gets != 6 {
		t.Fatalf("metadata choices not complete/re-authorized seen=%d USD=%v reads=%d", seen, foundUSD, r.gets)
	}
	r.deny = true
	denied := &GetOutput{}
	if err := get(currentContext(), &GetInput{ID: "record", Path: path, Cursor: cursor, MaxNodes: 6}, denied); err == nil || denied.Metadata != nil {
		t.Fatal("denied option continuation leaked choices")
	}
}
