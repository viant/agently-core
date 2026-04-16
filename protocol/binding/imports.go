package binding

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/afs/file"
	"github.com/viant/afs/url"
)

// importPattern matches a whole line whose entire non-whitespace content is
// a single "$import(path)" directive, with optional surrounding quotes.
// Paths must be relative — absolute paths are rejected at resolution time.
//
// Examples that match:
//
//	$import(parts/persona.md)
//	    $import(shared/data_contract.md)
//	$import("shared/data_contract.md")
//
// Examples that DO NOT match:
//
//	See $import(foo.md) below.                   — directive not alone on line
//	$$import(escape.md)                           — double-dollar escape
//	$import()                                     — empty path (rejected at resolve)
var importPattern = regexp.MustCompile(`(?m)^[\t ]*\$import\(\s*["']?([^"')]+)["']?\s*\)[\t ]*$`)

// escapedPattern rewrites "$$import(" to a sentinel before expansion, and
// the sentinel is restored to "$import(" after. This is the escape hatch
// for prompt text that needs a literal "$import(" token.
var escapedPattern = regexp.MustCompile(`\$\$import\(`)

const (
	importSentinel = "\x00ESC_IMPORT\x00("
	maxImportDepth = 8
)

// ResolveTextImports expands `$import(path)` directives embedded in a text
// blob (typically a prompt template loaded from a .tmpl/.md file). Path
// resolution is relative to the directory of baseURI — the same convention
// used by the YAML-node resolver in workspace/service/meta/imports.go.
//
// Semantics:
//   - A directive must occupy an entire line (ignoring leading/trailing
//     whitespace). Inline text like "See $import(foo) below" is left alone.
//   - Paths are relative. Absolute paths are rejected.
//   - Imported files are resolved recursively to a bounded depth (8).
//   - A visiting set prevents cycles.
//   - `$$import(...)` is an escape: it becomes literal `$import(...)` in
//     the output.
//
// Ordering: this helper runs BEFORE any prompt template engine (Go text/
// template, velty, …). Imported content may itself carry `{{...}}` or
// `$foo` directives — those are handled by the subsequent engine pass.
//
// Returns the expanded text unchanged when no directive is present.
func ResolveTextImports(ctx context.Context, fs afs.Service, text, baseURI string) (string, error) {
	if fs == nil {
		fs = afs.New()
	}
	if !strings.Contains(text, "$import(") {
		return text, nil
	}
	return resolveTextImports(ctx, fs, text, baseURI, 0, map[string]bool{})
}

func resolveTextImports(
	ctx context.Context,
	fs afs.Service,
	text, baseURI string,
	depth int,
	visiting map[string]bool,
) (string, error) {
	if depth >= maxImportDepth {
		return "", fmt.Errorf("$import recursion depth exceeded (max=%d) — likely a cycle in prompt imports", maxImportDepth)
	}

	// Protect escaped $$import( tokens by swapping to a sentinel that the
	// main pattern won't match, then restore at the end.
	working := escapedPattern.ReplaceAllString(text, importSentinel)

	baseDir, _ := url.Split(normaliseURI(baseURI), file.Scheme)

	var firstErr error
	expanded := importPattern.ReplaceAllStringFunc(working, func(match string) string {
		if firstErr != nil {
			return match
		}
		// Re-match to extract the captured path group.
		sub := importPattern.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		importPath := strings.TrimSpace(sub[1])
		if importPath == "" {
			firstErr = fmt.Errorf("empty $import path in %s", baseURI)
			return match
		}
		if filepath.IsAbs(importPath) {
			firstErr = fmt.Errorf("$import path must be relative, got absolute: %q in %s", importPath, baseURI)
			return match
		}

		resolvedURI := joinBase(baseDir, importPath)
		if visiting[resolvedURI] {
			firstErr = fmt.Errorf("$import cycle detected at %s (chain entered %s twice)", baseURI, resolvedURI)
			return match
		}

		data, err := fs.DownloadWithURL(ctx, resolvedURI)
		if err != nil {
			firstErr = fmt.Errorf("$import %q (resolved %s) from %s: %w", importPath, resolvedURI, baseURI, err)
			return match
		}

		visiting[resolvedURI] = true
		child, err := resolveTextImports(ctx, fs, string(data), resolvedURI, depth+1, visiting)
		delete(visiting, resolvedURI)
		if err != nil {
			firstErr = err
			return match
		}
		return child
	})
	if firstErr != nil {
		return "", firstErr
	}

	// Restore escaped tokens.
	expanded = strings.ReplaceAll(expanded, importSentinel, "$import(")
	return expanded, nil
}

// normaliseURI ensures a URI has a scheme; bare OS paths become file://.
func normaliseURI(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return uri
	}
	if url.Scheme(uri, "") == "" {
		return file.Scheme + "://" + uri
	}
	return uri
}

// joinBase joins a base directory URI with a relative path, preferring
// URL-style join when the base carries a scheme (avoids OS path quirks on
// remote storages such as s3://) and filesystem join otherwise.
func joinBase(baseDir, relPath string) string {
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		return filepath.Clean(relPath)
	}
	if strings.Contains(baseDir, "://") {
		bd := strings.TrimRight(baseDir, "/")
		rp := strings.TrimLeft(relPath, "/")
		return bd + "/" + rp
	}
	joined := filepath.Clean(filepath.Join(baseDir, relPath))
	if url.Scheme(joined, "") == "" {
		return file.Scheme + "://" + joined
	}
	return joined
}
