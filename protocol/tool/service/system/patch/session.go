package patch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "embed"

	"github.com/google/uuid"
	sgdiff "github.com/sourcegraph/go-diff/diff"
	"github.com/viant/afs"
	"github.com/viant/afs/file"
	afsurl "github.com/viant/afs/url"
)

type DiffResult struct {
	Patch string
	Stats DiffStats
}

var ErrNoChange = errors.New("no change between old and new")

// ──────────────────────────────────────────────────────────────────────────────
// Session engine
// ──────────────────────────────────────────────────────────────────────────────

type Action string

const (
	Delete Action = "delete"
	Move   Action = "move"
	Update Action = "update"
	Add    Action = "add"
)

type Session struct {
	ID      string
	fs      afs.Service
	tempDir string
	// proactive change tracking
	changes   []*changeEntry
	byCurrent map[string]*changeEntry
	byOrigin  map[string]*changeEntry
	order     []*changeEntry
	committed bool
	mu        sync.Mutex // guards committed flag and rollbacks slice
}

func NewSession() (*Session, error) {
	fs := afs.New()
	ctx := context.Background()

	// Create a unique temporary directory using the OS-reported temp dir so that
	// the location can be overridden in constrained execution environments
	// via the TMPDIR environment variable. The original implementation relied
	// on the hard-coded /tmp path which may not be writable on some systems
	// (e.g. sandboxed CI runners). By switching to os.TempDir we respect the
	// host configuration while preserving the file:// scheme expected by the
	// rest of the code.

	baseTempDir := os.TempDir()
	if baseTempDir == "" {
		baseTempDir = "/tmp" // Fallback to the conventional location
	}

	// Always build a valid file:// URI.
	//
	// os.TempDir() can return either an absolute path ("/tmp") or a relative path
	// ("tmp") depending on environment and OS. A valid file URL for an absolute
	// path must have three slashes: file:///tmp/...
	//
	// We therefore join using afsurl.Join which normalizes slashes and preserves the
	// file scheme, instead of string formatting.
	tmp := afsurl.Join("file:///"+strings.TrimLeft(baseTempDir, string(os.PathSeparator)), fmt.Sprintf("onpatch-%s", uuid.NewString()))
	if err := fs.Create(ctx, tmp, file.DefaultDirOsMode, true); err != nil {
		return nil, err
	}

	return &Session{ID: filepath.Base(tmp), tempDir: tmp, fs: fs,
		changes:   []*changeEntry{},
		byCurrent: map[string]*changeEntry{},
		byOrigin:  map[string]*changeEntry{},
		order:     []*changeEntry{},
	}, nil
}

// NewSessionFor creates a session with backups stored under a conversation-scoped folder.
// The layout is: file://<tmp>/agently/<convID>/onpatch-<uuid>
func NewSessionFor(convID string) (*Session, error) {
	fs := afs.New()
	ctx := context.Background()
	baseTempDir := os.TempDir()
	if baseTempDir == "" {
		baseTempDir = "/tmp"
	}
	safe := sanitizeID(convID)
	parent := afsurl.Join("file:///"+strings.TrimLeft(baseTempDir, string(os.PathSeparator)), "agently", safe)
	if err := fs.Create(ctx, parent, file.DefaultDirOsMode, true); err != nil {
		return nil, err
	}
	tmp := afsurl.Join(parent, fmt.Sprintf("onpatch-%s", uuid.NewString()))
	if err := fs.Create(ctx, tmp, file.DefaultDirOsMode, true); err != nil {
		return nil, err
	}
	return &Session{ID: filepath.Base(tmp), tempDir: tmp, fs: fs,
		changes:   []*changeEntry{},
		byCurrent: map[string]*changeEntry{},
		byOrigin:  map[string]*changeEntry{},
		order:     []*changeEntry{},
	}, nil
}

