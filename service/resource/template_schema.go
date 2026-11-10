package resource

import (
	"fmt"
	"github.com/viant/agently-core/internal/templating"
	"strings"

	tpl "github.com/viant/agently-core/protocol/template"
	"github.com/viant/forge/backend/authoring/markdown"
	forgeschema "github.com/viant/forge/backend/authoring/schema"
)

// resolveTemplateSchemas runs inside the existing immutable definition decoder
// or startup snapshot compiler. References bind to this binary's pinned Forge,
// never a remote document or mutable catalog lookup.
func resolveTemplateSchemas(t *tpl.Template) error {
	schemas := map[string]map[string]any{}
	compile := func(ref string) (map[string]any, error) {
		if schema, ok := schemas[ref]; ok {
			return schema, nil
		}
		schema, err := forgeschema.Resolve(ref)
		if err != nil {
			return nil, err
		}
		schemas[ref] = schema
		return schema, nil
	}
	resolve := func(ref *string, schema *map[string]any) error {
		if *ref == "" {
			return nil
		}
		if *schema != nil {
			return fmt.Errorf("template schema and schemaRef are mutually exclusive")
		}
		compiled, err := compile(*ref)
		if err != nil {
			return fmt.Errorf("resolve template schemaRef %q: %w", *ref, err)
		}
		*schema = compiled
		*ref = ""
		return nil
	}
	if err := resolve(&t.SchemaRef, &t.Schema); err != nil {
		return err
	}
	for i := range t.Fences {
		if err := resolve(&t.Fences[i].SchemaRef, &t.Fences[i].Schema); err != nil {
			return fmt.Errorf("template fence %d: %w", i, err)
		}
	}

	// Resolve only the two declared schema macros. Other runtime placeholders
	// remain untouched, and inserted schema bytes are never interpreted again.
	expanded := map[string]string{}
	text, err := markdown.TransformText(t.Instructions, func(prose string) (string, error) {
		replacements := []string{}
		for _, entry := range []struct{ name, ref string }{{"ReportSchema", "forge:report-spec"}, {"WindowSchema", "forge:window"}} {
			token := "${" + entry.name + "}"
			if !strings.Contains(prose, token) {
				continue
			}
			value, ok := expanded[token]
			if !ok {
				raw, err := forgeschema.Compact(entry.ref)
				if err != nil {
					return "", err
				}
				value, err = templating.Expand(token, map[string]interface{}{entry.name: raw})
				if err != nil {
					return "", err
				}
				expanded[token] = value
			}
			replacements = append(replacements, token, value)
		}
		return strings.NewReplacer(replacements...).Replace(prose), nil
	})
	if err != nil {
		return fmt.Errorf("expand template schema instructions: %w", err)
	}
	t.Instructions = text
	return nil
}
