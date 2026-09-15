package style

import "testing"

func TestCSSResourceAndSyntaxValidation(t *testing.T) {
	for _, source := range []string{
		`@import "other.css";`, `@\69mport "other.css";`, `@IMPORT url(other.css);`,
		`.agently-workspace {background:url(image.png)}`, `.agently-workspace {background: url("https://example.com/image")}`,
		`.agently-workspace {background:\75rl(image.png)}`, `.agently-workspace {background:image-set("image.png" 1x)}`,
		`.agently-workspace {background:\69mage-set("image.png" 1x)}`, `.agently-workspace {color:red`,
		`.agently-workspace {color:rgb(0,0,0}`, `.agently-workspace {color: "unterminated}`,
	} {
		if _, err := validateCSS([]byte(source), "", ""); err == nil {
			t.Errorf("accepted invalid CSS: %s", source)
		}
	}
	for _, source := range []string{
		`.agently-workspace {--label: "url(not-a-reference)"; color:rgb(1,2,3)}`,
		`@media (min-width: 400px) {.agently-workspace {color:red}}`,
		`.agently-workspace[data-forge-theme="branded"][data-forge-color-mode="dark"] .control {border: 1px solid var(--forge-control-border)}`,
	} {
		if _, err := validateCSS([]byte(source), "", ""); err != nil {
			t.Errorf("rejected valid CSS: %s: %v", source, err)
		}
	}
}
func TestScopeDiagnostics(t *testing.T) {
	warnings, err := validateCSS([]byte(`body {color: red}`), "branded", "dark")
	if err != nil || len(warnings) != 3 {
		t.Fatalf("expected scope diagnostics: %v %v", warnings, err)
	}
	warnings, err = validateCSS([]byte(`.agently-workspace[data-forge-theme="branded"][data-forge-color-mode="dark"] input {color: red}`), "branded", "dark")
	if err != nil || len(warnings) != 0 {
		t.Fatalf("valid scoped CSS: %v %v", warnings, err)
	}
	warnings, err = validateCSS([]byte(`.agently-application[data-agently-theme="branded"][data-agently-color-mode="dark"] .app-shell {color: red}`), "branded", "dark")
	if err != nil || len(warnings) != 0 {
		t.Fatalf("valid application-scoped CSS: %v %v", warnings, err)
	}
}
