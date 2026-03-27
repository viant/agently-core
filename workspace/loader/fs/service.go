package fs

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/agently-core/workspace"
	wscodec "github.com/viant/agently-core/workspace/codec"
	meta "github.com/viant/agently-core/workspace/service/meta"
	yml "github.com/viant/agently-core/workspace/service/meta/yml"
	"gopkg.in/yaml.v3"
)

const (
	defaultExtension = ".yaml"
)

// Service provides model data access operations
type Service[T any] struct {
	decoderFunc      DecodeFunc[T]
	metaService      *meta.Service
	defaultExtension string
	store            workspace.Store
	storeKind        string
}

func (s *Service[T]) List(ctx context.Context, URL string) ([]*T, error) {
	candidates, err := s.metaService.List(ctx, URL)
	if err != nil {
		return nil, fmt.Errorf("failed to list configs from %s: %w", URL, err)
	}
	var result = make([]*T, 0)
	for _, candidate := range candidates {
		config, err := s.Load(ctx, candidate)
		if err != nil {
			return nil, fmt.Errorf("failed to load configuration from %s: %w", candidate, err)
		}
		result = append(result, config)
	}
	return result, nil
}

// Load loads a model from the specified URL
func (s *Service[T]) Load(ctx context.Context, URL string) (*T, error) {
	// Store-aware path: read bytes from store then decode.
	if s.store != nil && s.storeKind != "" {
		name := extractName(URL)
		data, err := s.store.Load(ctx, s.storeKind, name)
		if err != nil {
			return nil, fmt.Errorf("failed to load configuration %s/%s: %w", s.storeKind, name, err)
		}
		var node yaml.Node
		if err := wscodec.DecodeData(name+s.defaultExtension, data, &node); err != nil {
			return nil, fmt.Errorf("failed to parse configuration %s/%s: %w", s.storeKind, name, err)
		}
		var t T
		if err := s.decoderFunc((*yml.Node)(&node), &t); err != nil {
			return nil, fmt.Errorf("failed to decode configuration %s/%s: %w", s.storeKind, name, err)
		}
		if validator, ok := any(&t).(Validator); ok {
			if err := validator.Validate(ctx); err != nil {
				return nil, fmt.Errorf("invalid configuration %s/%s: %w", s.storeKind, name, err)
			}
		}
		return &t, nil
	}

	ext := filepath.Ext(URL)
	if ext == "" {
		URL += s.defaultExtension
	}

	var node yaml.Node
	if err := s.metaService.Load(ctx, URL, &node); err != nil {
		return nil, fmt.Errorf("failed to load configuration from %s: %w", URL, err)
	}

	var t T
	if err := s.decoderFunc((*yml.Node)(&node), &t); err != nil {
		return nil, fmt.Errorf("failed to decode configuration from %s: %w", URL, err)
	}

	ptrT := &t
	// Use type assertion with interface{} first to avoid compile-time type checking issues with generics
	if validator, ok := any(ptrT).(Validator); ok {
		// Validate model
		if err := validator.Validate(ctx); err != nil {
			return nil, fmt.Errorf("invalid model configuration from %s: %w", URL, err)
		}
	}

	return &t, nil
}

// extractName derives a bare resource name from a URL or path.
func extractName(URL string) string {
	base := filepath.Base(URL)
	ext := strings.ToLower(filepath.Ext(base))
	if ext == ".yaml" || ext == ".yml" || ext == ".json" {
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return base
}

// New creates a new model service instance
func New[T any](decoderFunc DecodeFunc[T], options ...Option[T]) *Service[T] {
	ret := &Service[T]{
		metaService:      meta.New(afs.New(), ""),
		defaultExtension: defaultExtension,
		decoderFunc:      decoderFunc,
	}
	for _, opt := range options {
		opt(ret)
	}
	return ret
}
