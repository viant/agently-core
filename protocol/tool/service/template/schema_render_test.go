package template

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	tpl "github.com/viant/agently-core/protocol/template"
	forgeschema "github.com/viant/forge/backend/authoring/schema"
	"strings"
	"testing"
)

func TestCanonicalFenceSchemaUsesCompactJSON(t *testing.T) {
	for _, ref := range []string{"forge:report-spec", "forge:window"} {
		schema, err := forgeschema.Resolve(ref)
		require.NoError(t, err)
		expected, err := json.Marshal(schema)
		require.NoError(t, err)
		rendered := RenderTemplateDocument(&tpl.Template{Instructions: "Generate this format.", Fences: []tpl.Fence{{Lang: "json", Schema: schema}}})
		require.Contains(t, rendered, string(expected))
		require.False(t, strings.Contains(string(expected), "\n"))
	}
}
