package embedder

import (
	"github.com/viant/agently-core/genai/embedder/provider"
	"github.com/viant/agently-core/workspace/loader/fs"
)

// Service provides model data access operations
type Service struct {
	*fs.Service[provider.Config]
}

// New creates a new model service instance
func New(options ...fs.Option[provider.Config]) *Service {
	ret := &Service{
		Service: fs.New[provider.Config](decodeYaml, options...),
	}
	return ret
}
