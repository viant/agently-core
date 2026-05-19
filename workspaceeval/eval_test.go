package workspaceeval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	sdkapi "github.com/viant/agently-core/sdk/api"
)

func TestCheckEvalCatalogAndPublicCoverage(t *testing.T) {
	root := t.TempDir()
	evals := filepath.Join(root, "evals")
	prompts := filepath.Join(root, "prompts")
	templates := filepath.Join(root, "templates")
	agents := filepath.Join(root, "agents")
	for _, dir := range []string{evals, prompts, templates, agents} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(filepath.Join(prompts, "performance_analysis.yaml"), "id: performance_analysis\n")
	write(filepath.Join(templates, "analytics_dashboard.yaml"), "id: analytics_dashboard\n")
	write(filepath.Join(agents, "steward.yaml"), "id: steward\nprofile:\n  publish: true\nstarterTasks:\n  - id: one\n    coverageEvalIds:\n      - ok\n")
	write(filepath.Join(evals, "01_ok.yaml"), "id: ok\ntitle: ok\nuser_prompt: hi\nexpected_routing:\n  agent: steward\n  profile: performance_analysis\nexpected_output:\n  template: analytics_dashboard\n")

	agentIndex, err := LoadAgents(agents)
	if err != nil {
		t.Fatal(err)
	}
	if got := CheckEvalCatalog(evals, prompts, templates, agentIndex); len(got) != 0 {
		t.Fatalf("unexpected eval catalog errors: %v", got)
	}
	if got := CheckPublicAgentsCovered(evals, agentIndex); len(got) != 0 {
		t.Fatalf("unexpected public coverage errors: %v", got)
	}
	if got := CheckStarterTaskCoverage(evals, agentIndex); len(got) != 0 {
		t.Fatalf("unexpected starter coverage errors: %v", got)
	}
}

func TestCheckEvidenceContractProfiles(t *testing.T) {
	root := t.TempDir()
	prompts := filepath.Join(root, "prompts")
	if err := os.MkdirAll(prompts, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range DefaultRequiredEvidenceContractProfiles() {
		body := "id: " + id + "\n" +
			"evidenceContract:\n" +
			"  required:\n" +
			"    - one\n" +
			"  completion:\n" +
			"    - done\n"
		if err := os.WriteFile(filepath.Join(prompts, id+".yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := CheckEvidenceContractProfiles(prompts, DefaultRequiredEvidenceContractProfiles()); len(got) != 0 {
		t.Fatalf("unexpected evidence contract errors: %v", got)
	}
}

func TestAssertBehavioralTranscript(t *testing.T) {
	inline := func(v map[string]interface{}) json.RawMessage {
		body, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		wrapped, err := json.Marshal(map[string]interface{}{"inlineBody": string(body)})
		if err != nil {
			t.Fatal(err)
		}
		return wrapped
	}

	transcript := &sdkapi.ConversationStateResponse{
		Conversation: &sdkapi.ConversationState{
			Turns: []*sdkapi.TurnState{
				{
					TurnID: "turn-1",
					Status: sdkapi.TurnStatusCompleted,
					Execution: &sdkapi.ExecutionState{
						Pages: []*sdkapi.ExecutionPageState{
							{
								PageID: "page-1",
								ModelSteps: []*sdkapi.ModelStepState{
									{
										ProviderRequestPayload: inline(map[string]interface{}{
											"input": "show the most 3 impactful deal ids in the last 2 days",
										}),
									},
								},
								ToolSteps: []*sdkapi.ToolStepState{
									{
										ToolName:       "steward-AdHierarchy",
										RequestPayload: inline(map[string]interface{}{"AdOrderId": []int{2652067}}),
									},
									{
										ToolName:       "llm/agents:start",
										RequestPayload: inline(map[string]interface{}{"agentId": "data-analyst", "promptProfileId": "diagnostic_baseline"}),
									},
									{
										ToolName:       "template:get",
										RequestPayload: inline(map[string]interface{}{"name": "analytics_dashboard", "includeDocument": true}),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	doc := EvalDoc{
		ID:         "behavioral-smoke",
		Title:      "behavioral smoke",
		UserPrompt: "Troubleshoot order 2657966, use exploratory strategy",
	}
	doc.ExpectedRouting.Agent = "data-analyst"
	doc.ExpectedRouting.Profile = "diagnostic_baseline"
	doc.ExpectedPreDelegationTools = []EvalToolExpectation{{Name: "steward-AdHierarchy"}}
	doc.ExpectedOutput.Template = "analytics_dashboard"

	if err := AssertBehavioralTranscript(doc, transcript); err != nil {
		t.Fatalf("unexpected behavioral assertion failure: %v", err)
	}
}

func TestParseConversationID(t *testing.T) {
	output := "[workspace] /tmp/demo\n[agent] steward\nhello\n[conversation-id] conv-123\n"
	if got := ParseConversationID(output); got != "conv-123" {
		t.Fatalf("expected conv-123, got %q", got)
	}
}
