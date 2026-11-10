package resource

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
)

func TestNativeMarkdownTemplateMatchesYAML(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "templates"), 0700))
	yaml := []byte("id: sample\nname: Sample\ninstructions: 'Write clearly.'\nschema: {type: object}\nfences: [{lang: json, required: true}]\nexamples: [{title: Ready, fences: [{lang: json, body: {status: ready}}]}]\n")
	md := []byte("---\nid: sample\nname: Sample\n---\n```template-schema\n{type: object}\n```\n```template-fences\n[{lang: json, required: true}]\n```\n```template-examples\n[{title: Ready, fences: [{lang: json, body: {status: ready}}]}]\n```\nWrite clearly.")
	compile := func(name string, raw []byte) json.RawMessage {
		require.NoError(t, os.WriteFile(filepath.Join(root, "templates", name), raw, 0600))
		c := NewAuthoredCompilation("templates")
		defs, err := SnapshotExtensionDefinitions(ctx, root, []ExtensionDirectory{c.TemplateDirectory("workspace")})
		require.NoError(t, err)
		require.Len(t, defs, 1)
		snapshot, err := NewNativeAssetSnapshot(ctx, root, defs, NativeSnapshotOptions{BeforeCompile: c.Prepare})
		require.NoError(t, err)
		require.Empty(t, snapshot.CompilationErrors())
		uri, err := identity.ParseResourceURI("template://workspace/sample")
		require.NoError(t, err)
		candidates, err := snapshot.Candidates(ctx, uri)
		require.NoError(t, err)
		require.Len(t, candidates, 1)
		encoded, err := snapshot.ReadCandidate(ctx, uri, candidates[0])
		require.NoError(t, err)
		compiledCount := snapshot.CompileCount()
		again, err := snapshot.ReadCandidate(ctx, uri, candidates[0])
		require.NoError(t, err)
		require.Equal(t, encoded, again)
		require.Equal(t, compiledCount, snapshot.CompileCount(), "warm read must reuse native compilation")
		require.NoError(t, os.Remove(filepath.Join(root, "templates", name)))
		return encoded
	}
	// Compare payloads after excluding source-specific resource identifiers.
	first := compile("sample.yaml", yaml)
	second := compile("sample.md", md)
	require.JSONEq(t, string(first), string(second))
}

func TestTemplatePinnedForgeSchemas(t *testing.T) {
	for _, ref := range []string{"forge:report-spec", "forge:window"} {
		value, err := DecodeTemplateDefinition(json.RawMessage(`{"id":"sample","name":"Sample","schemaRef":"` + ref + `"}`))
		require.NoError(t, err)
		require.Empty(t, value.SchemaRef)
		require.NotEmpty(t, value.Schema)
		encoded, err := json.Marshal(value.Schema)
		require.NoError(t, err)
		require.Contains(t, string(encoded), `"$defs"`)
	}
	_, err := DecodeTemplateDefinition(json.RawMessage(`{"id":"sample","name":"Sample","schemaRef":"https://example.invalid/schema"}`))
	require.ErrorContains(t, err, "unsupported")
	_, err = DecodeTemplateDefinition(json.RawMessage(`{"id":"sample","name":"Sample","schemaRef":"forge:window","schema":{}}`))
	require.ErrorContains(t, err, "mutually exclusive")
}

func TestTemplateSchemaMacrosUseNativeExpansionOutsideCode(t *testing.T) {
	raw := json.RawMessage("{\"id\":\"sample\",\"name\":\"Sample\",\"instructions\":\"Report: ${ReportSchema}\\nWindow: ${WindowSchema}\\nUnknown: ${Other}\\n`${ReportSchema}`\\n```json\\n${WindowSchema}\\n```\"}")
	actual, err := DecodeTemplateDefinition(raw)
	require.NoError(t, err)
	require.Contains(t, actual.Instructions, "Unknown: ${Other}")
	require.Contains(t, actual.Instructions, "`${ReportSchema}`")
	require.Contains(t, actual.Instructions, "```json\n${WindowSchema}\n```")
	require.Contains(t, actual.Instructions, `"const":"reportSpec"`)
	require.Contains(t, actual.Instructions, `"$ref":"#/$defs/Window"`)
	encoded, err := json.Marshal(actual)
	require.NoError(t, err)
	again, err := DecodeTemplateDefinition(encoded)
	require.NoError(t, err)
	require.Equal(t, actual.Instructions, again.Instructions, "prepared instructions must not expand again")
	require.Nil(t, actual.Schema, "prose macros do not opt into whole-response grammar")
}
