package a2a

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		must []string // substrings that must appear in output
		deny []string // substrings that must NOT appear
	}{
		{
			name: "bearer in error body",
			in:   `upstream returned 401: {"error":"Bearer eyJhbGciOi.ABCDEF.XYZ rejected"}`,
			must: []string{"Bearer [REDACTED]"},
			deny: []string{"eyJhbGciOi", "ABCDEF.XYZ"},
		},
		{
			name: "access_token json field",
			in:   `token refresh failed: access_token="eyJhbGciOiJIUzI1NiJ9.abc.def"`,
			must: []string{"access_token=", "[REDACTED]"},
			deny: []string{"eyJhbGciOiJIUzI1NiJ9"},
		},
		{
			name: "id_token kv pair",
			in:   `oauth response id_token=eyJx.y.z scope=read`,
			must: []string{"id_token=", "[REDACTED]", "scope=read"},
			deny: []string{"eyJx.y.z"},
		},
		{
			name: "no secret",
			in:   `connection refused: dial tcp 127.0.0.1:9393`,
			must: []string{"connection refused"},
			deny: []string{"[REDACTED]"},
		},
	}
	for _, tc := range cases {
		got := redactSecrets(tc.in)
		for _, m := range tc.must {
			if !strings.Contains(got, m) {
				t.Errorf("%s: expected %q in %q", tc.name, m, got)
			}
		}
		for _, d := range tc.deny {
			if strings.Contains(got, d) {
				t.Errorf("%s: expected %q NOT in %q", tc.name, d, got)
			}
		}
	}
}

func TestRedactErrNil(t *testing.T) {
	if got := redactErr(nil); got != "" {
		t.Errorf("redactErr(nil) = %q, want empty", got)
	}
	if got := redactErr(errors.New("Bearer abc123")); !strings.Contains(got, "[REDACTED]") {
		t.Errorf("redactErr did not redact: %q", got)
	}
}
