package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDefaultsUnmarshalYAMLProjection(t *testing.T) {
	input := `
projection:
  relevance:
    enabled: true
    protectedRecentTurns: 1
    tokenThreshold: 42
    model: gpt-5-mini
    prompt:
      text: select relevant turns
      engine: go
  toolCallSupersession:
    enabled: false
    limit:
      history: 3
      turn: 4
`
	var got Defaults
	require.NoError(t, yaml.Unmarshal([]byte(input), &got))
	require.NotNil(t, got.Projection.Relevance)
	require.NotNil(t, got.Projection.Relevance.Enabled)
	require.True(t, *got.Projection.Relevance.Enabled)
	require.NotNil(t, got.Projection.Relevance.ProtectedRecentTurns)
	require.Equal(t, 1, *got.Projection.Relevance.ProtectedRecentTurns)
	require.NotNil(t, got.Projection.Relevance.TokenThreshold)
	require.Equal(t, 42, *got.Projection.Relevance.TokenThreshold)
	require.NotNil(t, got.Projection.Relevance.Model)
	require.Equal(t, "gpt-5-mini", *got.Projection.Relevance.Model)
	require.NotNil(t, got.Projection.Relevance.Prompt)
	require.Equal(t, "select relevant turns", got.Projection.Relevance.Prompt.Text)
	require.Equal(t, "go", got.Projection.Relevance.Prompt.Engine)
	require.NotNil(t, got.Projection.ToolCallSupersession)
	require.NotNil(t, got.Projection.ToolCallSupersession.Enabled)
	require.False(t, *got.Projection.ToolCallSupersession.Enabled)
	require.Equal(t, 3, got.Projection.SupersessionHistoryLimit())
	require.Equal(t, 4, got.Projection.SupersessionTurnLimit())
}

func TestDefaultsUnmarshalYAMLLegacyCompactionAlias(t *testing.T) {
	input := `
compaction:
  toolCallSupersession:
    enabled: false
    limit:
      history: 5
      turn: 6
`
	var got Defaults
	require.NoError(t, yaml.Unmarshal([]byte(input), &got))
	require.NotNil(t, got.Projection.ToolCallSupersession)
	require.NotNil(t, got.Projection.ToolCallSupersession.Enabled)
	require.False(t, *got.Projection.ToolCallSupersession.Enabled)
	require.Equal(t, 5, got.Projection.SupersessionHistoryLimit())
	require.Equal(t, 6, got.Projection.SupersessionTurnLimit())
}

func TestRelevanceProjection_Defaults(t *testing.T) {
	var relevance *RelevanceProjection
	require.True(t, relevance.IsEnabled())
	require.Equal(t, 1, relevance.ProtectedTurns())
	require.Equal(t, 20000, relevance.Threshold())
	require.Equal(t, 0, relevance.Chunk())
	require.Equal(t, 1, relevance.Concurrency())

	relevance = &RelevanceProjection{}
	require.True(t, relevance.IsEnabled())
	require.Equal(t, 1, relevance.ProtectedTurns())
	require.Equal(t, 20000, relevance.Threshold())
}

func TestDefaultsUnmarshalYAMLReporting(t *testing.T) {
	input := `
reporting:
  enabled: true
  queueIntervalMs: 250
  queueBatchLimit: 3
  store:
    backend: sql
    connectorRef: agently
`
	var got Defaults
	require.NoError(t, yaml.Unmarshal([]byte(input), &got))
	require.True(t, got.Reporting.Enabled)
	require.Equal(t, 250, got.Reporting.QueueIntervalMs)
	require.Equal(t, 3, got.Reporting.QueueBatchLimit)
	require.Equal(t, "sql", got.Reporting.Store.Backend)
	require.Equal(t, "agently", got.Reporting.Store.ConnectorRef)
}

func TestReportingBrowserRunPersistence_DefaultClosedAndExplicitlyEnabled(t *testing.T) {
	var defaults Defaults
	require.False(t, defaults.Reporting.BrowserRunPersistenceEnabled())
	require.False(t, defaults.Reporting.ConversationAdoptionEnabled())
	require.False(t, defaults.Reporting.ExportFromRunEnabled())
	require.False(t, defaults.Reporting.OrchestrationEnabled())
	require.NoError(t, defaults.Reporting.ValidateOrchestrationPrerequisites())

	input := `
reporting:
  enabled: true
  transitionalWithUI:
    admission: open
    persistence: enabled
    exportFromRun: disabled
    orchestration: disabled
    conversationAdoption: enabled
`
	require.NoError(t, yaml.Unmarshal([]byte(input), &defaults))
	require.True(t, defaults.Reporting.BrowserRunPersistenceEnabled())
	require.True(t, defaults.Reporting.ConversationAdoptionEnabled())
	require.False(t, defaults.Reporting.ExportFromRunEnabled())
	require.False(t, defaults.Reporting.OrchestrationEnabled())
	require.NoError(t, defaults.Reporting.ValidateOrchestrationPrerequisites())
	require.Equal(t, "disabled", defaults.Reporting.TransitionalWithUI.ExportFromRun)
	require.Equal(t, "disabled", defaults.Reporting.TransitionalWithUI.Orchestration)

	defaults.Reporting.TransitionalWithUI.ExportFromRun = "enabled"
	require.True(t, defaults.Reporting.ExportFromRunEnabled())
	defaults.Reporting.TransitionalWithUI.Admission = "closed"
	require.False(t, defaults.Reporting.BrowserRunPersistenceEnabled())
	require.False(t, defaults.Reporting.ConversationAdoptionEnabled())
	require.False(t, defaults.Reporting.ExportFromRunEnabled())
	require.False(t, defaults.Reporting.OrchestrationEnabled())
}

func TestReportingOrchestration_FailClosedPrerequisites(t *testing.T) {
	enabled := ReportingDefaults{
		Enabled: true,
		TransitionalWithUI: ReportingTransitionalWithUIDefaults{
			Admission:            "open",
			Persistence:          "enabled",
			ExportFromRun:        "enabled",
			Orchestration:        "enabled",
			ConversationAdoption: "disabled",
		},
	}
	require.True(t, enabled.OrchestrationEnabled())
	require.NoError(t, enabled.ValidateOrchestrationPrerequisites())

	for name, mutate := range map[string]func(*ReportingDefaults){
		"reporting disabled": func(value *ReportingDefaults) {
			value.Enabled = false
		},
		"admission closed": func(value *ReportingDefaults) {
			value.TransitionalWithUI.Admission = "closed"
		},
		"persistence disabled": func(value *ReportingDefaults) {
			value.TransitionalWithUI.Persistence = "disabled"
		},
		"run export disabled": func(value *ReportingDefaults) {
			value.TransitionalWithUI.ExportFromRun = "disabled"
		},
	} {
		t.Run(name, func(t *testing.T) {
			actual := enabled
			mutate(&actual)
			require.False(t, actual.OrchestrationEnabled())
			err := actual.ValidateOrchestrationPrerequisites()
			require.Error(t, err)
			require.Contains(t, strings.ToLower(err.Error()), "requires")
		})
	}
}

func TestReportingOrchestration_DisabledAndUnknownPreserveT2Behavior(t *testing.T) {
	for _, flag := range []string{"", "disabled", "typo"} {
		actual := ReportingDefaults{
			TransitionalWithUI: ReportingTransitionalWithUIDefaults{
				Orchestration: flag,
			},
		}
		require.False(t, actual.OrchestrationEnabled(), flag)
		require.NoError(t, actual.ValidateOrchestrationPrerequisites(), flag)
	}
}