func sanitizeID(in string) string {
	in = strings.TrimSpace(in)
	if in == "" {
		return "_"
	}
	b := strings.Builder{}
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// backup now stores **one snapshot per invocation** using a timestamp‑suffix to
// avoid overwriting when the same file is modified multiple times.
func (s *Session) backup(ctx context.Context, path string) (string, error) {
	data, err := s.fs.DownloadWithURL(ctx, path)
	if err != nil {
		return "", err
	}
	rel := strings.TrimPrefix(path, string(os.PathSeparator))
	unique := fmt.Sprintf("%s.%d.bak", rel, time.Now().UnixNano())
	dst := afsurl.Join(s.tempDir, unique)

	parent, _ := afsurl.Split(dst, file.Scheme)
	if err := s.fs.Create(ctx, parent, file.DefaultDirOsMode, true); err != nil {
		return "", err
	}

	if err := s.fs.Upload(ctx, dst, file.DefaultFileOsMode, bytes.NewReader(data)); err != nil {
		return "", err
	}
	return dst, nil
}

func (s *Session) assertActive() error {
	if s.committed {
		return errors.New("session already committed")
	}
	return nil
}

func (s *Session) Delete(ctx context.Context, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.assertActive(); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	exists, err := s.fs.Exists(ctx, path)
	if err != nil || !exists {
		return fmt.Errorf("delete: %w", err)
	}
	backup, err := s.backup(ctx, path)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if err := s.fs.Delete(ctx, path); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	s.trackDelete(ctx, path, backup)
	return nil
}

func (s *Session) Move(ctx context.Context, src, dst string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.assertActive(); err != nil {
		return err
	}
	exists, err := s.fs.Exists(ctx, src)
	if err != nil || !exists {
		return err
	}

	parent, _ := afsurl.Split(dst, file.Scheme)
	if err := s.fs.Create(ctx, parent, file.DefaultDirOsMode, true); err != nil {
		return err
	}

	if err := s.fs.Move(ctx, src, dst); err != nil {
		return err
	}
	s.trackMove(src, dst)
	return nil
}

func (s *Session) Update(ctx context.Context, path string, newData []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.assertActive(); err != nil {
		return err
	}
	exists, err := s.fs.Exists(ctx, path)
	if err != nil || !exists {
		return err
	}
	backup, err := s.backup(ctx, path)
	if err != nil {
		return err
	}
	if err := s.fs.Upload(ctx, path, file.DefaultFileOsMode, bytes.NewReader(newData)); err != nil {
		return err
	}
	s.trackUpdate(ctx, path, backup)
	return nil
}

func (s *Session) Replace(ctx context.Context, path, oldText, newText string, replaceAll bool, expectedOccurrences int) (int, DiffStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.assertActive(); err != nil {
		return 0, DiffStats{}, err
	}
	if oldText == "" {
		return 0, DiffStats{}, errors.New("old text must be non-empty")
	}
	if oldText == newText {
		return 0, DiffStats{}, errors.New("old and new text are identical")
	}
	exists, err := s.fs.Exists(ctx, path)
	if err != nil || !exists {
		if err != nil {
			return 0, DiffStats{}, err
		}
		return 0, DiffStats{}, fmt.Errorf("replace: file does not exist: %s", path)
	}
	oldData, err := s.fs.DownloadWithURL(ctx, path)
	if err != nil {
		return 0, DiffStats{}, err
	}
	oldContent := string(oldData)
	occurrences := strings.Count(oldContent, oldText)
	if occurrences == 0 {
		return 0, DiffStats{}, fmt.Errorf("replace: old text not found in %s", path)
	}
	if expectedOccurrences < 0 {
		return 0, DiffStats{}, errors.New("expectedOccurrences must be non-negative")
	}
	if expectedOccurrences > 0 && occurrences != expectedOccurrences {
		return 0, DiffStats{}, fmt.Errorf("replace: expected %d occurrence(s), found %d in %s", expectedOccurrences, occurrences, path)
	}
	if !replaceAll && occurrences != 1 {
		return 0, DiffStats{}, fmt.Errorf("replace: found %d occurrences in %s; provide more context or set replaceAll", occurrences, path)
	}

	limit := 1
	if replaceAll {
		limit = -1
	}
	newContent := strings.Replace(oldContent, oldText, newText, limit)
	newData := []byte(newContent)
	_, stats, err := GenerateDiff(oldData, newData, path, 3)
	if err != nil {
		return 0, DiffStats{}, err
	}

	backup, err := s.backup(ctx, path)
	if err != nil {
		return 0, DiffStats{}, err
	}
	if err := s.fs.Upload(ctx, path, file.DefaultFileOsMode, bytes.NewReader(newData)); err != nil {
		return 0, DiffStats{}, err
	}
	s.trackUpdate(ctx, path, backup)
	if replaceAll {
		return occurrences, stats, nil
	}
	return 1, stats, nil
}

func (s *Session) Add(ctx context.Context, path string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.assertActive(); err != nil {
		return err
	}
	parent, _ := afsurl.Split(path, file.Scheme)
	if err := s.fs.Create(ctx, parent, file.DefaultDirOsMode, true); err != nil {
		return err
	}

	if err := s.fs.Upload(ctx, path, file.DefaultFileOsMode, bytes.NewReader(data)); err != nil {
		return err
	}
	s.trackAdd(ctx, path)
	return nil
}

func (s *Session) Rollback(ctx ...context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If already committed, nothing to rollback
	if s.committed {
		return nil
	}

	// Use provided context or create a new one
	var c context.Context
	if len(ctx) > 0 {
		c = ctx[0]
	} else {
		c = context.Background()
	}

	var rollbackErrors []error

	// Process changes in reverse order
	for i := len(s.order) - 1; i >= 0; i-- {
		e := s.order[i]
		if e == nil || !e.alive || e.kind == "" {
			continue
		}
		switch e.kind {
		case "create":
			if e.url != "" {
				if err := s.fs.Delete(c, e.url); err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback delete %s: %w", e.url, err))
				}
			}
		case "updated":
			// move back if needed
			if e.url != "" && e.orig != "" && e.url != e.orig {
				// if current exists, move back
				if exists, _ := s.fs.Exists(c, e.url); exists {
					if err := s.fs.Move(c, e.url, e.orig); err != nil {
						rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback move %s->%s: %v", e.url, e.orig, err))
					}
				}
			}
			// restore content if we have backup
			if e.backup != "" && e.orig != "" {
				data, err := s.fs.DownloadWithURL(c, e.backup)
				if err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback read backup %s: %v", e.backup, err))
					continue
				}
				parent, _ := afsurl.Split(e.orig, file.Scheme)
				if err := s.fs.Create(c, parent, file.DefaultDirOsMode, true); err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback mkdir %s: %v", parent, err))
					continue
				}
				if err := s.fs.Upload(c, e.orig, file.DefaultFileOsMode, bytes.NewReader(data)); err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback restore %s: %v", e.orig, err))
					continue
				}
			}
		case "delete":
			if e.backup != "" && e.orig != "" {
				data, err := s.fs.DownloadWithURL(c, e.backup)
				if err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback read backup %s: %v", e.backup, err))
					continue
				}
				parent, _ := afsurl.Split(e.orig, file.Scheme)
				if err := s.fs.Create(c, parent, file.DefaultDirOsMode, true); err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback mkdir %s: %v", parent, err))
					continue
				}
				if err := s.fs.Upload(c, e.orig, file.DefaultFileOsMode, bytes.NewReader(data)); err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback restore %s: %v", e.orig, err))
					continue
				}
			}
		}
	}

	// Clean up temporary directory
	if err := s.fs.Delete(c, s.tempDir); err != nil {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback cleanup: %w", err))
	}

	// Clear rollbacks regardless of errors to prevent re-attempting
	s.changes = nil
	s.byCurrent = map[string]*changeEntry{}
	s.byOrigin = map[string]*changeEntry{}
	s.order = nil

	// If there were any errors, return a combined error message
	if len(rollbackErrors) > 0 {
		var errMsg strings.Builder
		errMsg.WriteString("rollback encountered errors:\n")
		for i, err := range rollbackErrors {
			errMsg.WriteString(fmt.Sprintf("  %d. %s\n", i+1, err.Error()))
		}
		return errors.New(errMsg.String())
	}

	return nil
}

