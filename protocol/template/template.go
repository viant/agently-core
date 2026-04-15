package template

import "fmt"

type Template struct {
	ID           string                   `yaml:"id" json:"id"`
	Name         string                   `yaml:"name" json:"name"`
	Description  string                   `yaml:"description,omitempty" json:"description,omitempty"`
	Format       string                   `yaml:"format,omitempty" json:"format,omitempty"`
	AppliesTo    []string                 `yaml:"appliesTo,omitempty" json:"appliesTo,omitempty"`
	Platforms    []string                 `yaml:"platforms,omitempty" json:"platforms,omitempty"`
	FormFactors  []string                 `yaml:"formFactors,omitempty" json:"formFactors,omitempty"`
	Surfaces     []string                 `yaml:"surfaces,omitempty" json:"surfaces,omitempty"`
	Instructions string                   `yaml:"instructions,omitempty" json:"instructions,omitempty"`
	Schema       map[string]interface{}   `yaml:"schema,omitempty" json:"schema,omitempty"`
	Examples     []map[string]interface{} `yaml:"examples,omitempty" json:"examples,omitempty"`
}

func (t *Template) Validate() error {
	if t == nil {
		return fmt.Errorf("template was nil")
	}
	if t.ID == "" {
		return fmt.Errorf("template.id was empty")
	}
	if t.Name == "" {
		return fmt.Errorf("template.name was empty")
	}
	return nil
}
