package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

func TestCandidateDiscoveryDiagnosticsContainOnlySubphaseMetrics(t *testing.T) {
	for _, failBundles := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "bundle-failure"}[failBundles], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "trace.jsonl")
			t.Setenv("AGENTLY_DEBUG_TRACE_FILE", path)
			service, input, definitions, bundles := preparedDiscoveryFixture()
			input.Query = "private-query-payload"
			input.Agent.ID = "private-agent-id"
			definitions[0].Description = "private-description-payload"
			definitions[0].Parameters["private-schema-payload"] = true
			bundles[0].ID = "private-bundle-id"
			failure := errors.New("private-error-payload")
			if failBundles {
				service.toolBundles = func(context.Context) ([]*toolbundle.Bundle, error) { return nil, failure }
			}
			ctx := requestctx.WithTurnMeta(authctx.WithBearer(context.Background(), "private-bearer-payload"), requestctx.TurnMeta{TurnID: "diagnostic-turn", ConversationID: "diagnostic-conversation"})
			result, err := service.discoverableToolDefinitions(ctx, input)
			if failBundles {
				require.ErrorIs(t, err, failure)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.Len(t, result, 3)
			}
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "private-")
			expected := []string{"candidate_profile", "candidate_definitions", "candidate_caller_scope", "candidate_bundles", "candidate_rules", "candidate_visibility", "candidate_namespace", "candidate_discovery"}
			counts := map[string]float64{"candidate_profile": 0, "candidate_definitions": 4, "candidate_caller_scope": 4, "candidate_bundles": 2, "candidate_rules": 3, "candidate_visibility": 3, "candidate_namespace": 3, "candidate_discovery": 3}
			if failBundles {
				expected = []string{"candidate_profile", "candidate_definitions", "candidate_caller_scope", "candidate_bundles", "candidate_discovery"}
				counts["candidate_bundles"] = 0
				counts["candidate_discovery"] = 0
			}
			var stages []string
			decoder := json.NewDecoder(bytes.NewReader(raw))
			for {
				var record struct {
					Component string                 `json:"component"`
					Event     string                 `json:"event"`
					Fields    map[string]interface{} `json:"fields"`
				}
				err := decoder.Decode(&record)
				if errors.Is(err, io.EOF) {
					break
				}
				require.NoError(t, err)
				stage, ok := record.Fields["stage"].(string)
				if !ok || !strings.HasPrefix(stage, "candidate_") {
					continue
				}
				stages = append(stages, stage)
				require.Equal(t, "agent", record.Component)
				require.Equal(t, "binding_tool_stage", record.Event)
				require.Len(t, record.Fields, 7)
				require.Equal(t, "diagnostic-turn", record.Fields["turnID"])
				require.Equal(t, "diagnostic-conversation", record.Fields["conversationID"])
				require.Equal(t, counts[stage], record.Fields["count"])
				require.Equal(t, false, record.Fields["reused"])
				require.Equal(t, failBundles && (stage == "candidate_bundles" || stage == "candidate_discovery"), record.Fields["failed"])
				elapsed, ok := record.Fields["elapsedMs"].(float64)
				require.True(t, ok)
				require.GreaterOrEqual(t, elapsed, float64(0))
			}
			require.Equal(t, expected, stages)
		})
	}
}
