package llm_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/viant/agently-core/genai/llm"
	"gopkg.in/yaml.v3"
)

func TestModelPreferences_UnmarshalYAML(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		want    llm.ModelPreferences
		wantErr bool
	}{
		{
			name: "string-list hints",
			yaml: `
intelligencePriority: 0.2
speedPriority: 0.9
costPriority: 0.9
hints:
  - claude-haiku
  - gpt-5-mini
`,
			want: llm.ModelPreferences{
				IntelligencePriority: 0.2,
				SpeedPriority:        0.9,
				CostPriority:         0.9,
				Hints:                []string{"claude-haiku", "gpt-5-mini"},
			},
		},
		{
			name: "mcp-style object hints",
			yaml: `
intelligencePriority: 0.9
hints:
  - {name: claude-opus}
  - {name: claude-sonnet}
`,
			want: llm.ModelPreferences{
				IntelligencePriority: 0.9,
				Hints:                []string{"claude-opus", "claude-sonnet"},
			},
		},
		{
			name: "mixed shapes",
			yaml: `
hints:
  - claude-haiku
  - {name: gpt-5-mini}
  - sonnet
`,
			want: llm.ModelPreferences{
				Hints: []string{"claude-haiku", "gpt-5-mini", "sonnet"},
			},
		},
		{
			name: "empty entries dropped",
			yaml: `
hints:
  - claude-haiku
  - ""
  - {name: ""}
  - "  "
`,
			want: llm.ModelPreferences{
				Hints: []string{"claude-haiku"},
			},
		},
		{
			name: "no hints at all",
			yaml: `
intelligencePriority: 0.5
`,
			want: llm.ModelPreferences{
				IntelligencePriority: 0.5,
				Hints:                nil,
			},
		},
		{
			name: "block-style mcp object",
			yaml: `
hints:
  - name: claude-haiku
  - name: gpt-5-mini
`,
			want: llm.ModelPreferences{
				Hints: []string{"claude-haiku", "gpt-5-mini"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got llm.ModelPreferences
			err := yaml.Unmarshal([]byte(tc.yaml), &got)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got none; result=%+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("mismatch\nwant=%+v\ngot =%+v", tc.want, got)
			}
		})
	}
}

func TestModelPreferences_UnmarshalJSON(t *testing.T) {
	cases := []struct {
		name string
		data string
		want llm.ModelPreferences
	}{
		{
			name: "string-list hints",
			data: `{"intelligencePriority":0.2,"speedPriority":0.9,"hints":["claude-haiku","gpt-5-mini"]}`,
			want: llm.ModelPreferences{
				IntelligencePriority: 0.2,
				SpeedPriority:        0.9,
				Hints:                []string{"claude-haiku", "gpt-5-mini"},
			},
		},
		{
			name: "mcp-style object hints",
			data: `{"intelligencePriority":0.9,"hints":[{"name":"claude-opus"},{"name":"claude-sonnet"}]}`,
			want: llm.ModelPreferences{
				IntelligencePriority: 0.9,
				Hints:                []string{"claude-opus", "claude-sonnet"},
			},
		},
		{
			name: "mixed shapes",
			data: `{"hints":["claude-haiku",{"name":"gpt-5-mini"},"sonnet"]}`,
			want: llm.ModelPreferences{
				Hints: []string{"claude-haiku", "gpt-5-mini", "sonnet"},
			},
		},
		{
			name: "empty entries dropped",
			data: `{"hints":["claude-haiku","",{"name":""},"  "]}`,
			want: llm.ModelPreferences{
				Hints: []string{"claude-haiku"},
			},
		},
		{
			name: "no hints",
			data: `{"costPriority":0.7}`,
			want: llm.ModelPreferences{
				CostPriority: 0.7,
				Hints:        nil,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got llm.ModelPreferences
			if err := json.Unmarshal([]byte(tc.data), &got); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("mismatch\nwant=%+v\ngot =%+v", tc.want, got)
			}
		})
	}
}

