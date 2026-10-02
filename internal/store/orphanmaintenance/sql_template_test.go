package orphanmaintenance

import (
	"os"
	"strings"
	"testing"

	"github.com/viant/velty"
)

func TestOrphanTemplateSelectsOnlyExactRuleAndRecord(t *testing.T) {
	generated, err := os.ReadFile("../../datly/orphanmaintenance/read/sql/reader.sql")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("../../../dql/orphanmaintenance/read/sql/orphanmaintenance.sql")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(generated), "SELECT '' AS rule_id")
	end := strings.Index(string(generated), ")  data_rows WHERE")
	if start < 0 || end < start || strings.TrimSpace(string(generated[start:end])) != strings.TrimSpace(string(source)) {
		t.Fatal("source and generated orphan predicates drifted")
	}
	planner := velty.New()
	for name, value := range map[string]any{"RuleID": "", "RecordID": "", "OlderThan": "2026-01-01", "MySQLContract": false} {
		if err := planner.DefineVariable(name, value); err != nil {
			t.Fatal(err)
		}
	}
	execution, newState, err := planner.Compile(source)
	if err != nil {
		t.Fatal(err)
	}
	ruleIDs := []string{"", "unknown", "report_shared_artifact.missing_source"}
	for _, rule := range staticRules {
		ruleIDs = append(ruleIDs, rule.ID)
	}
	for _, mysql := range []bool{false, true} {
		for _, rule := range ruleIDs {
			record := "fixture-id"
			if rule == "" {
				record = ""
			}
			values := map[string]any{"RuleID": rule, "RecordID": record, "OlderThan": "2026-01-01", "MySQLContract": mysql}
			state := newState()
			for name, value := range values {
				if err := state.SetValue(name, value); err != nil {
					t.Fatal(err)
				}
			}
			if err := execution.Exec(state); err != nil {
				t.Fatal(err)
			}
			query := state.Buffer.String()
			_, enabled := FindRule(mysql, rule)
			switch {
			case rule == "":
				if strings.Count(query, "UNION ALL") != len(Rules(mysql)) || strings.Contains(query, "fixture-id") {
					t.Fatal("list lost rules")
				}
			case enabled:
				if strings.Count(query, "UNION ALL") != 1 || !strings.Contains(query, " = fixture-id") || !strings.Contains(query, "SELECT '"+rule+"' AS rule_id") {
					t.Fatalf("mysql=%t rule=%s exact recheck scanned unrelated rules or lost key predicate: %s", mysql, rule, query)
				}
			default:
				if strings.Contains(query, "UNION ALL") {
					t.Fatalf("mysql=%t rule=%s unknown/disabled rule rendered a candidate: %s", mysql, rule, query)
				}
			}
		}
	}
}
