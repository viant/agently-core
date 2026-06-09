package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/agent/execution"
	goalruntime "github.com/viant/agently-core/service/goal"
)

func TestBuildGoalContinuationHint_FromPlanStep(t *testing.T) {
	hint := buildGoalContinuationHint(&QueryOutput{
		Plan: &execution.Plan{
			Intention: "finish parser cleanup",
			Steps: execution.Steps{
				{
					Type:   "tool",
					Name:   "system/exec",
					Reason: "Run the focused parser test suite",
					Args: map[string]interface{}{
						"cmd": "go test ./parser/...",
					},
				},
			},
		},
	})
	require.NotNil(t, hint)
	require.Equal(t, "continue planned step: system/exec", hint.Reason)
	require.Contains(t, hint.Preview, "Run tool system/exec")
	require.Contains(t, hint.Payload, "Next planned step: Run tool system/exec")
	require.Contains(t, hint.Payload, `"cmd":"go test ./parser/..."`)
}

func TestBuildGoalContinuationHint_FromExplicitContentHint(t *testing.T) {
	hint := buildGoalContinuationHint(&QueryOutput{
		Content: `{"continuationHint":"Call message-show with messageId=msg-1 and byteRange.from=1000, byteRange.to=2000.","nextArgs":{"messageId":"msg-1","byteRange":{"from":1000,"to":2000}}}`,
	})
	require.NotNil(t, hint)
	require.Equal(t, "continue explicit tool-result continuation", hint.Reason)
	require.Contains(t, hint.Preview, "Call message-show")
	require.Contains(t, hint.Payload, `"messageId":"msg-1"`)
}

func TestBuildGoalContinuationHint_PrefersExplicitContentOverPlan(t *testing.T) {
	hint := buildGoalContinuationHint(&QueryOutput{
		Content: `continuationHint: Call message-show with messageId=msg-1 and byteRange.from=1000, byteRange.to=2000.`,
		Plan: &execution.Plan{
			Steps: execution.Steps{
				{Type: "tool", Name: "system/exec"},
			},
		},
	})
	require.NotNil(t, hint)
	require.Equal(t, "continue explicit tool-result continuation", hint.Reason)
	require.Contains(t, hint.Preview, "message-show")
}

func TestBuildGoalProgressFingerprint_ChangesWithContentAndPlan(t *testing.T) {
	continuation := &goalruntime.ContinuationHint{
		Reason:  "continue planned step",
		Preview: "Continue: Run tool system/exec",
		Payload: "Continue working toward the active goal.",
	}
	base := buildGoalProgressFingerprint(&QueryOutput{
		Content: "Working through the parser cleanup now.",
		Plan: &execution.Plan{
			Steps: execution.Steps{{Type: "tool", Name: "system/exec", Reason: "Run parser tests"}},
		},
	}, continuation)
	changedContent := buildGoalProgressFingerprint(&QueryOutput{
		Content: "Parser cleanup is done; moving to formatter fixes.",
		Plan: &execution.Plan{
			Steps: execution.Steps{{Type: "tool", Name: "system/exec", Reason: "Run parser tests"}},
		},
	}, continuation)
	changedPlan := buildGoalProgressFingerprint(&QueryOutput{
		Content: "Working through the parser cleanup now.",
		Plan: &execution.Plan{
			Steps: execution.Steps{{Type: "tool", Name: "system/patch", Reason: "Apply formatter fix"}},
		},
	}, continuation)
	require.NotEmpty(t, base)
	require.NotEqual(t, base, changedContent)
	require.NotEqual(t, base, changedPlan)
}
