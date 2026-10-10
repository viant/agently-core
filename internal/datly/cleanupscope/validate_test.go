package cleanupscope

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateBoundedHostScope(t *testing.T) {
	for _, size := range []int{0, 1, 399, 400, 401} {
		ids := make([]string, size)
		for i := range ids {
			ids[i] = fmt.Sprint(i)
		}
		err := Validate(true, Predicate{Present: true, IDs: ids})
		if size == 0 || size > 400 {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}
	require.Error(t, Validate(false, Predicate{Present: true, IDs: []string{"a"}}))
	require.Error(t, Validate(true))
	require.Error(t, Validate(true, Predicate{Present: true, IDs: []string{" "}}))
	require.Error(t, Validate(true, Predicate{Present: true, IDs: []string{"a"}}, Predicate{Present: true, IDs: []string{"b"}}))
}
