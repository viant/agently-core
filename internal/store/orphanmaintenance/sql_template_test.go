package orphanmaintenance

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/viant/velty"
	_ "modernc.org/sqlite"
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
			if strings.Contains(strings.ToLower(query), "printf(") {
				t.Fatalf("mysql=%t rule=%s report discovery SQL still requires SQLite printf", mysql, rule)
			}
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

func TestOrphanCompositeKeyConcatPreservesSQLiteEncoding(t *testing.T) {
	const query = `SELECT
 CAST(printf('%s%s%s', COALESCE(CAST(first AS CHAR), ''), CHAR(31), COALESCE(CAST(second AS CHAR), '')) AS CHAR),
 CAST(CONCAT(COALESCE(CAST(first AS CHAR), ''), CHAR(31), COALESCE(CAST(second AS CHAR), '')) AS CHAR)
 FROM (SELECT ? AS first, ? AS second)`
	for _, driver := range []string{"sqlite", "sqlite3"} {
		t.Run(driver, func(t *testing.T) {
			db, err := sql.Open(driver, ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := db.Ping(); err != nil {
				if driver == "sqlite3" && strings.Contains(err.Error(), "requires cgo") {
					t.Skip("sqlite3 requires a CGO-enabled build")
				}
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name          string
				first, second any
				want          string
			}{
				{"nulls", nil, nil, RecordSeparator},
				{"null owner", nil, "conversation", RecordSeparator + "conversation"},
				{"null reference", "owner", nil, "owner" + RecordSeparator},
				{"empty", "", "", RecordSeparator},
				{"ordinary", "owner", "conversation", "owner" + RecordSeparator + "conversation"},
				{"unicode and format characters", "zażółć%s", "🙂'conversation", "zażółć%s" + RecordSeparator + "🙂'conversation"},
				{"embedded separator", "owner" + RecordSeparator, "line\nid", "owner" + RecordSeparator + RecordSeparator + "line\nid"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var before, after string
					if err := db.QueryRow(query, tc.first, tc.second).Scan(&before, &after); err != nil {
						t.Fatal(err)
					}
					if before != tc.want || after != before {
						t.Fatalf("key encoding changed: printf=%q concat=%q want=%q", before, after, tc.want)
					}
				})
			}
		})
	}
}
