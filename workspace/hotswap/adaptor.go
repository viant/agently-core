package hotswap

import "context"

// LoaderFunc loads a resource by name.
type LoaderFunc[T any] func(ctx context.Context, name string) (T, error)

// SetFunc injects a loaded resource into the finder.
type SetFunc[T any] func(name string, v T)

// RemoveFunc removes a resource from the finder.
type RemoveFunc func(name string)

// Adaptor bridges a workspace loader and a finder using the Reloadable interface.
type Adaptor[T any] struct {
	load   LoaderFunc[T]
	set    SetFunc[T]
	remove RemoveFunc
}

// NewAdaptor creates a generic adaptor.
func NewAdaptor[T any](load LoaderFunc[T], set SetFunc[T], remove RemoveFunc) *Adaptor[T] {
	return &Adaptor[T]{load: load, set: set, remove: remove}
}

// OnChange handles a workspace resource change.
func (a *Adaptor[T]) OnChange(ctx context.Context, ch Change) error {
	switch ch.Action {
	case ActionDelete:
		a.remove(ch.Name)
	default:
		v, err := a.load(ctx, ch.Name)
		if err != nil {
			return err
		}
		a.set(ch.Name, v)
	}
	return nil
}
