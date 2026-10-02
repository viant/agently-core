package augmenter

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/viant/datly/bootstrap/connector"
)

type upstreamPool struct {
	set *connector.Set
	db  *sql.DB
}

// openUpstream supplies the external ingestion library with an application
// owned stock connector. Resolved connection targets share a pool.
func (s *Service) openUpstream(ctx context.Context, driver, dsn string) (*sql.DB, error) {
	if ctx == nil {
		return nil, fmt.Errorf("upstream context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(driver + "\x00" + dsn))
	s.upstreamMu.Lock()
	defer s.upstreamMu.Unlock()
	if s.upstreamsClosed {
		return nil, fmt.Errorf("upstream connectors are closed")
	}
	if pool := s.upstreamPools[key]; pool != nil {
		return pool.db, nil
	}
	var set *connector.Set
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		set, err = connector.Open(pingCtx, []connector.Config{{Name: "embedius_upstream", Driver: driver, DSN: dsn}}, "embedius_upstream")
		cancel()
		if err == nil {
			break
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}
	if err != nil {
		return nil, err
	}
	db, err := set.ResolveDB(ctx, "embedius_upstream")
	if err != nil {
		_ = set.Close()
		return nil, err
	}
	if s.upstreamPools == nil {
		s.upstreamPools = make(map[[32]byte]*upstreamPool)
	}
	s.upstreamPools[key] = &upstreamPool{set: set, db: db}
	return db, nil
}

// Close releases upstream connector pools owned by this service.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.upstreamMu.Lock()
	defer s.upstreamMu.Unlock()
	s.upstreamsClosed = true
	var err error
	for key, pool := range s.upstreamPools {
		err = errors.Join(err, pool.set.Close())
		delete(s.upstreamPools, key)
	}
	return err
}
