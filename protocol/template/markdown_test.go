package template

import (
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"testing"
)

func TestMarkdownContractAndLiteralExamples(t *testing.T) {
	raw := []byte("---\nid: sample\nname: Sample\n---\nWrite clearly.\n```json\n{\"schema\":\"literal\"}\n```\n````text\n```template-schema\nnot metadata\n```\n````\n```template-schema\n{\"type\":\"object\"}\n```\n```template-fences\n- lang: json\n  required: true\n```\n```template-examples\n- title: Ready\n  fences:\n  - lang: json\n    body: {status: ready}\n```\n")
	compiled, body, err := MarkdownSource(raw)
	require.NoError(t, err)
	var actual Template
	require.NoError(t, yaml.Unmarshal(compiled, &actual))
	require.Contains(t, body, "```template-schema\nnot metadata")
	require.Contains(t, body, "{\"schema\":\"literal\"}")
	require.Equal(t, map[string]any{"type": "object"}, actual.Schema)
	require.Equal(t, []Fence{{Lang: "json", Required: true}}, actual.Fences)
	require.Equal(t, "Ready", actual.Examples[0].Title)
	require.Equal(t, body, actual.Instructions)
	require.NotContains(t, body, "status: ready")
}
func TestMarkdownRejectsConflictingAndBrokenContracts(t *testing.T) {
	for _, raw := range []string{
		"---\nid: sample\ninstructions: old\n---\nnew",
		"---\nschema: {type: object}\n---\n```template-schema\n{}\n```",
		"```template-examples\n[]",
		"```template-schema\n[\n```",
	} {
		_, _, err := MarkdownSource([]byte(raw))
		require.Error(t, err)
	}
}
