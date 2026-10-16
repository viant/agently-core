package template

import (
	"fmt"

	"github.com/viant/agently-core/protocol/lookup/overlay"
)

type Template struct {
	// ResourceURI is host-supplied admitted routing metadata, not authored authority.
	ResourceURI     string                     `json:"-" yaml:"-"`
	ID              string                     `yaml:"id" json:"id"`
	Name            string                     `yaml:"name" json:"name"`
	Description     string                     `yaml:"description,omitempty" json:"description,omitempty"`
	Format          string                     `yaml:"format,omitempty" json:"format,omitempty"`
	AppliesTo       []string                   `yaml:"appliesTo,omitempty" json:"appliesTo,omitempty"`
	Platforms       []string                   `yaml:"platforms,omitempty" json:"platforms,omitempty"`
	FormFactors     []string                   `yaml:"formFactors,omitempty" json:"formFactors,omitempty"`
	Surfaces        []string                   `yaml:"surfaces,omitempty" json:"surfaces,omitempty"`
	Instructions    string                     `yaml:"instructions,omitempty" json:"instructions,omitempty"`
	Fences          []Fence                    `yaml:"fences,omitempty" json:"fences,omitempty"`
	Schema          map[string]interface{}     `yaml:"schema,omitempty" json:"schema,omitempty"`
	Examples        []TemplateExample          `yaml:"examples,omitempty" json:"examples,omitempty"`
	LookupOverlays  []*overlay.Overlay         `yaml:"lookupOverlays,omitempty" json:"lookupOverlays,omitempty"`
	LookupLibraries []overlay.LibraryReference `yaml:"lookupLibraries,omitempty" json:"lookupLibraries,omitempty"`
}

type Fence struct {
	Lang        string                 `yaml:"lang" json:"lang"`
	Required    bool                   `yaml:"required,omitempty" json:"required,omitempty"`
	Repeatable  bool                   `yaml:"repeatable,omitempty" json:"repeatable,omitempty"`
	Order       int                    `yaml:"order,omitempty" json:"order,omitempty"`
	Description string                 `yaml:"description,omitempty" json:"description,omitempty"`
	Schema      map[string]interface{} `yaml:"schema,omitempty" json:"schema,omitempty"`
}

type TemplateExample struct {
	Title  string         `yaml:"title,omitempty" json:"title,omitempty"`
	Fences []FenceExample `yaml:"fences,omitempty" json:"fences,omitempty"`
}

type FenceExample struct {
	Lang string      `yaml:"lang" json:"lang"`
	Body interface{} `yaml:"body" json:"body"`
}

func (t *Template) Validate() error { return t.ValidateForParent(false) }

// ValidateForParent additionally requires stamped library refs when this
// template is itself loaded from an immutable parent revision.
func (t *Template) ValidateForParent(immutableParent bool) error {
	if t == nil {
		return fmt.Errorf("template was nil")
	}
	if t.ID == "" {
		return fmt.Errorf("template.id was empty")
	}
	if t.Name == "" {
		return fmt.Errorf("template.name was empty")
	}
	if err := overlay.ValidateLibraryReferences(t.LookupLibraries, immutableParent); err != nil {
		return fmt.Errorf("template.lookupLibraries: %w", err)
	}
	return nil
}
