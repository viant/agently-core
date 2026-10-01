package theme

import (
	"strings"
	"testing"
)

func TestOptionalSemanticRoles(t *testing.T) {
	values := Defaults("light")
	values["typography.body.size"] = 14
	values["typography.body.lineHeight"] = 20
	values["interaction.selectedBackground"] = "#e6f5fc"
	values["data.categorical.1"] = "#315caa"
	catalog := &Catalog{Version: Version, Themes: []ResolvedTheme{{ID: "workspace", Modes: map[string]Tokens{"light": values}}}}
	css, err := CSS(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"--forge-type-body-line-height: 20px", "--agently-theme-type-body-size: 14px", "--forge-selected-background: #e6f5fc", "--agently-theme-data-categorical-1: #315caa"} {
		if !strings.Contains(css, token) {
			t.Fatalf("missing %s", token)
		}
	}
	values["typography.body.lineHeight"] = -1
	if _, err = CSS(catalog); err == nil {
		t.Fatal("negative line height accepted")
	}
}
