package executionprotection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	toolprotection "github.com/viant/agently-core/protocol/tool/protection"
	"github.com/viant/datly"
)

const standardConnectorName = "agently"

type daoRepository struct {
	dao *datly.Service
}

type unavailableRepository struct {
	reason string
}

// NewDAORepository retains the existing DAO without opening a connector or
// checking schema. Missing persistence fails only when a protected call claims.
func NewDAORepository(dao *datly.Service) Repository {
	if dao == nil {
		return &unavailableRepository{reason: "standard agently DAO is nil"}
	}
	return &daoRepository{dao: dao}
}

func (r *unavailableRepository) Claim(context.Context, ClaimRecord) (bool, error) {
	return false, fmt.Errorf("claim repository unavailable: %s", r.reason)
}

func (r *unavailableRepository) Finish(context.Context, string, toolprotection.State, time.Time) error {
	return fmt.Errorf("claim repository unavailable: %s", r.reason)
}

func (r *daoRepository) Claim(ctx context.Context, record ClaimRecord) (bool, error) {
	db, driver, err := r.database()
	if err != nil {
		return false, err
	}
	const values = "(claim_key, rule_id, canonical_tool_name, turn_id, semantic_request_hash, state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 'claimed', ?, ?)"
	query := "INSERT INTO tool_execution_claim " + values
	if isSQLiteDriver(driver) {
		query += " ON CONFLICT(claim_key) DO NOTHING"
	}
	result, err := db.ExecContext(ctx, query,
		record.ClaimKey, record.RuleID, record.CanonicalToolName, record.TurnID,
		record.SemanticHash, record.CreatedAt, record.CreatedAt)
	if err != nil {
		if isMySQLDuplicate(err, driver) {
			exists, lookupErr := claimKeyExists(ctx, db, record.ClaimKey)
			if lookupErr != nil {
				return false, fmt.Errorf("verify claim_key after MySQL duplicate error: %w", lookupErr)
			}
			if exists {
				return false, nil
			}
		}
		return false, err
	}
	if isSQLiteDriver(driver) {
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return false, rowsErr
		}
		return rows == 1, nil
	}
	return true, nil
}

func claimKeyExists(ctx context.Context, db *sql.DB, claimKey string) (bool, error) {
	var marker int
	err := db.QueryRowContext(ctx,
		"SELECT 1 FROM tool_execution_claim WHERE claim_key = ? LIMIT 1",
		claimKey,
	).Scan(&marker)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, err
	}
}

func (r *daoRepository) Finish(ctx context.Context, claimKey string, state toolprotection.State, finishedAt time.Time) error {
	db, _, err := r.database()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		"UPDATE tool_execution_claim SET state = ?, updated_at = ?, finished_at = ? WHERE claim_key = ?",
		string(state), finishedAt, finishedAt, claimKey)
	return err
}

func (r *daoRepository) database() (*sql.DB, string, error) {
	if r == nil || r.dao == nil {
		return nil, "", fmt.Errorf("claim repository unavailable: standard agently DAO is nil")
	}
	connector, err := r.dao.Resource().Connector(standardConnectorName)
	if err != nil {
		return nil, "", fmt.Errorf("lookup standard agently connector: %w", err)
	}
	if connector == nil {
		return nil, "", fmt.Errorf("standard agently connector is nil")
	}
	driver := strings.TrimSpace(connector.Driver)
	if !isSQLiteDriver(driver) && !strings.Contains(strings.ToLower(driver), "mysql") {
		return nil, "", fmt.Errorf("unsupported standard agently connector driver %q", driver)
	}
	db, err := connector.DB()
	if err != nil {
		return nil, "", fmt.Errorf("open standard agently connector: %w", err)
	}
	if db == nil {
		return nil, "", fmt.Errorf("standard agently connector returned nil database")
	}
	return db, driver, nil
}

func isSQLiteDriver(driver string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(driver)), "sqlite")
}

func isMySQLDuplicate(err error, driver string) bool {
	if !strings.Contains(strings.ToLower(strings.TrimSpace(driver)), "mysql") {
		return false
	}
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
