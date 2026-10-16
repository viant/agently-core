package template

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/viant/afs"
	templ "github.com/viant/agently-core/protocol/template"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/repository/base"
)

type Reader interface {
	LoadAll(context.Context) ([]*templ.Template, error)
	Load(context.Context, string) (*templ.Template, error)
}

type Repository struct {
	reader Reader
	*base.Repository[templ.Template]
}

// NewWithReader binds definition consumption to a host-owned reader.
func NewWithReader(reader Reader) *Repository { return &Repository{reader: reader} }

func New(fs afs.Service) *Repository {
	return &Repository{Repository: base.New[templ.Template](fs, workspace.KindTemplate)}
}

func NewWithStore(store workspace.Store) *Repository {
	return &Repository{Repository: base.NewWithStore[templ.Template](store, workspace.KindTemplate)}
}

func (r *Repository) LoadAll(ctx context.Context) ([]*templ.Template, error) {
	if r != nil && r.reader != nil {
		return r.reader.LoadAll(ctx)
	}
	if r == nil || r.Repository == nil {
		return nil, fmt.Errorf("template repository not configured")
	}
	names, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	sort.Strings(names)
	out := make([]*templ.Template, 0, len(names))
	for _, name := range names {
		tpl, err := r.Load(ctx, name)
		if err != nil {
			return nil, err
		}
		if tpl == nil {
			continue
		}
		if err := tpl.Validate(); err != nil {
			return nil, fmt.Errorf("invalid template %q: %w", strings.TrimSpace(name), err)
		}
		out = append(out, tpl)
	}
	return out, nil
}

func (r *Repository) Load(ctx context.Context, name string) (*templ.Template, error) {
	if r != nil && r.reader != nil {
		return r.reader.Load(ctx, name)
	}
	if r == nil || r.Repository == nil {
		return nil, fmt.Errorf("template repository not configured")
	}
	return r.Repository.Load(ctx, name)
}

// AdmitSelected freshly reads delegated definitions; explicit filesystem repositories retain their original behavior.
func (r *Repository) AdmitSelected(ctx context.Context, key string, selected *templ.Template) (*templ.Template, error) {
	if r != nil && r.reader != nil {
		return r.reader.Load(ctx, key)
	}
	return selected, nil
}

func (r *Repository) LoadFrom(ctx context.Context, key, parentURI, provider string) (*templ.Template, error) {
	if r != nil && r.reader != nil {
		if reader, ok := r.reader.(interface {
			LoadFrom(context.Context, string, string, string) (*templ.Template, error)
		}); ok {
			return reader.LoadFrom(ctx, key, parentURI, provider)
		}
	}
	return r.Load(ctx, key)
}
