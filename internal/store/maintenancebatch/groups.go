// Package maintenancebatch bounds canonical writer requests without crossing
// the caller's authorization, expected-reference or transaction boundaries.
package maintenancebatch

import "context"

// Size preserves the deletion writer's existing bound (distinct from the
// graph lock batch size). Keys must include every shared writer guard.
const Size = 400

func Groups[T any, K comparable](ctx context.Context, rows []T, key func(T) (K, error), apply func(K, []T) error) error {
	groups := make(map[K][]T)
	keys := make([]K, 0)
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		k, err := key(row)
		if err != nil {
			return err
		}
		if _, exists := groups[k]; !exists {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], row)
	}
	// First-seen order is deterministic for canonical reader results; avoid
	// map iteration so errors and mutation order remain reproducible.
	for _, k := range keys {
		group := groups[k]
		for start := 0; start < len(group); start += Size {
			if err := ctx.Err(); err != nil {
				return err
			}
			end := min(start+Size, len(group))
			if err := apply(k, group[start:end]); err != nil {
				return err
			}
		}
	}
	return nil
}