func (s *Session) Commit(ctx ...context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.committed {
		return nil
	}

	// Use provided context or create a new one
	var c context.Context
	if len(ctx) > 0 {
		c = ctx[0]
	} else {
		c = context.Background()
	}

	s.committed = true
	s.changes = nil
	s.byCurrent = map[string]*changeEntry{}
	s.byOrigin = map[string]*changeEntry{}
	s.order = nil
	if err := s.fs.Delete(c, s.tempDir); err != nil {
		return fmt.Errorf("commit cleanup: %w", err)
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Patch application (F‑03)
// ──────────────────────────────────────────────────────────────────────────────

func (s *Session) ApplyPatch(ctx context.Context, patchText string, directory ...string) error {
	// Get directory parameter or default to empty string
	dir := ""
	if len(directory) > 0 {
		dir = directory[0]
	}

	patchText = strings.TrimSpace(patchText)
	// Check if the patch is in the new format
	if strings.HasPrefix(patchText, "*** Begin Patch") {
		// Use the new parser
		hunks, err := Parse(patchText)
		if err != nil {
			return fmt.Errorf("parse patch: %w", err)
		}
		return s.applyParsedHunks(ctx, hunks, dir)
	}

	// Original format handling
	mfd, err := sgdiff.ParseMultiFileDiff([]byte(patchText))
	if err != nil {
		return fmt.Errorf("parse patch: %w", err)
	}
	if len(mfd) == 0 {
		return fmt.Errorf("parse patch: no patch hunks found")
	}
	for _, fd := range mfd {
		orig := strings.TrimPrefix(fd.OrigName, "a/")
		newer := strings.TrimPrefix(fd.NewName, "b/")

		// Resolve paths based on directory parameter
		if fd.OrigName != "/dev/null" {
			orig, err = resolvePath(orig, dir)
			if err != nil {
				return err
			}
		}
		if newer != "/dev/null" {
			newer, err = resolvePath(newer, dir)
			if err != nil {
				return err
			}
		}

		switch {
		case fd.NewName != "/dev/null" && fd.OrigName == "/dev/null":
			var buf bytes.Buffer
			if err := applyHunks(nil, fd.Hunks, &buf); err != nil {
				return err
			}
			if err := s.Add(ctx, newer, buf.Bytes()); err != nil {
				return err
			}
		case fd.NewName == "/dev/null" && fd.OrigName != "/dev/null":
			if err := s.Delete(ctx, orig); err != nil {
				return err
			}
		case orig != newer && len(fd.Hunks) == 0:
			if err := s.Move(ctx, orig, newer); err != nil {
				return err
			}
		default:
			oldData, err := s.fs.DownloadWithURL(ctx, orig)
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			if err := applyHunks(oldData, fd.Hunks, &buf); err != nil {
				return err
			}
			target := orig
			if orig != newer {
				if err := s.Move(ctx, orig, newer); err != nil {
					return err
				}
				target = newer
			}
			if err := s.Update(ctx, target, buf.Bytes()); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolvePath resolves a patch path against workdir without changing caller
// intent. Relative paths are resolved under workdir. Absolute paths are accepted
// only when they already point inside workdir.
func resolvePath(path, directory string) (string, error) {
	path = strings.TrimSpace(path)
	directory = strings.TrimSpace(directory)

	if path == "" {
		return "", fmt.Errorf("patch path is empty")
	}
	if directory == "" {
		return path, nil
	}
	if strings.Contains(directory, "://") {
		return resolveURLPath(path, directory)
	}
	return resolveFilesystemPath(path, directory)
}

func resolveFilesystemPath(path, directory string) (string, error) {
	base, err := filepath.Abs(filepath.Clean(directory))
	if err != nil {
		return "", fmt.Errorf("resolve workdir %q: %w", directory, err)
	}

	var target string
	if strings.Contains(path, "://") {
		baseURL, targetPath := afsurl.Base(path, file.Scheme)
		if baseURL != "file://localhost" && baseURL != "file://" {
			return "", fmt.Errorf("patch path %q is outside workdir %q", path, directory)
		}
		target = filepath.Clean(targetPath)
	} else if filepath.IsAbs(path) {
		target = filepath.Clean(path)
	} else {
		if relativePathEscapesBase(path) {
			return "", fmt.Errorf("patch path %q resolves outside workdir %q", path, directory)
		}
		target = filepath.Clean(filepath.Join(base, path))
	}

	target, err = filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve patch path %q: %w", path, err)
	}
	if !containsFilesystemPath(base, target) {
		return "", fmt.Errorf("patch path %q resolves outside workdir %q", path, directory)
	}
	return target, nil
}

func resolveURLPath(path, directory string) (string, error) {
	baseURL, basePath := afsurl.Base(directory, file.Scheme)
	basePath = cleanURLPath(basePath)

	var targetBaseURL string
	var targetPath string
	if strings.Contains(path, "://") {
		targetBaseURL, targetPath = afsurl.Base(path, file.Scheme)
		if targetBaseURL != baseURL {
			return "", fmt.Errorf("patch path %q is outside workdir %q", path, directory)
		}
		targetPath = cleanURLPath(targetPath)
	} else {
		if pathpkg.IsAbs(path) {
			if baseURL != "file://localhost" && baseURL != "file://" {
				return "", fmt.Errorf("patch path %q is outside workdir %q", path, directory)
			}
			targetPath = cleanURLPath(path)
		} else {
			if relativePathEscapesBase(path) {
				return "", fmt.Errorf("patch path %q resolves outside workdir %q", path, directory)
			}
			targetPath = cleanURLPath(pathpkg.Join(basePath, filepath.ToSlash(path)))
		}
		targetBaseURL = baseURL
	}

	if !containsURLPath(basePath, targetPath) {
		return "", fmt.Errorf("patch path %q resolves outside workdir %q", path, directory)
	}
	if targetPath == "/" {
		return afsurl.Join(targetBaseURL), nil
	}
	return afsurl.Join(targetBaseURL, targetPath), nil
}

func cleanURLPath(value string) string {
	clean := pathpkg.Clean("/" + strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(value)), "/"))
	if clean == "." || clean == "" {
		return "/"
	}
	return clean
}

func relativePathEscapesBase(value string) bool {
	depth := 0
	for _, segment := range strings.Split(filepath.ToSlash(value), "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			if depth == 0 {
				return true
			}
			depth--
		default:
			depth++
		}
	}
	return false
}

