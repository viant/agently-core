package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/sqlitewrite"
)

type OrphanMaintenanceReason string

const (
	OrphanMaintenanceDeletedReason          OrphanMaintenanceReason = "deleted"
	OrphanMaintenanceDetachedReason         OrphanMaintenanceReason = "detached"
	OrphanMaintenanceReportOnlyReason       OrphanMaintenanceReason = "report_only"
	OrphanMaintenanceNoLongerEligibleReason OrphanMaintenanceReason = "no_longer_eligible"
)

// OrphanMaintenanceRequest identifies one previously reported orphan. The
// rule, age and missing-parent condition are all re-evaluated in a serializable
// transaction before any mutation is allowed.
type OrphanMaintenanceRequest struct {
	RuleID    string
	RecordID  string
	OlderThan time.Time
	Lease     MaintenanceLease
}

type OrphanMaintenanceResult struct {
	RuleID   string
	RecordID string
	Action   OrphanMaintenanceAction
	Eligible bool
	Mutated  bool
	Deleted  bool
	Detached bool
	Reason   OrphanMaintenanceReason
}

func (s *datlyService) MaintainOrphanCandidate(ctx context.Context, request OrphanMaintenanceRequest) (*OrphanMaintenanceResult, error) {
	request.RuleID = strings.TrimSpace(request.RuleID)
	request.RecordID = strings.TrimSpace(request.RecordID)
	request.OlderThan = request.OlderThan.UTC()
	if request.RuleID == "" {
		return nil, fmt.Errorf("%w: orphan rule id is required", ErrInvalidConversationMaintenanceRequest)
	}
	if request.RecordID == "" {
		return nil, fmt.Errorf("%w: orphan record id is required", ErrInvalidConversationMaintenanceRequest)
	}
	if request.OlderThan.IsZero() {
		return nil, fmt.Errorf("%w: orphan grace-period cutoff is required", ErrInvalidConversationMaintenanceRequest)
	}
	if err := validateMaintenanceLease(normalizeMaintenanceLease(request.Lease)); err != nil {
		return nil, fmt.Errorf("%w: orphan maintenance requires maintenance lease: %v", ErrInvalidConversationMaintenanceRequest, err)
	}

	result := &OrphanMaintenanceResult{RuleID: request.RuleID, RecordID: request.RecordID}
	_, err := sqlitewrite.Do(ctx, s.writeGate, func() (struct{}, error) {
		return struct{}{}, s.maintainOrphanCandidateDirect(ctx, request, result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *datlyService) maintainOrphanCandidateDirect(ctx context.Context, request OrphanMaintenanceRequest, result *OrphanMaintenanceResult) error {
	db, driver, err := s.dbWithDriver()
	if err != nil {
		return err
	}
	capabilities, err := deleteSchemaCapabilitiesForDriver(driver)
	if err != nil {
		return err
	}
	rule := orphanRuleByID(orphanMaintenanceRules(capabilities), request.RuleID)
	if rule == nil {
		return fmt.Errorf("%w: unknown orphan rule %q", ErrInvalidConversationMaintenanceRequest, request.RuleID)
	}
	if err := validateOrphanMaintenanceMutationRule(*rule); err != nil {
		return err
	}
	result.Action = rule.Action

	keyValues, err := orphanMaintenanceKeyValues(*rule, request.RecordID)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := lockMaintenanceLeaseTx(ctx, tx, driver, request.Lease); err != nil {
		return err
	}

	eligible, err := recheckOrphanMaintenanceCandidate(ctx, tx, capabilities.driver, *rule, keyValues, request.OlderThan)
	if err != nil {
		return err
	}
	if !eligible {
		result.Reason = OrphanMaintenanceNoLongerEligibleReason
		if err = tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}
	result.Eligible = true
	if rule.Action == OrphanMaintenanceReportOnly {
		result.Reason = OrphanMaintenanceReportOnlyReason
		if err = tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}

	query, args := orphanMaintenanceMutationStatement(*rule, keyValues)
	execResult, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := execResult.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("orphan maintenance rule=%q record=%q affected %d rows, expected 1", rule.ID, request.RecordID, affected)
	}
	result.Mutated = true
	switch rule.Action {
	case OrphanMaintenanceSafeDelete:
		result.Deleted = true
		result.Reason = OrphanMaintenanceDeletedReason
	case OrphanMaintenanceSafeDetach:
		result.Detached = true
		result.Reason = OrphanMaintenanceDetachedReason
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func validateOrphanMaintenanceMutationRule(rule orphanMaintenanceRule) error {
	if !isOrphanMaintenanceIdentifier(rule.Table) || !isOrphanMaintenanceIdentifier(rule.Alias) || len(rule.KeyColumns) == 0 {
		return fmt.Errorf("invalid orphan maintenance rule %q", rule.ID)
	}
	for _, column := range rule.KeyColumns {
		if !isOrphanMaintenanceIdentifier(column) {
			return fmt.Errorf("invalid key column %q in orphan maintenance rule %q", column, rule.ID)
		}
	}
	switch rule.Action {
	case OrphanMaintenanceSafeDelete, OrphanMaintenanceReportOnly:
		if rule.DetachColumn != "" {
			return fmt.Errorf("unexpected detach column in orphan maintenance rule %q", rule.ID)
		}
	case OrphanMaintenanceSafeDetach:
		if !isOrphanMaintenanceIdentifier(rule.DetachColumn) {
			return fmt.Errorf("invalid detach column in orphan maintenance rule %q", rule.ID)
		}
	default:
		return fmt.Errorf("invalid action in orphan maintenance rule %q", rule.ID)
	}
	return nil
}

func orphanMaintenanceKeyValues(rule orphanMaintenanceRule, recordID string) ([]interface{}, error) {
	parts := strings.Split(recordID, orphanMaintenanceRecordSeparator)
	if len(parts) != len(rule.KeyColumns) {
		return nil, fmt.Errorf("%w: orphan record id does not match rule %q key", ErrInvalidConversationMaintenanceRequest, rule.ID)
	}
	result := make([]interface{}, len(parts))
	for i := range parts {
		result[i] = parts[i]
	}
	return result, nil
}

func recheckOrphanMaintenanceCandidate(ctx context.Context, tx *sql.Tx, driver string, rule orphanMaintenanceRule, keyValues []interface{}, olderThan time.Time) (bool, error) {
	keyPredicate := orphanMaintenanceKeyPredicate(rule, true)
	query := fmt.Sprintf("SELECT 1 FROM %s %s WHERE %s AND %s IS NOT NULL AND %s <= ? AND (%s) LIMIT 1",
		rule.Table, rule.Alias, keyPredicate, rule.AgeExpr, rule.AgeExpr, rule.Predicate)
	if strings.Contains(driver, "mysql") {
		query += " FOR UPDATE"
	}
	args := append(append([]interface{}{}, keyValues...), olderThan)
	var found int
	err := tx.QueryRowContext(ctx, query, args...).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func orphanMaintenanceMutationStatement(rule orphanMaintenanceRule, keyValues []interface{}) (string, []interface{}) {
	keyPredicate := orphanMaintenanceKeyPredicate(rule, false)
	query := "DELETE FROM " + rule.Table + " WHERE " + keyPredicate
	if rule.Action == OrphanMaintenanceSafeDetach {
		query = "UPDATE " + rule.Table + " SET " + rule.DetachColumn + " = NULL WHERE " + keyPredicate
	}
	return query, append([]interface{}{}, keyValues...)
}

func orphanMaintenanceKeyPredicate(rule orphanMaintenanceRule, qualified bool) string {
	parts := make([]string, 0, len(rule.KeyColumns))
	for _, column := range rule.KeyColumns {
		if qualified {
			column = rule.Alias + "." + column
		}
		parts = append(parts, column+" = ?")
	}
	return strings.Join(parts, " AND ")
}
