package auth

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/authlog"
	"github.com/viant/agently-core/internal/logx"
)

const authDatlyStoreSlowThreshold = 250 * time.Millisecond

func logDatlyStoreOp(ctx context.Context, kind, op, key string, started time.Time, err error) {
	elapsed := time.Since(started)
	kind = strings.TrimSpace(kind)
	op = strings.TrimSpace(op)
	key = strings.TrimSpace(key)
	if kind == "token" {
		if err == nil {
			if elapsed >= authDatlyStoreSlowThreshold {
				logx.Debugf("auth-token", "slow store op=%q elapsed_ms=%d", op, elapsed.Milliseconds())
			}
			return
		}
		parts := strings.SplitN(key, "|", 2)
		userID := ""
		provider := ""
		if len(parts) > 0 {
			userID = parts[0]
		}
		if len(parts) > 1 {
			provider = parts[1]
		}
		authlog.Log(ctx, authlog.Event{
			Op:             "store_" + op,
			UserID:         userID,
			Provider:       provider,
			Classification: "persistence",
			Action:         "failed",
			Elapsed:        elapsed,
		})
		return
	}
	if err == nil {
		if elapsed >= authDatlyStoreSlowThreshold {
			logx.Debugf("auth-store", "slow kind=%q op=%q elapsed_ms=%d", kind, op, elapsed.Milliseconds())
		}
		return
	}
	log.Printf("[auth-store] kind=%q op=%q key=%q elapsed_ms=%d err=%q",
		authlog.Sanitize(kind),
		authlog.Sanitize(op),
		authlog.Sanitize(key),
		elapsed.Milliseconds(),
		errString(err),
	)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return authlog.Sanitize(err.Error())
}
