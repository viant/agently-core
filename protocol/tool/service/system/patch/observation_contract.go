package patch

import (
	"context"
	"fmt"
	"strings"

	sgdiff "github.com/sourcegraph/go-diff/diff"
	"github.com/viant/afs"
	"github.com/viant/agently-core/protocol/tool/service/observation"
)

func validatePatchObservations(ctx context.Context, sess *Session, patchText, workdir string) error {
	if !observation.IsEnforced(ctx) {
		return nil
	}
	paths, err := observedPathsForPatch(patchText, workdir)
	if err != nil {
		return err
	}
	return validateObservedPaths(ctx, sess, paths)
}

func validateObservedPaths(ctx context.Context, sess *Session, paths []string) error {
	if !observation.IsEnforced(ctx) {
		return nil
	}
	seen := map[string]struct{}{}
	fs := afs.New()
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		key := observation.CanonicalURI(path)
		if key == "" {
			return fmt.Errorf("target file must be read with resources:read before patching")
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if sess != nil && sess.HasCreatedChange(path) {
			continue
		}
		data, err := fs.DownloadWithURL(ctx, path)
		if err != nil {
			return fmt.Errorf("target file must be read with resources:read before patching")
		}
		if err := observation.VerifyCurrent(ctx, path, data); err != nil {
			return err
		}
	}
	return nil
}

func observedPathsForPatch(patchText, workdir string) ([]string, error) {
	patchText = strings.TrimSpace(patchText)
	if patchText == "" {
		return nil, fmt.Errorf("patch is required")
	}
	if strings.HasPrefix(patchText, "*** Begin Patch") {
		hunks, err := Parse(patchText)
		if err != nil {
			return nil, fmt.Errorf("parse patch: %w", err)
		}
		return observedPathsForParsedHunks(hunks, workdir)
	}
	mfd, err := sgdiff.ParseMultiFileDiff([]byte(patchText))
	if err != nil {
		return nil, fmt.Errorf("parse patch: %w", err)
	}
	if len(mfd) == 0 {
		return nil, fmt.Errorf("parse patch: no patch hunks found")
	}
	var paths []string
	for _, fd := range mfd {
		if fd == nil || fd.OrigName == "/dev/null" {
			continue
		}
		orig := strings.TrimPrefix(fd.OrigName, "a/")
		path, err := resolvePath(orig, workdir)
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func observedPathsForParsedHunks(hunks []Hunk, workdir string) ([]string, error) {
	var paths []string
	for _, hunk := range hunks {
		switch h := hunk.(type) {
		case AddFile:
			continue
		case DeleteFile:
			path, err := resolvePath(h.Path, workdir)
			if err != nil {
				return nil, err
			}
			paths = append(paths, path)
		case UpdateFile:
			path, err := resolvePath(h.Path, workdir)
			if err != nil {
				return nil, err
			}
			paths = append(paths, path)
		}
	}
	return paths, nil
}
