package config

import (
	"os"
	"path/filepath"
	"testing"

	execconfig "github.com/viant/agently-core/app/executor/config"
	"gopkg.in/yaml.v3"
)

func TestLoadExpandsMCPServerAddress(t *testing.T) {
	tests := []struct {
		name   string
		setEnv bool
		want   string
	}{
		{name: "environment override", setEnv: true, want: "127.0.0.1:5003"},
		{name: "fallback", want: "127.0.0.1:5001"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.setEnv {
				t.Setenv("AGENTLY_TEST_MCP_ADDR", "127.0.0.1:5003")
			} else {
				t.Setenv("AGENTLY_TEST_MCP_ADDR", "")
			}
			root := t.TempDir()
			data := []byte("mcpServer:\n  addr: ${AGENTLY_TEST_MCP_ADDR:-127.0.0.1:5001}\n")
			if err := os.WriteFile(filepath.Join(root, "config.yaml"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if got := loaded.MCPServer.Addr; got != test.want {
				t.Fatalf("MCPServer.Addr = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLoadExpandsEnvironmentInAllScalarValuesAndPreservesRuntimeMacros(t *testing.T) {
	t.Setenv("AGENTLY_TEST_RUNTIME_ROOT", "/tmp/agently-runtime")
	t.Setenv("AGENTLY_TEST_INDEX_PATH", "/tmp/agently-index")
	root := t.TempDir()
	data := []byte(`
default:
  runtimeRoot: ${AGENTLY_TEST_RUNTIME_ROOT:-/opt/agently/data}
  statePath: ${runtimeRoot}/state
  dbPath: ${AGENTLY_TEST_DB_PATH:-/tmp/default.db}
  resources:
    indexPath: ${AGENTLY_TEST_INDEX_PATH}
    snapshotPath: ${runtimeRoot}/snapshots
auth:
  oauth:
    client:
      configURL: ${AGENTLY_TEST_CONFIG_URL:-idp.enc|blowfish://default}
`)
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := loaded.ResolveDefaultsWithFallback(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := defaults.RuntimeRoot, "/tmp/agently-runtime"; got != want {
		t.Fatalf("RuntimeRoot = %q, want %q", got, want)
	}
	if got, want := defaults.StatePath, "${runtimeRoot}/state"; got != want {
		t.Fatalf("StatePath = %q, want %q", got, want)
	}
	if got, want := defaults.DBPath, "/tmp/default.db"; got != want {
		t.Fatalf("DBPath = %q, want %q", got, want)
	}
	if got, want := defaults.Resources.IndexPath, "/tmp/agently-index"; got != want {
		t.Fatalf("Resources.IndexPath = %q, want %q", got, want)
	}
	if got, want := defaults.Resources.SnapshotPath, "${runtimeRoot}/snapshots"; got != want {
		t.Fatalf("Resources.SnapshotPath = %q, want %q", got, want)
	}
	var auth map[string]interface{}
	if err := loaded.AuthNode.Decode(&auth); err != nil {
		t.Fatal(err)
	}
	oauth := mapLookup(auth, "oauth")
	client := mapLookup(oauth, "client")
	if got, want := client["configURL"], "idp.enc|blowfish://default"; got != want {
		t.Fatalf("auth.oauth.client.configURL = %q, want %q", got, want)
	}
}

func TestResolveDefaultsLoadsAgentAutoSelectionPromptURI(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "intake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "intake", "router.md"), []byte("Route to the best authorized agent."), 0o600); err != nil {
		t.Fatal(err)
	}
	data := []byte("default:\n  agentAutoSelection:\n    prompt:\n      uri: intake/router.md\n")
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := loaded.ResolveDefaultsWithFallback(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := defaults.AgentAutoSelection.Prompt.Text, "Route to the best authorized agent."; got != want {
		t.Fatalf("AgentAutoSelection.Prompt.Text = %q, want %q", got, want)
	}
}

func TestDefaultsWithFallbackMergesAdvancedDefaults(t *testing.T) {
	fallback := &execconfig.Defaults{
		Model:    "fallback-model",
		Embedder: "fallback-embedder",
		Agent:    "fallback-agent",
		Reporting: execconfig.ReportingDefaults{
			Enabled:         false,
			QueueIntervalMs: 100,
			QueueBatchLimit: 10,
			TransitionalWithUI: execconfig.ReportingTransitionalWithUIDefaults{
				Admission:            "closed",
				Persistence:          "disabled",
				ExportFromRun:        "enabled",
				Orchestration:        "enabled",
				ConversationAdoption: "disabled",
			},
		},
		PreviewSettings: execconfig.PreviewSettings{
			Limit:           1000,
			AgedLimit:       200,
			ToolResultLimit: 300,
			AgedAfterSteps:  3,
		},
		ToolCallMaxResults:    2,
		ToolCallTimeoutSec:    5,
		ElicitationTimeoutSec: 10,
	}

	root := &Root{}
	const yamlConfig = `
default:
  model: openai_gpt-5_4
  reporting:
    enabled: true
    queueIntervalMs: 250
    queueBatchLimit: 25
    store:
      backend: sql
      connectorRef: agently
    transitionalWithUI:
      admission: open
      persistence: enabled
      exportFromRun: disabled
      orchestration: disabled
      conversationAdoption: enabled
  skills:
    model: openai_gpt-5.4-mini
  previewSettings:
    limit: 8000
    agedLimit: 2500
    toolResultLimit: 32000
    agedAfterSteps: 2
  toolCallMaxResults: 7
  toolCallTimeoutSec: 45
  elicitationTimeoutSec: 90
`
	if err := yaml.Unmarshal([]byte(yamlConfig), root); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}

	got := root.DefaultsWithFallback(fallback)
	if got == nil {
		t.Fatalf("DefaultsWithFallback() = nil")
	}

	if got.Model != "openai_gpt-5_4" {
		t.Fatalf("expected merged model, got %q", got.Model)
	}
	if !got.Reporting.Enabled {
		t.Fatalf("expected reporting enabled to merge from workspace defaults")
	}
	if got.Reporting.QueueIntervalMs != 250 {
		t.Fatalf("expected reporting queueIntervalMs 250, got %d", got.Reporting.QueueIntervalMs)
	}
	if got.Reporting.QueueBatchLimit != 25 {
		t.Fatalf("expected reporting queueBatchLimit 25, got %d", got.Reporting.QueueBatchLimit)
	}
	if got.Reporting.Store.Backend != "sql" {
		t.Fatalf("expected reporting store backend sql, got %q", got.Reporting.Store.Backend)
	}
	if got.Reporting.Store.ConnectorRef != "agently" {
		t.Fatalf("expected reporting store connectorRef agently, got %q", got.Reporting.Store.ConnectorRef)
	}
	if !got.Reporting.BrowserRunPersistenceEnabled() {
		t.Fatalf("expected browser report-run persistence to be enabled through workspace defaults")
	}
	if !got.Reporting.ConversationAdoptionEnabled() {
		t.Fatalf("expected conversation adoption to be enabled through workspace defaults")
	}
	if got.Reporting.TransitionalWithUI.ExportFromRun != "disabled" {
		t.Fatalf("expected exportFromRun disabled, got %q", got.Reporting.TransitionalWithUI.ExportFromRun)
	}
	if got.Reporting.TransitionalWithUI.Orchestration != "disabled" {
		t.Fatalf("expected orchestration disabled, got %q", got.Reporting.TransitionalWithUI.Orchestration)
	}
	if got.Skills.Model != "openai_gpt-5.4-mini" {
		t.Fatalf("expected merged skills model, got %q", got.Skills.Model)
	}
	if got.PreviewSettings.Limit != 8000 {
		t.Fatalf("expected preview limit 8000, got %d", got.PreviewSettings.Limit)
	}
	if got.PreviewSettings.AgedLimit != 2500 {
		t.Fatalf("expected aged limit 2500, got %d", got.PreviewSettings.AgedLimit)
	}
	if got.PreviewSettings.AgedAfterSteps != 2 {
		t.Fatalf("expected agedAfterSteps 2, got %d", got.PreviewSettings.AgedAfterSteps)
	}
	if got.PreviewSettings.ToolResultLimit != 32000 {
		t.Fatalf("expected toolResultLimit 32000, got %d", got.PreviewSettings.ToolResultLimit)
	}
	if got.ToolCallMaxResults != 7 {
		t.Fatalf("expected ToolCallMaxResults 7, got %d", got.ToolCallMaxResults)
	}
	if got.ToolCallTimeoutSec != 45 {
		t.Fatalf("expected ToolCallTimeoutSec 45, got %d", got.ToolCallTimeoutSec)
	}
	if got.ElicitationTimeoutSec != 90 {
		t.Fatalf("expected ElicitationTimeoutSec 90, got %d", got.ElicitationTimeoutSec)
	}
}

func TestRootForgeReportingRoot(t *testing.T) {
	root := &Root{}
	if err := yaml.Unmarshal([]byte(`
forge:
  reporting:
    root: custom/forge/reports
`), root); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if got := root.ForgeReportingRoot(); got != "custom/forge/reports" {
		t.Fatalf("ForgeReportingRoot() = %q", got)
	}
	if got := (*Root)(nil).ForgeReportingRoot(); got != "" {
		t.Fatalf("nil ForgeReportingRoot() = %q", got)
	}
}

func TestRootAuthorizationTool(t *testing.T) {
	root := &Root{}
	if err := yaml.Unmarshal([]byte(`
ui:
  authorization:
    tool: myMcp
`), root); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if got := root.AuthorizationTool(); got != "myMcp" {
		t.Fatalf("AuthorizationTool() = %q", got)
	}
}

func TestRootPolicyAuthorization(t *testing.T) {
	root := &Root{}
	if err := yaml.Unmarshal([]byte(`
policy:
  authorization:
    mcpTool: myMcp:authorize
    ui: {}
    reports: {enabled: true}
    starterPrompt: {}
    intent: false
`), root); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if got := root.PolicyAuthorizationMCPTool(); got != "myMcp:authorize" {
		t.Fatalf("PolicyAuthorizationMCPTool() = %q", got)
	}
	if got := root.AuthorizationTool(); got != "" {
		t.Fatalf("AuthorizationTool() = %q", got)
	}
	for _, operation := range []string{"ui", "reports", "starterPrompt"} {
		if !root.PolicyAuthorizationEnabled(operation) {
			t.Fatalf("PolicyAuthorizationEnabled(%q) = false", operation)
		}
	}
	if root.PolicyAuthorizationEnabled("intent") {
		t.Fatal("PolicyAuthorizationEnabled(intent) = true")
	}
}

func TestRootPolicyAuthorizationStarterTaskAlias(t *testing.T) {
	root := &Root{}
	if err := yaml.Unmarshal([]byte("policy:\n  authorization:\n    mcpTool: myMcp:authorize\n    starterTask: {}\n"), root); err != nil {
		t.Fatal(err)
	}
	if !root.PolicyAuthorizationEnabled("starterPrompt") {
		t.Fatal("starterTask alias did not enable starterPrompt")
	}
}

func TestRootLegacyUIAuthorizationRemainsIndependent(t *testing.T) {
	root := &Root{}
	if err := yaml.Unmarshal([]byte(`
ui:
  authorization:
    tool: legacyMcp:resourceAuthorization
policy:
  authorization:
    mcpTool: myMcp:authorize
    ui: {}
`), root); err != nil {
		t.Fatal(err)
	}
	if got := root.AuthorizationTool(); got != "legacyMcp:resourceAuthorization" {
		t.Fatalf("AuthorizationTool() = %q", got)
	}
	if got := root.PolicyAuthorizationMCPTool(); got != "myMcp:authorize" {
		t.Fatalf("PolicyAuthorizationMCPTool() = %q", got)
	}
}

func TestGoalsEnabled(t *testing.T) {
	testCases := []struct {
		name     string
		yamlText string
		want     bool
	}{
		{
			name:     "missing features defaults true",
			yamlText: "default:\n  agent: chatter\n",
			want:     true,
		},
		{
			name: "explicit true",
			yamlText: `
features:
  goals:
    enabled: true
`,
			want: true,
		},
		{
			name: "explicit false",
			yamlText: `
features:
  goals:
    enabled: false
`,
			want: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := &Root{}
			if err := yaml.Unmarshal([]byte(testCase.yamlText), root); err != nil {
				t.Fatalf("yaml.Unmarshal() error = %v", err)
			}
			if got := root.GoalsEnabled(); got != testCase.want {
				t.Fatalf("GoalsEnabled() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestGoalWakeupsPolicy(t *testing.T) {
	root := &Root{}
	const yamlConfig = `
features:
  wakeups:
    enabled: false
    minWakeDelaySeconds: 300
    maxWakeDelaySeconds: 900
`
	if err := yaml.Unmarshal([]byte(yamlConfig), root); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}

	if got := root.GoalWakeupsEnabled(); got != false {
		t.Fatalf("GoalWakeupsEnabled() = %v, want false", got)
	}
	if got := root.GoalWakeupMinDelaySeconds(); got != 300 {
		t.Fatalf("GoalWakeupMinDelaySeconds() = %d, want 300", got)
	}
	if got := root.GoalWakeupMaxDelaySeconds(); got != 900 {
		t.Fatalf("GoalWakeupMaxDelaySeconds() = %d, want 900", got)
	}
	if got := root.GoalWakeupMaxGlobalWakeupsPerHour(); got != 5 {
		t.Fatalf("GoalWakeupMaxGlobalWakeupsPerHour() = %d, want 5", got)
	}
	if got := root.GoalWakeupMaxConversationWakeups(); got != 3 {
		t.Fatalf("GoalWakeupMaxConversationWakeups() = %d, want 3", got)
	}
	if got := root.GoalWakeupMaxGoalWakeups(); got != 2 {
		t.Fatalf("GoalWakeupMaxGoalWakeups() = %d, want 2", got)
	}
}

func TestGoalWakeupsPolicyDefaults(t *testing.T) {
	root := &Root{}
	if got := root.GoalWakeupsEnabled(); got != true {
		t.Fatalf("GoalWakeupsEnabled() = %v, want true", got)
	}
	if got := root.GoalWakeupMinDelaySeconds(); got != 60 {
		t.Fatalf("GoalWakeupMinDelaySeconds() = %d, want 60", got)
	}
	if got := root.GoalWakeupMaxDelaySeconds(); got != 3600 {
		t.Fatalf("GoalWakeupMaxDelaySeconds() = %d, want 3600", got)
	}
	if got := root.GoalWakeupMaxGlobalWakeupsPerHour(); got != 5 {
		t.Fatalf("GoalWakeupMaxGlobalWakeupsPerHour() = %d, want 5", got)
	}
	if got := root.GoalWakeupMaxConversationWakeups(); got != 3 {
		t.Fatalf("GoalWakeupMaxConversationWakeups() = %d, want 3", got)
	}
	if got := root.GoalWakeupMaxGoalWakeups(); got != 2 {
		t.Fatalf("GoalWakeupMaxGoalWakeups() = %d, want 2", got)
	}
}
