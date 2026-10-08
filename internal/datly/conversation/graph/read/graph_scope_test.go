package read

import (
	"context"
	"strings"
	"testing"
)

func TestGraphScopeRequiresTrustedBoundedNonemptyPredicate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input *Input
		valid bool
	}{
		{"nil", nil, false},
		{"untrusted", &Input{IDs: []string{"root"}, Has: &InputHas{IDs: true}}, false},
		{"unbounded", &Input{Trusted: true, Has: &InputHas{}}, false},
		{"missing_marker", &Input{Trusted: true, IDs: []string{"root"}}, false},
		{"empty_ids", &Input{Trusted: true, Has: &InputHas{IDs: true}}, false},
		{"blank_ids", &Input{Trusted: true, IDs: []string{" "}, Has: &InputHas{IDs: true}}, false},
		{"empty_second_predicate", &Input{Trusted: true, IDs: []string{"root"}, Has: &InputHas{IDs: true, ParentIDs: true}}, false},
		{"ids", &Input{Trusted: true, IDs: []string{"root"}, Has: &InputHas{IDs: true}}, true},
		{"parents", &Input{Trusted: true, ParentIDs: []string{"root"}, Has: &InputHas{ParentIDs: true}}, true},
		{"turn_parents", &Input{Trusted: true, ParentTurnIDs: []string{"turn"}, Has: &InputHas{ParentTurnIDs: true}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.input.Init(context.Background()); (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
		})
	}
}

func TestGeneratedGraphSQLContainsNoContentColumns(t *testing.T) {
	query, err := ReaderDatlyResources.ReadFile("sql/reader.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(query))
	for _, forbidden := range []string{"c.*", " join ", "message", "inline_body", "report_spec", " limit "} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("unexpected graph SQL: %s", forbidden)
		}
	}
	for _, field := range []string{"c.id", "c.created_by_user_id", "c.status", "c.schedule_run_id", "cast(c.created_at as char)"} {
		if !strings.Contains(sql, field) {
			t.Fatalf("missing graph SQL field: %s", field)
		}
	}
}
