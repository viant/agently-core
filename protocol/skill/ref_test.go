package skill

import "testing"

func TestSkillReferences(t *testing.T) {
	for _, raw := range []string{"skill://github/review", "skill://github/review/SKILL.md", "github/review"} {
		r, err := ParseRef(raw)
		if err != nil || r.URI() != "skill://github/review" {
			t.Fatalf("%s: %+v %v", raw, r, err)
		}
	}
	for _, raw := range []string{"skill://user@github/review", "skill://github:80/review", "skill://github/review?q=x", "skill://github/review#x", "skill://github/%2e%2e", "skill://github/%252e%252e", "skill://github/a%2fb", "skill://github/a/b", "skill://github/../review"} {
		if _, err := ParseRef(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
