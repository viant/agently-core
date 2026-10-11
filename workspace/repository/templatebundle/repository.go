package templatebundle

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/agently-core/protocol/templatebundle"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/repository/base"
)

type Reader interface {
	LoadAll(context.Context) ([]*templatebundle.Bundle, error)
}

type Repository struct {
	reader Reader
	*base.Repository[templatebundle.Bundle]
}

// NewWithReader binds definition consumption to a host-owned reader.
func NewWithReader(reader Reader) *Repository { return &Repository{reader: reader} }

func New(fs afs.Service) *Repository {
	return &Repository{Repository: base.New[templatebundle.Bundle](fs, workspace.KindTemplateBundle)}
}

func NewWithStore(store workspace.Store) *Repository {
	return &Repository{Repository: base.NewWithStore[templatebundle.Bundle](store, workspace.KindTemplateBundle)}
}

func (r *Repository) LoadAll(ctx context.Context) ([]*templatebundle.Bundle, error) {
	if r != nil && r.reader != nil {
		return r.reader.LoadAll(ctx)
	}
	if r == nil || r.Repository == nil {
		return nil, fmt.Errorf("template bundle repository not configured")
	}
	names, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	sort.Strings(names)
	out := make([]*templatebundle.Bundle, 0, len(names))
	for _, name := range names {
		b, err := r.Load(ctx, name)
		if err != nil {
			return nil, err
		}
		if b == nil {
			continue
		}
		if err := b.Validate(); err != nil {
			return nil, fmt.Errorf("invalid template bundle %q: %w", strings.TrimSpace(name), err)
		}
		out = append(out, b)
	}
	return out, nil
}