func containsFilesystemPath(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func containsURLPath(base, target string) bool {
	base = cleanURLPath(base)
	target = cleanURLPath(target)
	if base == "/" {
		return strings.HasPrefix(target, "/")
	}
	return target == base || strings.HasPrefix(target, base+"/")
}

// applyParsedHunks applies the hunks parsed by the new parser
func (s *Session) applyParsedHunks(ctx context.Context, hunks []Hunk, directory string) error {
	for _, hunk := range hunks {
		switch h := hunk.(type) {
		case AddFile:
			// Resolve path based on directory parameter
			path, err := resolvePath(h.Path, directory)
			if err != nil {
				return err
			}
			if err := s.Add(ctx, path, []byte(h.Contents)); err != nil {
				return err
			}

		case DeleteFile:
			// Resolve path based on directory parameter
			path, err := resolvePath(h.Path, directory)
			if err != nil {
				return err
			}
			if err := s.Delete(ctx, path); err != nil {
				return err
			}

		case UpdateFile:
			// Resolve path based on directory parameter
			path, err := resolvePath(h.Path, directory)
			if err != nil {
				return err
			}

			// Handle move if specified
			if h.MovePath != "" {
				newPath, err := resolvePath(h.MovePath, directory)
				if err != nil {
					return err
				}

				// If there are no chunks, just move the file
				if len(h.Chunks) == 0 {
					if err := s.Move(ctx, path, newPath); err != nil {
						return err
					}
					continue
				}

				// Otherwise, we'll move and then update
				if err := s.Move(ctx, path, newPath); err != nil {
					return err
				}
				path = newPath
			}

			// If there are chunks, apply them
			if len(h.Chunks) > 0 {
				// Read the original file
				oldData, err := s.fs.DownloadWithURL(ctx, path)
				if err != nil {
					return err
				}

				newContent, err := s.applyUpdate(oldData, (UpdateFile)(h), path)
				if err != nil {
					return err
				}

				if err := s.Update(ctx, path, []byte(newContent)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// applyHunks applies diff hunks to oldData and writes the patched file to w.
// It walks the original lines sequentially, verifies every context and delete
// line for consistency, and emits additions.  Any mismatch aborts with error.
func applyHunks(oldData []byte, hunks []*sgdiff.Hunk, w io.Writer) error {
	// Preserve original newline layout.
	oldLines := strings.SplitAfter(string(oldData), "\n")
	origIdx := 0 // 0-based index into oldLines

	linesEqual := func(a, b string) bool {
		if a == b {
			return true
		}
		// Handle newline-at-EOF equivalence: SplitAfter leaves an empty string as
		// the last slice element whereas diff encodes it as "\n" context line.
		if (a == "" && b == "\n") || (a == "\n" && b == "") {
			return true
		}
		// Make comparison space-insensitive by removing all whitespace
		aNoSpace := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(a, " ", ""), "\t", ""), "\r", "")
		bNoSpace := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(b, " ", ""), "\t", ""), "\r", "")
		return aNoSpace == bNoSpace
	}

	for _, h := range hunks {
		// 1) Copy untouched lines that appear before this hunk.
		//    OrigStartLine is 1-based; we want everything < that line.
		targetIdx := int(h.OrigStartLine) - 1
		for origIdx < targetIdx && origIdx < len(oldLines) {
			if _, err := io.WriteString(w, oldLines[origIdx]); err != nil {
				return err
			}
			origIdx++
		}

		// 2) Process the hunk body.
		for _, hl := range strings.SplitAfter(string(h.Body), "\n") {
			if hl == "" { // final split can be empty
				continue
			}
			tag := hl[0]
			line := hl[1:] // includes trailing newline (if present)

			switch tag {
			case ' ': // context — must match original, then copy through
				if origIdx >= len(oldLines) || !linesEqual(oldLines[origIdx], line) {
					return fmt.Errorf("patch failed: context mismatch at original line %d", origIdx+1)
				}
				// The special case where "line" is just "\n" and the counterpart in
				// oldLines is an empty string means we are at the implicit newline that
				// terminates the file. It has already been emitted as part of the
				// previous line, so we skip writing to avoid producing an extra blank
				// line (issue #thread-safe-newline).
				if !(oldLines[origIdx] == "" && line == "\n") {
					if _, err := io.WriteString(w, line); err != nil {
						return err
					}
				}
				origIdx++

			case '-': // deletion — must match original, *do not* copy
				if origIdx >= len(oldLines) || !linesEqual(oldLines[origIdx], line) {
					return fmt.Errorf("patch failed: delete mismatch at original line %d", origIdx+1)
				}
				origIdx++

			case '+': // addition — write to output, do not advance original
				if _, err := io.WriteString(w, line); err != nil {
					return err
				}

			case '\\': // “\ No newline at end of file” — ignore
				continue

			default:
				return fmt.Errorf("patch failed: unexpected hunk tag %q", tag)
			}
		}
	}

	// 3) Copy any remaining untouched lines after the last hunk.
	for origIdx < len(oldLines) {
		if _, err := io.WriteString(w, oldLines[origIdx]); err != nil {
			return err
		}
		origIdx++
	}
	return nil
}
