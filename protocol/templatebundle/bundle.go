package templatebundle

import "fmt"

type Bundle struct {
	ID          string   `yaml:"id" json:"id"`
	Title       string   `yaml:"title,omitempty" json:"title,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Priority    int      `yaml:"priority,omitempty" json:"priority,omitempty"`
	Templates   []string `yaml:"templates,omitempty" json:"templates,omitempty"`
}

func (b *Bundle) Validate() error {
	if b == nil {
		return fmt.Errorf("template bundle was nil")
	}
	if b.ID == "" {
		return fmt.Errorf("template bundle id was empty")
	}
	if len(b.Templates) == 0 {
		return fmt.Errorf("template bundle templates were empty")
	}
	return nil
}