// TestModelPreferences_RoundTripYAML asserts that marshalling a populated
// ModelPreferences and re-unmarshalling produces the same value. This proves
// the default Marshal output (string-list form) parses cleanly back through
// the custom UnmarshalYAML.
func TestModelPreferences_RoundTripYAML(t *testing.T) {
	original := llm.ModelPreferences{
		IntelligencePriority: 0.3,
		SpeedPriority:        0.8,
		CostPriority:         0.9,
		Hints:                []string{"claude-haiku", "gpt-5-mini"},
	}
	out, err := yaml.Marshal(&original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundtripped llm.ModelPreferences
	if err := yaml.Unmarshal(out, &roundtripped); err != nil {
		t.Fatalf("unmarshal: %v\nyaml output:\n%s", err, string(out))
	}
	if !reflect.DeepEqual(original, roundtripped) {
		t.Fatalf("round-trip mismatch\norig=%+v\ngot =%+v\nyaml=\n%s", original, roundtripped, string(out))
	}
}

// TestFromEffort_DeprecationMapping locks in the legacy-to-MCP migration
// table for the deprecated `effort:` skill frontmatter key. Skill parsers
// call FromEffort when they see the bare key and route the resulting
// preferences through the same Matcher path as metadata.model-preferences,
// emitting a warn diagnostic separately. This test prevents drift in the
// canonical mapping.
func TestFromEffort_DeprecationMapping(t *testing.T) {
	cases := []struct {
		input string
		want  *llm.ModelPreferences
	}{
		{"low", &llm.ModelPreferences{IntelligencePriority: 0.2, SpeedPriority: 0.8}},
		{"LOW", &llm.ModelPreferences{IntelligencePriority: 0.2, SpeedPriority: 0.8}},
		{" low ", &llm.ModelPreferences{IntelligencePriority: 0.2, SpeedPriority: 0.8}},
		{"medium", &llm.ModelPreferences{IntelligencePriority: 0.5, SpeedPriority: 0.5}},
		{"med", &llm.ModelPreferences{IntelligencePriority: 0.5, SpeedPriority: 0.5}},
		{"high", &llm.ModelPreferences{IntelligencePriority: 0.9, SpeedPriority: 0.2}},
		{"", nil},
		{"  ", nil},
		{"unknown", nil},
		{"highest", nil}, // not "high" — no fuzzy match
	}
	for _, tc := range cases {
		t.Run("FromEffort("+tc.input+")", func(t *testing.T) {
			got := llm.FromEffort(tc.input)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected %+v, got nil", tc.want)
			}
			if got.IntelligencePriority != tc.want.IntelligencePriority {
				t.Errorf("IntelligencePriority: want %v got %v", tc.want.IntelligencePriority, got.IntelligencePriority)
			}
			if got.SpeedPriority != tc.want.SpeedPriority {
				t.Errorf("SpeedPriority: want %v got %v", tc.want.SpeedPriority, got.SpeedPriority)
			}
		})
	}
}

// TestModelPreferences_RoundTripJSON same as above for JSON.
func TestModelPreferences_RoundTripJSON(t *testing.T) {
	original := llm.ModelPreferences{
		IntelligencePriority: 0.3,
		SpeedPriority:        0.8,
		CostPriority:         0.9,
		Hints:                []string{"claude-haiku", "gpt-5-mini"},
	}
	out, err := json.Marshal(&original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundtripped llm.ModelPreferences
	if err := json.Unmarshal(out, &roundtripped); err != nil {
		t.Fatalf("unmarshal: %v\njson output: %s", err, string(out))
	}
	if !reflect.DeepEqual(original, roundtripped) {
		t.Fatalf("round-trip mismatch\norig=%+v\ngot =%+v\njson=%s", original, roundtripped, string(out))
	}
}
