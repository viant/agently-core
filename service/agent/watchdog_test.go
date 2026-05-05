package agent

import (
	token "github.com/viant/agently-core/internal/auth/token"
	agrunstale "github.com/viant/agently-core/pkg/agently/run/stale"
	"testing"
)

func strptr(v string) *string { return &v }

func TestShouldSkipStaleRun(t *testing.T) {
	cases := []struct {
		name string
		run  *agrunstale.StaleRunsView
		want bool
	}{
		{name: "nil", run: nil, want: true},
		{name: "scheduled", run: &agrunstale.StaleRunsView{ConversationKind: "scheduled"}, want: true},
		{name: "resumed child", run: &agrunstale.StaleRunsView{ConversationKind: "interactive", ResumedFromRunId: strptr("old-run")}, want: false},
		{name: "interactive root", run: &agrunstale.StaleRunsView{ConversationKind: "interactive"}, want: false},
	}
	for _, tc := range cases {
		if got := shouldSkipStaleRun(tc.run); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestResolveResumeUserID(t *testing.T) {
	effective := "persisted-user"
	tests := []struct {
		name string
		run  *agrunstale.StaleRunsView
		sd   *token.SecurityData
		want string
	}{
		{
			name: "prefers restored security subject",
			run:  &agrunstale.StaleRunsView{EffectiveUserId: &effective},
			sd:   &token.SecurityData{Subject: "restored-user"},
			want: "restored-user",
		},
		{
			name: "falls back to persisted effective user",
			run:  &agrunstale.StaleRunsView{EffectiveUserId: &effective},
			sd:   nil,
			want: "persisted-user",
		},
		{
			name: "trims persisted effective user",
			run:  &agrunstale.StaleRunsView{EffectiveUserId: strptr("  persisted-user  ")},
			sd:   &token.SecurityData{},
			want: "persisted-user",
		},
		{
			name: "empty when neither source exists",
			run:  &agrunstale.StaleRunsView{},
			sd:   nil,
			want: "",
		},
	}
	for _, tc := range tests {
		if got := resolveResumeUserID(tc.run, tc.sd); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}
