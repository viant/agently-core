package sqlitewrite

import (
	"context"
	"strings"
	"sync"

	"github.com/viant/datly"
)

var (
	gatesMu sync.Mutex
	gates   = map[string]chan struct{}{}
)

func Key(dao *datly.Service, connectorName string) string {
	if dao == nil || dao.Resource() == nil {
		return ""
	}
	conn, err := dao.Resource().Connector(strings.TrimSpace(connectorName))
	if err != nil || conn == nil {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(conn.Driver), "sqlite") {
		return ""
	}
	dsn := strings.TrimSpace(conn.DSN)
	if dsn == "" {
		dsn = strings.TrimSpace(conn.Name)
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
