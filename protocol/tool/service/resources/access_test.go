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
			loc:     "workspace://localhost/Users/adrianwitas/projects/poly/poly",
			allowed: []string{"file://localhost/Users/adrianwitas"},
			expect:  true,
		},
		{
			name:    "file under allowed workspace root",
			loc:     "file://localhost/Users/adrianwitas/projects/poly/poly",
			allowed: []string{"workspace://localhost/Users/adrianwitas"},
			expect:  true,
		},
		{
			name:    "outside allowed root remains blocked",
			loc:     "workspace://localhost/tmp/other",
			allowed: []string{"file://localhost/Users/adrianwitas"},
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
