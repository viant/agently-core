package skill

import (
	"context"
	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	proto "github.com/viant/agently-core/protocol/skill"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	format "github.com/viant/mcp-protocol/extension/skills"
	protocol "github.com/viant/mcp-protocol/server"
	client "github.com/viant/mcp/client"
	server "github.com/viant/mcp/server"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

type skillWire struct {
	handler transport.Handler
	id      atomic.Uint64
	methods []string
}

func (w *skillWire) Notify(ctx context.Context, n *jsonrpc.Notification) error {
	w.handler.OnNotification(ctx, n)
	return nil
}
func (w *skillWire) Send(ctx context.Context, r *jsonrpc.Request) (*jsonrpc.Response, error) {
	r.Id = w.id.Add(1)
	out := &jsonrpc.Response{}
	w.methods = append(w.methods, r.Method)
	w.handler.Serve(ctx, r, out)
	return out, nil
}

type wireSource struct{ client client.Interface }

func (s *wireSource) Names(context.Context) ([]string, error) { return []string{"upstream"}, nil }
func (s *wireSource) Options(context.Context, string) (*cfg.MCPClient, error) {
	return &cfg.MCPClient{SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}}, nil
}
func (s *wireSource) Get(context.Context, string, string) (client.Interface, error) {
	return s.client, nil
}
func (s *wireSource) WithAuthTokenContext(ctx context.Context, _ string) context.Context { return ctx }
func (s *wireSource) UseIDToken(context.Context, string) bool                            { return false }

func TestNativeSkillsWireBridge(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	files := fstest.MapFS{"SKILL.md": {Data: []byte("---\nname: review\ndescription: Review\nallowed-tools: lookup\n---\nRead refs/note.txt.")}, "refs/note.txt": {Data: []byte("support")}}
	factory := protocol.WithDefaultHandler(ctx, func(d *protocol.DefaultHandler) error {
		for _, uri := range []string{"skill://team-a/review/SKILL.md", "docs://team-b/review/SKILL.md"} {
			compiled, err := (format.Compiler{Source: files}).Compile(ctx, uri)
			if err != nil {
				return err
			}
			if err = d.RegisterStaticSkill(compiled); err != nil {
				return err
			}
		}
		return nil
	})
	srv, err := server.New(server.WithNewHandler(factory))
	require.NoError(t, err)
	wire := &skillWire{}
	wire.handler = srv.NewHandler(ctx, wire)
	cli := client.New("test", "1", wire, client.WithProtocolVersion("2026-07-28"))
	defer cli.Close()
	s := &Service{mcpSource: &wireSource{client: cli}, registry: proto.NewRegistry()}
	a := &agentmdl.Agent{Skills: []string{"*"}}
	listed, err := s.ListVisible(ctx, a)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	require.NotEqual(t, listed[0].URI, listed[1].URI)
	require.NotContains(t, wire.methods, "resources/read")
	require.Contains(t, wire.methods, "skills/list")
	_, err = s.GetVisible(ctx, a, "upstream/review")
	require.ErrorContains(t, err, "ambiguous")
	item, err := s.GetVisible(ctx, a, listed[0].URI)
	require.NoError(t, err)
	require.Equal(t, "", item.Frontmatter.AllowedTools)
	require.Equal(t, "upstream:lookup", item.RequestedTools)
	text, _, _, err := s.readSupporting(ctx, item, "refs/note.txt")
	require.NoError(t, err)
	require.Equal(t, "support", text)
	_, _, _, err = s.readSupporting(ctx, item, "../escape")
	require.Error(t, err)
	require.Contains(t, wire.methods, "skills/get")
	require.Contains(t, wire.methods, "resources/read")
	require.NotContains(t, wire.methods, "tools/call")
	c := BuildConstraints([]*proto.Skill{item})
	require.NotNil(t, c)
	require.Error(t, ValidateExecution(WithConstraints(ctx, c), "system/exec:execute", nil))
	require.Error(t, ValidateExecution(WithConstraints(ctx, c), "other:lookup", nil))
}
