// Package cleanupscope validates host-only metadata read boundaries.
package cleanupscope

import (
	"fmt"
	"strings"

	"github.com/viant/agently-core/internal/store/maintenancebatch"
)

type Predicate struct {
	Present bool
	IDs     []string
}

func Validate(trusted bool, predicates ...Predicate) error {
	if !trusted {
		return fmt.Errorf("authorized cleanup metadata scope is required")
	}
	count := 0
	for _, predicate := range predicates {
		if !predicate.Present {
			continue
		}
		count++
		if len(predicate.IDs) == 0 || len(predicate.IDs) > maintenancebatch.Size {
			return fmt.Errorf("cleanup metadata predicate requires 1..%d IDs", maintenancebatch.Size)
		}
		for _, id := range predicate.IDs {
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("cleanup metadata identity is empty")
			}
		}
	}
	if count != 1 {
		return fmt.Errorf("cleanup metadata read requires exactly one bounded predicate")
	}
	return nil
}
