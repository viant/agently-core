package logx

import (
	"log"
	"os"
	"strings"
)

func Enabled() bool {
	return EnabledFor("AGENTLY_DEBUG", "AGENTLY_SCHEDULER_DEBUG")
}

func EnabledFor(keys ...string) bool {
	for _, key := range keys {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "1", "true", "yes", "y", "on":
			return true
		}
	}
	return false
}

func Debugf(component, format string, args ...any) {
	if !Enabled() {
		return
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "debug"
	}
	log.Printf("[debug][%s] "+format, append([]any{component}, args...)...)
}

func Infof(component, format string, args ...any) {
	Debugf(component, "[INFO] "+format, args...)
}

func Warnf(component, format string, args ...any) {
	Debugf(component, "[WARN] "+format, args...)
}

func Errorf(component, format string, args ...any) {
	Debugf(component, "[ERROR] "+format, args...)
}
