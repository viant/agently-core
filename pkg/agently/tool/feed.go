package tool

import "github.com/viant/forge/backend/types"

// Feed describes a matched rule and extracted data rendered by the UI.
type Feed struct {
	ID string `json:"id" yaml:"id"`

	Data DataFeed `json:"dataFeed" yaml:"dataFeed"`

	UI *types.Container `json:"ui" yaml:"ui"`

	DataSources map[string]*types.DataSource `yaml:"dataSources,omitempty" json:"dataSources,omitempty"`

	Invoked bool `json:"-" yaml:"-"`
}

// DataFeed carries resolved value and the raw selector used to obtain it.
type DataFeed struct {
	Name        string      `json:"name" yaml:"name"`
	Data        interface{} `json:"data" yaml:"data"`
	RawSelector string      `json:"rawSelector" yaml:"rawSelector"`
}
