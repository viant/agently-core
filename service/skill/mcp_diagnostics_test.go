package skill

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	proto "github.com/viant/agently-core/protocol/skill"
	"github.com/viant/jsonrpc"
)

type failingDiagnosticSkillSource struct {
	wireSource
	err    error
	failAt string
}

func (s *failingDiagnosticSkillSource) Names(context.Context) ([]string, error) {
	if s.failAt == "options" {
		return []string{"upstream"}, nil
	}
	return nil, s.err
}

func (s *failingDiagnosticSkillSource) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return nil, s.err
}

type failingDiagnosticSkillDelivery struct {
	fixedNativeSkillDelivery
	err error
}

func (d *failingDiagnosticSkillDelivery) List(context.Context) ([]proto.Metadata, error) {
	return nil, d.err
}

func TestSkillCatalogFailureLogIdentifiesStageWithoutErrorPayload(t *testing.T) {
	for _, stage := range []string{"remote_entries", "primitive_metadata"} {
		t.Run(stage, func(t *testing.T) {
			t.Setenv("AGENTLY_DEBUG", "off")
			t.Setenv("AGENTLY_DEBUG_COMPONENTS", "other")
			failure := &jsonrpc.Error{Code: -32003, Message: "private-error-message", Data: json.RawMessage(`{"status":403,"token":"private-token"}`)}
			service := &Service{registry: proto.NewRegistry()}
			if stage == "remote_entries" {
				service.mcpSource = &failingDiagnosticSkillSource{err: failure}
			} else {
				service.delivery = &failingDiagnosticSkillDelivery{err: failure}
			}
			var output bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&output)
			t.Cleanup(func() { log.SetOutput(previous) })
			ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "reader"})
			_, err := service.ListVisible(ctx, &agentmdl.Agent{Skills: []string{"*"}})
			require.ErrorIs(t, err, failure)
			logs := output.String()
			require.Contains(t, logs, `skill.catalog.failed stage="`+stage+`"`)
			if stage == "remote_entries" {
				require.Contains(t, logs, `skill.discovery.failed stage="names" server=""`)
			}
			require.Contains(t, logs, `rpcCode=-32003 rpcStatus=403`)
			require.NotContains(t, logs, "private-error-message")
			require.NotContains(t, logs, "private-token")
		})
	}
}

func TestSkillDiscoveryFailureNamesServerAtOptionsBoundary(t *testing.T) {
	t.Setenv("AGENTLY_DEBUG", "off")
	failure := &jsonrpc.Error{Code: -32003, Message: "private-error-message", Data: json.RawMessage(`{"status":403,"token":"private-token"}`)}
	service := &Service{registry: proto.NewRegistry(), mcpSource: &failingDiagnosticSkillSource{err: failure, failAt: "options"}}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "reader"})
	_, err := service.ListVisible(ctx, &agentmdl.Agent{Skills: []string{"*"}})
	require.ErrorIs(t, err, failure)
	require.Contains(t, output.String(), `skill.discovery.failed stage="options" server="upstream"`)
	require.Contains(t, output.String(), `rpcCode=-32003 rpcStatus=403`)
	require.NotContains(t, output.String(), "private-token")
}
