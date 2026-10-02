package sqlitewrite

import (
	"context"
	"strings"
	"sync"
)

var (
	gatesMu sync.Mutex
	gates   = map[string]chan struct{}{}
)

// KeyForConnector identifies SQLite connections that share a write gate.
// Callers supply connector metadata; this package does not depend on a Datly
// service or open a database.
func KeyForConnector(driver, dsn, name string) string {
	driver = strings.TrimSpace(driver)
	if !strings.EqualFold(driver, "sqlite") && !strings.EqualFold(driver, "sqlite3") {
		return ""
	}
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		dsn = strings.TrimSpace(name)
	}
	if dsn == "" {
		dsn = "default"
	}
	return "sqlite:" + dsn
}

func Do[T any](ctx context.Context, key string, fn func() (T, error)) (T, error) {
	var zero T
	key = strings.TrimSpace(key)
	if key == "" {
		return fn()
	}
	ch := gateFor(key)
	select {
	case ch <- struct{}{}:
		defer func() { <-ch }()
	case <-ctx.Done():
		return zero, ctx.Err()
	}
	return fn()
}

func gateFor(key string) chan struct{} {
	gatesMu.Lock()
	defer gatesMu.Unlock()
	if ch, ok := gates[key]; ok {
		return ch
	}
	ch := make(chan struct{}, 1)
	gates[key] = ch
	return ch
}
