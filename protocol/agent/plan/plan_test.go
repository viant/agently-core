package plan

import (
	"testing"

	mcpproto "github.com/viant/mcp-protocol/schema"
)

func TestPlanIsEmpty(t *testing.T) {
	tests := []struct {
		name string
		plan *Plan
		want bool
	}{
		{
			name: "nil plan",
			plan: nil,
			want: true,
		},
		{
			name: "elicitation only is not empty",
			plan: &Plan{
				Elicitation: &Elicitation{
					ElicitRequestParams: mcpproto.ElicitRequestParams{
						Message: "Need more information",
					},
				},
			},
			want: false,
		},
		{
			name: "blank step plus tool step is not empty",
			plan: &Plan{
				Steps: Steps{
					{Type: "noop"},
					{Type: "tool", Name: "system/exec:execute"},
				},
			},
			want: false,
		},
		{
			name: "all blank steps are empty",
			plan: &Plan{
				Steps: Steps{
					{},
					{Type: "noop"},
				},
			},
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.plan.IsEmpty(); got != test.want {
				t.Fatalf("IsEmpty() = %v, want %v", got, test.want)
			}
		})
	}
}
