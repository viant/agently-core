package tool

import (
	"encoding/json"
	"errors"
	"strings"

	mcpname "github.com/viant/agently-core/pkg/mcpname"
	"github.com/viant/forge/backend/types"
)

type FeedSpec struct {
	ID          string          `yaml:"id,omitempty" json:"id,omitempty"`
	Title       string          `yaml:"title,omitempty" json:"title,omitempty"`
	Priority    int             `yaml:"priority,omitempty" json:"priority,omitempty"`
	Match       MatchSpec       `yaml:"match" json:"match"`
	Activation  ActivationSpec  `yaml:"activation" json:"activation"`
	DataSources DataSources     `yaml:"dataSource,omitempty" json:"dataSource,omitempty"`
	UI          types.Container `yaml:"ui" json:"ui"`
}

type DataSources map[string]*DataSource

type DataSource struct {
	types.DataSource `yaml:",inline"`
	Source           string `json:"source" yaml:"source"`
	Name             string `json:"name" yaml:"name"`
}

func (d *DataSource) HasSource() bool {
	return d != nil && d.Source != ""
}

func (s DataSources) FeedDataSource() (*DataSource, error) {
	for key, candidate := range s {
		if candidate != nil && candidate.HasSource() {
			candidate.Name = key
			return candidate, nil
		}
	}
	return nil, errors.New("no sources defined in datasource found")
}

func (s DataSources) Normalize() map[string]*types.DataSource {
	result := make(map[string]*types.DataSource, len(s))
	for key := range s {
		candidate := s[key].DataSource
		result[key] = &candidate
	}
	return result
}

func (s DataSources) Transform(hash string) (map[string]*types.DataSource, map[string]string) {
	if len(s) == 0 {
		return map[string]*types.DataSource{}, map[string]string{}
	}
	nameMap := make(map[string]string, len(s))
	for name := range s {
		nameMap[name] = name + hash
	}
	out := make(map[string]*types.DataSource, len(s))
	for name, ds := range s {
		if ds == nil {
			continue
		}
		copied := ds.DataSource
		if newRef, ok := nameMap[copied.DataSourceRef]; ok {
			copied.DataSourceRef = newRef
		}
		out[nameMap[name]] = &copied
	}
	return out, nameMap
}

func RewriteContainerDataSourceRefs(c types.Container, nameMap map[string]string) types.Container {
	var result types.Container
	raw, err := json.Marshal(c)
	if err != nil {
		return c
	}
	var any interface{}
	if err = json.Unmarshal(raw, &any); err != nil {
		return c
	}
	var rewrite func(node interface{})
	rewrite = func(node interface{}) {
		switch v := node.(type) {
		case map[string]interface{}:
			if refVal, ok := v["dataSourceRef"].(string); ok {
				if newName, ok2 := nameMap[refVal]; ok2 {
					v["dataSourceRef"] = newName
				}
			}
			for _, child := range v {
				rewrite(child)
			}
		case []interface{}:
			for _, item := range v {
				rewrite(item)
			}
		}
	}
	rewrite(any)
	if buf, err := json.Marshal(any); err == nil {
		_ = json.Unmarshal(buf, &result)
		return result
	}
	return c
}

type MatchSpec struct {
	Service string `yaml:"service,omitempty" json:"service,omitempty"`
	Method  string `yaml:"method,omitempty" json:"method,omitempty"`
}

func (m *MatchSpec) Name() mcpname.Name {
	return mcpname.NewName(m.Service, m.Method)
}

type ActivationSpec struct {
	Kind    string                 `yaml:"kind,omitempty" json:"kind,omitempty"`
	Scope   string                 `yaml:"scope,omitempty" json:"scope,omitempty"`
	Service string                 `yaml:"service,omitempty" json:"service,omitempty"`
	Method  string                 `yaml:"method,omitempty" json:"method,omitempty"`
	Args    map[string]interface{} `yaml:"args,omitempty" json:"args,omitempty"`
}

type FeedSpecs []*FeedSpec

func (s FeedSpecs) Index() map[mcpname.Name]*FeedSpec {
	result := make(map[mcpname.Name]*FeedSpec)
	for _, feed := range s {
		if feed == nil {
			continue
		}
		name := feed.Match.Name()
		result[name] = feed
		lc := mcpname.Name(strings.ToLower(string(name)))
		if lc != name {
			result[lc] = feed
		}
	}
	return result
}

func (s FeedSpecs) MatchSpec() []MatchSpec {
	if len(s) == 0 {
		return nil
	}
	out := make([]MatchSpec, 0, len(s))
	for _, fs := range s {
		if fs == nil {
			continue
		}
		out = append(out, MatchSpec{
			Service: strings.TrimSpace(fs.Match.Service),
			Method:  strings.TrimSpace(fs.Match.Method),
		})
	}
	return out
}

func (s FeedSpecs) Matches(name mcpname.Name) bool {
	for _, m := range s.MatchSpec() {
		if m.Matches(name) {
			return true
		}
	}
	return false
}

func (m MatchSpec) Matches(name mcpname.Name) bool {
	ms := strings.TrimSpace(m.Service)
	mm := strings.TrimSpace(m.Method)
	if ms == "" || mm == "" {
		return false
	}
	serviceMatches := ms == "*" || strings.EqualFold(ms, name.Service())
	methodMatches := mm == "*" || strings.EqualFold(mm, name.Method())
	return serviceMatches && methodMatches
}

func (f *FeedSpec) InvokeServiceMethod() (string, string) {
	if f == nil {
		return "", ""
	}
	svc := strings.TrimSpace(f.Activation.Service)
	mth := strings.TrimSpace(f.Activation.Method)
	if svc == "" {
		svc = strings.TrimSpace(f.Match.Service)
	}
	if mth == "" {
		mth = strings.TrimSpace(f.Match.Method)
	}
	return svc, mth
}

func (f *FeedSpec) ShallInvokeTool() bool {
	if f == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(f.Activation.Kind), "tool_call")
}

func (f *FeedSpec) ShallUseHistory() bool {
	if f == nil {
		return false
	}
	k := strings.TrimSpace(f.Activation.Kind)
	return k == "" || strings.EqualFold(k, "history")
}
