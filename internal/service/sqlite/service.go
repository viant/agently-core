package sqlite

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	iscript "github.com/viant/agently-core/internal/script"
	_ "modernc.org/sqlite"
)

var (
	memoryDBMu sync.Mutex
	memoryDBs  = map[string]*sql.DB{}
)

// Service provisions sqlite databases for agently-core.
type Service struct {
	root string
	path string
}

func New(root string) *Service { return &Service{root: root} }

// WithPath overrides the sqlite file path.
func (s *Service) WithPath(path string) *Service {
	if s == nil {
		return s
	}
	s.path = strings.TrimSpace(path)
	return s
}

// Ensure sets up a SQLite database and loads schema if needed. It returns a DSN.
func (s *Service) Ensure(ctx context.Context) (string, error) {
	base := s.root
	if strings.TrimSpace(base) == "" {
		wd, _ := os.Getwd()
		base = wd
	}

	dbFile := strings.TrimSpace(s.path)
	if dbFile == "" {
		dbDir := filepath.Join(base, "db")
		if err := os.MkdirAll(dbDir, 0o755); err != nil {
			return "", fmt.Errorf("failed to create db dir: %w", err)
		}
		dbFile = filepath.Join(dbDir, "agently-core.db")
	} else {
		if err := os.MkdirAll(filepath.Dir(dbFile), 0o755); err != nil {
			return "", fmt.Errorf("failed to create db dir: %w", err)
		}
	}

	dsn := "file:" + dbFile + "?cache=shared&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	return s.ensureSchema(ctx, dsn)
}

// EnsureInMemory initializes an in-memory shared SQLite DB and returns its DSN.
func (s *Service) EnsureInMemory(ctx context.Context) (string, error) {
	dsn := "file:agently-core-memory?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	return s.ensureSchemaInMemory(ctx, dsn)
}

func (s *Service) ensureSchema(ctx context.Context, dsn string) (string, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", fmt.Errorf("failed to open sqlite db: %w", err)
	}
	defer db.Close()

	_, _ = db.ExecContext(ctx, "PRAGMA foreign_keys=ON")
	_, _ = db.ExecContext(ctx, "PRAGMA busy_timeout=5000")

	var name string
	err = db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='conversation'").Scan(&name)
	if err == nil && name == "conversation" {
		return dsn, nil
	}

	if err := loadSchema(ctx, db, iscript.SqlListScript); err != nil {
		return "", err
	}
	return dsn, nil
}

func (s *Service) ensureSchemaInMemory(ctx context.Context, dsn string) (string, error) {
	memoryDBMu.Lock()
	db, ok := memoryDBs[dsn]
	memoryDBMu.Unlock()
	if !ok {
		var err error
		db, err = sql.Open("sqlite", dsn)
		if err != nil {
			return "", fmt.Errorf("failed to open sqlite in-memory db: %w", err)
		}
		memoryDBMu.Lock()
		memoryDBs[dsn] = db
		memoryDBMu.Unlock()
	}

	_, _ = db.ExecContext(ctx, "PRAGMA foreign_keys=ON")
	_, _ = db.ExecContext(ctx, "PRAGMA busy_timeout=5000")

	var name string
	err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='conversation'").Scan(&name)
	if err == nil && name == "conversation" {
		return dsn, nil
	}
	if err := loadSchema(ctx, db, iscript.SqlListScript); err != nil {
		return "", err
	}
	return dsn, nil
}

func loadSchema(ctx context.Context, db *sql.DB, ddl string) error {
	if strings.TrimSpace(ddl) == "" {
		return fmt.Errorf("embedded sqlite schema is empty")
	}
	scanner := bufio.NewScanner(strings.NewReader(ddl))
	var buf strings.Builder
	flush := func() error {
		stmt := strings.TrimSpace(buf.String())
		if stmt == "" {
			return nil
		}
		if _, execErr := db.ExecContext(ctx, stmt); execErr != nil {
			return fmt.Errorf("schema exec failed: %w (sql: %s)", execErr, stmt)
		}
		buf.Reset()
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		buf.WriteString(line)
		buf.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("schema scan failed: %w", err)
	}
	if err := flush(); err != nil {
		return err
	}
	return nil
}
