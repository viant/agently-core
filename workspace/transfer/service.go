package transfer

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/viant/agently-core/workspace"
)

// Options controls which resource kinds are transferred and how.
type Options struct {
	Kinds   []string // kinds to transfer (nil = all known kinds)
	Replace bool     // overwrite existing in dest (default true)
}

// Result summarises a transfer operation.
type Result struct {
	Copied  int
	Skipped int
	Errors  []error
}

// Transfer copies resources from src to dst for the specified kinds.
// When opts is nil the defaults are: all kinds, replace=true.
func Transfer(ctx context.Context, src, dst workspace.Store, opts *Options) (*Result, error) {
	if src == nil || dst == nil {
		return nil, fmt.Errorf("src and dst stores must be non-nil")
	}
	if opts == nil {
		opts = &Options{Replace: true}
	}
	kinds := opts.Kinds
	if len(kinds) == 0 {
		kinds = workspace.AllKinds()
	}

	res := &Result{}
	for _, kind := range kinds {
		names, err := src.List(ctx, kind)
		if err != nil {
			if isNotExist(err) {
				continue
			}
			res.Errors = append(res.Errors, fmt.Errorf("list %s: %w", kind, err))
			continue
		}
		for _, name := range names {
			if !opts.Replace {
				exists, err := dst.Exists(ctx, kind, name)
				if err != nil {
					res.Errors = append(res.Errors, fmt.Errorf("exists %s/%s: %w", kind, name, err))
					continue
				}
				if exists {
					res.Skipped++
					continue
				}
			}
			data, err := src.Load(ctx, kind, name)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Errorf("load %s/%s: %w", kind, name, err))
				continue
			}
			if err := dst.Save(ctx, kind, name, data); err != nil {
				res.Errors = append(res.Errors, fmt.Errorf("save %s/%s: %w", kind, name, err))
				continue
			}
			res.Copied++
		}
	}
	return res, nil
}

// isNotExist returns true when the error indicates a missing directory or file.
func isNotExist(err error) bool {
	if os.IsNotExist(err) {
		return true
	}
	return strings.Contains(err.Error(), "no such file or directory")
}
