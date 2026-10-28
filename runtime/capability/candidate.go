// Package capability describes authorized metadata used for discovery ranking.
// Candidates are presentation data, never permission or executable schemas.
package capability

import (
	"net/url"
	"strings"

	skill "github.com/viant/agently-core/protocol/skill"
)

const DefaultNamespace = "default"
const DefaultNamespaceDescription = "Tools and skills without a declared namespace. Search these capabilities by their names and descriptions, and broaden discovery whenever needed."
const DefaultCandidateLimit = 40

type Candidate struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	URI         string   `json:"uri,omitempty"`
	Namespace   string   `json:"namespace"`
	Provider    string   `json:"-"`
	Retained    bool     `json:"-"`
	CacheScope  string   `json:"-"`
	LookupNames []string `json:"-"`
}

func (c Candidate) ID() string {
	return c.Kind + "\x00" + c.Provider + "\x00" + c.Name + "\x00" + c.URI
}

func ToolNamespace(declared, resourceURI, provider string) string {
	if declared != "" {
		return declared
	}
	if provider != "" {
		if uri, err := url.Parse(resourceURI); err == nil && uri.Scheme == "mcp" && uri.Host != "" && uri.User == nil {
			return uri.Host
		}
	}
	return DefaultNamespace
}

func SkillNamespace(declared, uriText string) string {
	if declared != "" {
		return declared
	}
	if ref, err := skill.ParseRef(uriText); err == nil && ref.ResourceURI != "" {
		uriText = ref.ResourceURI
	}
	if uri, err := url.Parse(uriText); err == nil && uri.Scheme == "skill" && uri.Host != "" && uri.User == nil && strings.HasSuffix(uri.Path, "/SKILL.md") {
		return uri.Host
	}
	return DefaultNamespace
}
