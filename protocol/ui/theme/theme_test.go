package theme

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

const example = `version: 1
defaultTheme: branded
themes:
  - id: branded
    label: Branded
    fallbackMode: light
    tokens:
      control.radius: 8
    modes:
      light: {}
      dark:
        tokens:
          control.background: '#123456'
`

func TestResolveAndIsolation(t *testing.T) {
	m, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	c, err := Resolve(*m)
	if err != nil {
		t.Fatal(err)
	}
	theme := c.Themes[0]
	if c.DefaultMode != "system" || theme.Modes["dark"]["control.radius"] != 8 || theme.Modes["dark"]["control.background"] != "#123456" {
		t.Fatalf("unexpected catalog: %+v", c)
	}
	theme.Modes["dark"]["control.radius"] = 99
	if theme.Modes["light"]["control.radius"] != 8 || Defaults("dark")["control.radius"] != 4 {
		t.Fatal("palettes share mutable maps")
	}
	for _, test := range []struct{ preference, system, want string }{{"system", "dark", "dark"}, {"light", "dark", "light"}, {"bad", "dark", "light"}} {
		if got := theme.EffectiveMode(test.preference, test.system); got != test.want {
			t.Fatalf("mode %q != %q", got, test.want)
		}
	}
	delete(theme.Modes, "dark")
	if theme.EffectiveMode("system", "dark") != "light" {
		t.Fatal("single-mode fallback failed")
	}
}
func TestInvalidManifest(t *testing.T) {
	for _, input := range []string{
		strings.Replace(example, "version: 1", "version: 2", 1),
		example + "unknown: value\n", example + "---\nversion: 1\n", example + "version: 1\n",
		strings.Replace(example, "fallbackMode: light", "fallbackMode: other", 1),
		strings.Replace(example, "defaultTheme: branded", "defaultTheme: absent", 1),
		strings.ReplaceAll(example, "branded", "forge-default"),
		strings.Replace(example, "control.radius: 8", "control.radius: 8px", 1),
		strings.Replace(example, "control.radius: 8", "control.radius: -1", 1),
		strings.Replace(example, "control.radius: 8", "unknown.token: 8", 1),
		strings.Replace(example, "'#123456'", "'red; } body { display:none'", 1),
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("accepted invalid manifest: %s", input)
		}
	}
	if _, err := Parse([]byte(strings.Repeat(" ", MaxManifestBytes+1))); err == nil {
		t.Fatal("accepted oversized manifest")
	}
	if err := validateTokens(Tokens{"control.radius": math.NaN()}); err == nil {
		t.Fatal("accepted NaN")
	}
}
func TestCSSAndJSON(t *testing.T) {
	m, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	c, err := Resolve(*m)
	if err != nil {
		t.Fatal(err)
	}
	css, err := CSS(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(css, "--forge-control-radius: 8px;") ||
		!strings.Contains(css, "--agently-theme-control-radius: 8px;") ||
		!strings.Contains(css, `data-forge-color-mode="dark"`) ||
		!strings.Contains(css, `data-agently-color-mode="dark"`) {
		t.Fatal(css)
	}
	applicationStart := strings.Index(css, ".agently-application")
	if applicationStart < 0 {
		t.Fatal("missing application theme scope")
	}
	applicationEnd := applicationStart + strings.Index(css[applicationStart:], "}\n")
	if applicationEnd < applicationStart || strings.Contains(css[applicationStart:applicationEnd], "color-scheme") {
		t.Fatal("application variables must not restyle browser controls before workspace opt-in")
	}
	bytes, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Catalog
	if err = json.Unmarshal(bytes, &decoded); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := CSS(&decoded)
	if err != nil {
		t.Fatal(err)
	}
	if css != roundTrip {
		t.Fatal("JSON round trip changes generated CSS")
	}
	decoded.Themes[0].ID = `x"] body`
	if _, err = CSS(&decoded); err == nil {
		t.Fatal("accepted selector injection")
	}
}
func TestCSSOnlyManifest(t *testing.T) {
	m, err := Parse([]byte("version: 1\nfiles: [forms.css]\n"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := Resolve(*m)
	if err != nil {
		t.Fatal(err)
	}
	css, err := CSS(c)
	if err != nil || css != "" {
		t.Fatalf("CSS-only theme resolution: %q %v", css, err)
	}
}

func TestSharedFixture(t *testing.T) {
	source, err := os.ReadFile("testdata/baseline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Resolve(*m)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("testdata/baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(expected), actual) {
		t.Fatal("shared native fixture is stale")
	}
}

func TestSharedCSSFixture(t *testing.T) {
	source, err := os.ReadFile("testdata/baseline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Resolve(*m)
	if err != nil {
		t.Fatal(err)
	}
	css, err := CSS(c)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("testdata/baseline.css")
	if err != nil {
		t.Fatal(err)
	}
	if string(expected) != css {
		t.Fatal("shared browser fixture is stale")
	}
}
