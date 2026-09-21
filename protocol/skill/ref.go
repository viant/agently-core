package skill

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var catalogSegment = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Ref is a host-owned identity. It deliberately contains no upstream URI.
type Ref struct {
	ServerID    string
	Name        string
	ResourceURI string
	Revision    string
}

func ValidCatalogSegment(s string) bool { return len(s) <= 128 && catalogSegment.MatchString(s) }

func (r Ref) URI() string {
	result := "skill://" + r.ServerID + "/" + r.Name
	if r.ResourceURI != "" {
		result += "~" + base64.RawURLEncoding.EncodeToString([]byte(r.ResourceURI))
		if r.Revision != "" {
			result += "~" + r.Revision
		}
	}
	return result
}

// ParseRef accepts the catalog URI, its document alias, or server/name.
func ParseRef(raw string) (Ref, error) {
	s := strings.TrimSpace(raw)
	if !strings.Contains(s, "://") {
		s = "skill://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "skill" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || strings.Contains(s, "%") || !ValidCatalogSegment(u.Host) {
		return Ref{}, fmt.Errorf("invalid skill reference")
	}
	p := strings.TrimPrefix(u.Path, "/")
	p = strings.TrimSuffix(p, "/SKILL.md")
	var remote string
	var revision string
	if parts := strings.Split(p, "~"); len(parts) == 3 {
		revision = parts[2]
		if len(revision) != 64 || strings.Trim(revision, "0123456789abcdef") != "" {
			return Ref{}, fmt.Errorf("invalid skill revision")
		}
		p = parts[0] + "~" + parts[1]
	}
	if parts := strings.SplitN(p, "~", 2); len(parts) == 2 {
		p = parts[0]
		b, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return Ref{}, fmt.Errorf("invalid skill reference")
		}
		remote = string(b)
		u, err := url.Parse(remote)
		if err != nil || u.Scheme == "" || !strings.HasSuffix(u.Path, "/SKILL.md") {
			return Ref{}, fmt.Errorf("invalid skill reference")
		}
	}
	if !ValidCatalogSegment(p) {
		return Ref{}, fmt.Errorf("invalid skill reference")
	}
	return Ref{ServerID: u.Host, Name: p, ResourceURI: remote, Revision: revision}, nil
}

// Identity keeps portable names unchanged while distinguishing remote sources.
func (s *Skill) Identity() string {
	if s == nil {
		return ""
	}
	if s.CatalogURI != "" {
		return s.CatalogURI
	}
	return s.Frontmatter.Name
}
