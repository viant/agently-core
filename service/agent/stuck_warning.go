package agent

import (
	"os"
	"strconv"
	"strings"
	"time"
)

func stuckWarnDuration() time.Duration {
	v := strings.TrimSpace(os.Getenv("AGENTLY_CONVERSATION_STUCK_WARN_SEC"))
	if v == "" {
		return 10 * time.Minute
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}
