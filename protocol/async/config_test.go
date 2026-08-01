package async

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestConfig_TerminalCarrierBeforeModelIsOptIn(t *testing.T) {
	var defaultJSON Config
	require.NoError(t, json.Unmarshal([]byte(`{"run":{"tool":"run","operationIdPath":"id"},"status":{"tool":"status","operationIdArg":"id","selector":{"statusPath":"status"}}}`), &defaultJSON))
	require.False(t, defaultJSON.TerminalCarrierBeforeModel)

	enabled := Config{TerminalCarrierBeforeModel: true}
	jsonData, err := json.Marshal(enabled)
	require.NoError(t, err)
	require.Contains(t, string(jsonData), `"terminalCarrierBeforeModel":true`)

	yamlData, err := yaml.Marshal(enabled)
	require.NoError(t, err)
	require.Contains(t, string(yamlData), "terminalCarrierBeforeModel: true")
}
