package window

import (
	"context"
	"github.com/viant/afs"
)

// LoaderOptions injects a host-owned filesystem through native window, import,
// assignment and descriptor loading. Check reports sticky access denials even
// when legacy branch probing or optional-asset loading suppresses an error.
type LoaderOptions struct {
	// SharedDefault permits a shared/main native root when no main/singleton alias exists.
	SharedDefault    bool
	FS               afs.Service
	AssetDirectories func(context.Context) (map[string]bool, error)
	Check            func() error
}

func (o LoaderOptions) filesystem() afs.Service {
	if o.FS != nil {
		return o.FS
	}
	return afs.New()
}
func (o LoaderOptions) check() error {
	if o.Check != nil {
		return o.Check()
	}
	return nil
}
