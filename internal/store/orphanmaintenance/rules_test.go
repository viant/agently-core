package orphanmaintenance

import (
	"errors"
	"testing"
)

func TestStaticOrphanContractKeepsUnsafePseudoRulesDisabled(t *testing.T) {
	for _, mysql := range []bool{false, true} {
		rules := Rules(mysql)
		want := 56
		if mysql {
			want = 59
		}
		if len(rules) != want {
			t.Fatalf("mysql=%v rules=%d want%d", mysql, len(rules), want)
		}
		previous := Rule{}
		seen := map[string]bool{}
		for _, rule := range rules {
			if seen[rule.ID] {
				t.Fatalf("duplicate rule %s", rule.ID)
			}
			seen[rule.ID] = true
			if rule.Priority < previous.Priority || rule.Priority == previous.Priority && rule.ID < previous.ID {
				t.Fatalf("rule order %s before %s", rule.ID, previous.ID)
			}
			previous = rule
		}
		for _, id := range []string{"report_audit_event.missing_job", "report_audit_event.missing_artifact", "report_shared_artifact.missing_source"} {
			if _, found := FindRule(mysql, id); found {
				t.Fatalf("unsafe pseudo-orphan rule restored: %s", id)
			}
		}
		for _, id := range []string{"investigation.missing_conversation", "schedule_run.missing_schedule", "schedule_run.missing_conversation"} {
			_, found := FindRule(mysql, id)
			if found != mysql {
				t.Fatalf("optional schema rule %s availability=%v mysql=%v", id, found, mysql)
			}
		}
	}
}
func TestOrphanCursorRetainsCompositeRecordIdentity(t *testing.T) {
	rule, ok := FindRule(false, "conversation_report_context.missing_conversation")
	if !ok {
		t.Fatal("rule missing")
	}
	record := "owner" + RecordSeparator + "conversation"
	cursor := EncodeCursor(rule, record)
	priority, id, key, err := DecodeCursor(cursor, Rules(false))
	if err != nil || priority != 1080 || id != rule.ID || key != record {
		t.Fatalf("decoded=%d/%s/%q err=%v", priority, id, key, err)
	}
	parts, err := KeyValues(rule, key)
	if err != nil || len(parts) != 2 || parts[0] != "owner" || parts[1] != "conversation" {
		t.Fatalf("keys=%v err=%v", parts, err)
	}
	for _, bad := range []string{"invalid", "000080" + cursorOrderSeparator + rule.ID + cursorSeparator + record, "000001" + cursorOrderSeparator + "unknown" + cursorSeparator + record} {
		if _, _, _, err := DecodeCursor(bad, Rules(false)); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("cursor %q error=%v", bad, err)
		}
	}
	if _, err := KeyValues(rule, "one-part"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid key=%v", err)
	}
}
