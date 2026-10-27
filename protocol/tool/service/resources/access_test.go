package resources

import "testing"

func TestIsAllowedWorkspace_NormalizesFileAndWorkspaceForms(t *testing.T) {
	testCases := []struct {
		name    string
		loc     string
		allowed []string
		expect  bool
	}{
		{
			name:    "workspace under allowed file root",
			loc:     "workspace://localhost/workspace/example",
			allowed: []string{"file:///workspace/example"},
			expect:  true,
		},
		{
			name:    "file under allowed workspace root",
			loc:     "file:///workspace/example",
			allowed: []string{"workspace://localhost/workspace/example"},
			expect:  true,
		},
		{
			name:    "outside allowed root remains blocked",
			loc:     "workspace://localhost/workspace/other",
			allowed: []string{"file:///workspace/example"},
			expect:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAllowedWorkspace(tc.loc, tc.allowed); got != tc.expect {
				t.Fatalf("isAllowedWorkspace(%q, %v) = %v, want %v", tc.loc, tc.allowed, got, tc.expect)
			}
		})
	}
}
