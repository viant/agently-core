package mcpname

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalProviderNamesAreValidAndIdempotent(t *testing.T) {
	valid := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	for _, test := range []struct{ raw, want string }{
		{"internal-skills:resource/get", "internal-skills-resource_get"},
		{"service/path.method", "service_path-method"},
		{"service:cursor.number-list", "service_cursor-number-list"},

		{"native_name-method", "native_name-method"},
	} {
		t.Run(test.raw, func(t *testing.T) {
			actual := Canonical(test.raw)
			require.Equal(t, test.want, actual)
			require.Regexp(t, valid, actual)
			require.Equal(t, actual, Canonical(actual))
		})
	}
}

func TestCanonicalRetainsConfiguredWildcardPatterns(t *testing.T) {
	require.Equal(t, "catalog-*", Canonical("catalog:*"))
	require.Equal(t, "*", Canonical("*"))
	require.Equal(t, "service_path-*", Canonical("service/path:*"))
}
