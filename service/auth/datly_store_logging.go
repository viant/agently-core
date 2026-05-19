package auth

import (
	"log"
	"strings"
	"time"
)

const authDatlyStoreSlowThreshold = 250 * time.Millisecond

func logDatlyStoreOp(kind, op, key string, started time.Time, err error) {
	elapsed := time.Since(started)
	if err == nil && elapsed < authDatlyStoreSlowThreshold {
		return
	}
	log.Printf("[auth-store] kind=%q op=%q key=%q elapsed_ms=%d err=%q",
		strings.TrimSpace(kind),
		strings.TrimSpace(op),
		strings.TrimSpace(key),
		elapsed.Milliseconds(),
		errString(err),
	)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}
