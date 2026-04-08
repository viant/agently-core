package resources

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/viant/afs"
	"github.com/viant/afs/url"
	"github.com/viant/agently-core/internal/textutil"
	mcpuri "github.com/viant/agently-core/protocol/mcp/uri"
	svc "github.com/viant/agently-core/protocol/tool/service"
	mcpfs "github.com/viant/agently-core/service/augmenter/mcpfs"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
)

type GrepInput struct {
	// Pattern is a required search expression. Internally it may be split on
	// simple OR separators ("|" or " or ") into multiple patterns which are
	// combined using logical OR.
	Pattern string `json:"pattern" description:"search pattern; supports OR via '|' or textual 'or' (case-insensitive)"`
	// ExcludePattern optionally defines patterns that, when matched, cause a
	// file or snippet to be excluded. It follows the same splitting rules as
	// Pattern.
	ExcludePattern string `json:"excludePattern,omitempty" description:"exclude pattern; same OR semantics as pattern"`

	// RootURI is the normalized or user-provided root URI. Prefer using
	// RootID when possible; RootURI is retained for backward compatibility.
	RootURI string `json:"root,omitempty"`
	// RootID is a stable identifier corresponding to a root returned by
	// roots. When provided, it is resolved to the underlying
	// normalized URI before enforcement and grep operations.
	RootID    string   `json:"rootId,omitempty"`
	Path      string   `json:"path"`
	Recursive bool     `json:"recursive,omitempty"`
	Include   []string `json:"include,omitempty" description:"optional file/path globs to include (matched against file path and base name); supports ** for any depth"`
	Exclude   []string `json:"exclude,omitempty" description:"optional file/path globs to exclude; supports ** for any depth"`

	CaseInsensitive bool `json:"caseInsensitive,omitempty"`

	Mode      string `json:"mode,omitempty"  description:"snippet mode: 'head' shows the first lines of each matching file; 'match' shows lines around matches"  choices:"head,match"`
	Bytes     int    `json:"bytes,omitempty"`
	Lines     int    `json:"lines,omitempty"`
	MaxFiles  int    `json:"maxFiles,omitempty"`
	MaxBlocks int    `json:"maxBlocks,omitempty"`

	SkipBinary  bool `json:"skipBinary,omitempty"`
	MaxSize     int  `json:"maxSize,omitempty"`
	Concurrency int  `json:"concurrency,omitempty"`
}

type GrepOutput struct {
	Stats textutil.GrepStats  `json:"stats"`
	Files []textutil.GrepFile `json:"files,omitempty"`
}

type grepSearchHashInput struct {
	Pattern         string   `json:"pattern,omitempty"`
	ExcludePattern  string   `json:"excludePattern,omitempty"`
	Root            string   `json:"root,omitempty"`
	RootID          string   `json:"rootId,omitempty"`
	Path            string   `json:"path,omitempty"`
	Recursive       bool     `json:"recursive,omitempty"`
	Include         []string `json:"include,omitempty"`
	Exclude         []string `json:"exclude,omitempty"`
	CaseInsensitive bool     `json:"caseInsensitive,omitempty"`
	Mode            string   `json:"mode,omitempty"`
	Bytes           int      `json:"bytes,omitempty"`
	Lines           int      `json:"lines,omitempty"`
	MaxFiles        int      `json:"maxFiles,omitempty"`
	MaxBlocks       int      `json:"maxBlocks,omitempty"`
	SkipBinary      bool     `json:"skipBinary,omitempty"`
	MaxSize         int      `json:"maxSize,omitempty"`
	Concurrency     int      `json:"concurrency,omitempty"`
}

func grepSearchHash(input *GrepInput, rootURI string) string {
	if input == nil {
		return ""
	}
	payload := grepSearchHashInput{
		Pattern:         strings.TrimSpace(input.Pattern),
		ExcludePattern:  strings.TrimSpace(input.ExcludePattern),
		Root:            strings.TrimSpace(rootURI),
		RootID:          strings.TrimSpace(input.RootID),
		Path:            strings.TrimSpace(input.Path),
		Recursive:       input.Recursive,
		Include:         append([]string(nil), input.Include...),
		Exclude:         append([]string(nil), input.Exclude...),
		CaseInsensitive: input.CaseInsensitive,
		Mode:            strings.TrimSpace(input.Mode),
		Bytes:           input.Bytes,
		Lines:           input.Lines,
		MaxFiles:        input.MaxFiles,
		MaxBlocks:       input.MaxBlocks,
		SkipBinary:      input.SkipBinary,
		MaxSize:         input.MaxSize,
		Concurrency:     input.Concurrency,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha1.Sum(raw)
	// Short but collision-resistant enough for UI row keys.
	return hex.EncodeToString(sum[:6])
}

func (s *Service) grepFiles(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*GrepInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*GrepOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	pattern := strings.TrimSpace(input.Pattern)
	if pattern == "" {
		return fmt.Errorf("pattern must not be empty")
	}
	rootURI := strings.TrimSpace(input.RootURI)
	rootID := strings.TrimSpace(input.RootID)
	allowed := s.agentAllowed(ctx)
	if (rootURI == "" && rootID == "") || rootID == "workspace://localhost" || rootID == "workspace://localhost/" {
		if inferred := inferAllowedRootFromPath(input.Path, allowed); inferred != "" {
			rootURI = inferred
			rootID = ""
		}
	}
	if rootURI == "" && rootID != "" {
		var err error
		rootURI, err = s.resolveRootID(ctx, rootID)
		if err != nil {
			return err
		}
	}
	if rootURI == "" {
		return fmt.Errorf("root or rootId is required")
	}
	searchHash := grepSearchHash(input, rootURI)
	curAgent := s.currentAgent(ctx)
	if mcpuri.Is(rootURI) {
		if wsRoot, _, err := s.normalizeUserRoot(ctx, rootURI); err == nil && strings.TrimSpace(wsRoot) != "" {
			if len(allowed) > 0 && !isAllowedWorkspace(wsRoot, allowed) {
				if rootMeta, ok := s.mcpRootMeta(ctx, wsRoot); ok && rootMeta != nil {
					allowed = append(allowed, wsRoot)
				}
			}
		}
	}
	rootCtx, err := s.newRootContext(ctx, rootURI, input.RootID, allowed)
	if err != nil {
		return err
	}
	wsRoot := rootCtx.Workspace()
	if mcpuri.Is(wsRoot) && s.mcpMgr == nil {
		return fmt.Errorf("mcp manager not configured")
	}
	// Enforce per-resource grep capability when agent context is available.
	if !s.grepAllowedForAgent(ctx, curAgent, wsRoot) {
		return fmt.Errorf("grep not allowed for root: %s", rootURI)
	}
	rootBase := rootCtx.Base()
	base := rootBase
	pathValue := strings.TrimSpace(input.Path)
	if pathValue == "" {
		if workdir, ok := toolexec.WorkdirFromContext(ctx); ok {
			pathValue = strings.TrimSpace(workdir)
		}
	}
	if pathValue != "" {
		base, err = rootCtx.ResolvePath(pathValue)
		if err != nil {
			return err
		}
	}
	// Normalise defaults
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = "match"
	}
	limitBytes := input.Bytes
	if limitBytes <= 0 {
		limitBytes = 512
	}
	limitLines := input.Lines
	if limitLines <= 0 {
		limitLines = 32
	}
	maxFiles := input.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 20
	}
	maxBlocks := input.MaxBlocks
	if maxBlocks <= 0 {
		maxBlocks = 200
	}
	maxSize := input.MaxSize
	if maxSize <= 0 {
		maxSize = 1024 * 1024 // 1MB
	}
	skipBinary := input.SkipBinary
	if !input.SkipBinary {
		// default behaviour: skip binary files unless explicitly disabled
		skipBinary = true
	}

	patList := splitPatterns(pattern)
	exclList := splitPatterns(strings.TrimSpace(input.ExcludePattern))
	if len(patList) == 0 {
		return fmt.Errorf("pattern must not be empty")
	}
	matchers, err := compilePatterns(patList, input.CaseInsensitive)
	if err != nil {
		return err
	}
	excludeMatchers, err := compilePatterns(exclList, input.CaseInsensitive)
	if err != nil {
		return err
	}

	includes := normalizeListGlobs(input.Include)
	excludes := normalizeListGlobs(input.Exclude)

	stats := textutil.GrepStats{}
	var files []textutil.GrepFile
	totalBlocks := 0

	if mcpuri.Is(wsRoot) {
		return s.grepMCPFiles(ctx, input, output, rootCtx, searchHash, matchers, excludeMatchers, includes, excludes, mode, limitBytes, limitLines, maxFiles, maxBlocks, maxSize, skipBinary)
	}

	// Local/workspace handling
	fs := afs.New()

	// When input.Path resolves to a file, avoid Walk (which assumes directories for file:// roots).
	if obj, err := fs.Object(ctx, base); err == nil && obj != nil && !obj.IsDir() {
		uri := base
		data, err := fs.DownloadWithURL(ctx, uri)
		if err != nil {
			return err
		}
		if maxSize > 0 && len(data) > maxSize {
			data = data[:maxSize]
		}
		if skipBinary && isBinary(data) {
			output.Stats = stats
			output.Files = nil
			return nil
		}
		text := string(data)
		lines := strings.Split(text, "\n")
		var matchLines []int
		for i, line := range lines {
			if lineMatches(line, matchers, excludeMatchers) {
				matchLines = append(matchLines, i)
			}
		}
		if len(matchLines) == 0 {
			output.Stats = stats
			output.Files = nil
			return nil
		}

		scheme := url.Scheme(rootBase, "file")
		rootBaseNormalised := url.Normalize(rootBase, scheme)
		uriNormalised := url.Normalize(uri, scheme)

		rel := relativePath(rootBaseNormalised, uriNormalised)
		if rel == "" {
			baseNormalised := url.Normalize(base, scheme)
			rel = relativePath(baseNormalised, uriNormalised)
		}
		if rel == "" {
			rel = uri
		}
		name := pathpkg.Base(strings.TrimSuffix(rel, "/"))
		if !listMatchesFilters(rel, name, includes, excludes) {
			output.Stats = stats
			output.Files = nil
			return nil
		}

		stats.Scanned = 1
		stats.Matched = 1
		gf := textutil.GrepFile{Path: rel, URI: uri, Matches: len(matchLines)}
		gf.SearchHash = searchHash
		if mode == "head" {
			end := limitLines
			if end > len(lines) {
				end = len(lines)
			}
			snippetText := joinLines(lines[:end])
			if len(snippetText) > limitBytes {
				snippetText = snippetText[:limitBytes]
			}
			gf.Snippets = append(gf.Snippets, textutil.Snippet{StartLine: 1, EndLine: end, Text: snippetText})
			gf.RangeKey = fmt.Sprintf("%d-%d", 1, end)
			files = append(files, gf)
			output.Stats = stats
			output.Files = files
			return nil
		}
		for _, idx := range matchLines {
			if totalBlocks >= maxBlocks {
				stats.Truncated = true
				break
			}
			start := idx - limitLines/2
			if start < 0 {
				start = 0
			}
			end := start + limitLines
			if end > len(lines) {
				end = len(lines)
			}
			snippetText := joinLines(lines[start:end])
			cut := false
			if len(snippetText) > limitBytes {
				snippetText = snippetText[:limitBytes]
				cut = true
			}
			gf.Snippets = append(gf.Snippets, textutil.Snippet{
				StartLine:   start + 1,
				EndLine:     end,
				Text:        snippetText,
				OffsetBytes: 0,
				LengthBytes: len(snippetText),
				Cut:         cut,
			})
			if gf.RangeKey == "" {
				gf.RangeKey = fmt.Sprintf("%d-%d", start+1, end)
			}
			totalBlocks++
		}
		files = append(files, gf)
		output.Stats = stats
		output.Files = files
		return nil
	}

	err = fs.Walk(ctx, base, func(ctx context.Context, walkBaseURL, parent string, info os.FileInfo, reader io.Reader) (bool, error) {
		if info == nil || info.IsDir() {
			return true, nil
		}
		// Enforce non-recursive mode: only consider direct children when Recursive=false.
		if !input.Recursive && parent != "" {
			return true, nil
		}
		// Build full URI
		var uri string
		if parent == "" {
			uri = url.Join(walkBaseURL, info.Name())
		} else {
			uri = url.Join(walkBaseURL, parent, info.Name())
		}
		// Apply include/exclude globs on the relative path
		scheme := url.Scheme(walkBaseURL, "file")
		baseNormalised := url.Normalize(base, scheme)
		rel := relativePath(baseNormalised, uri)
		if !listMatchesFilters(rel, info.Name(), includes, excludes) {
			return true, nil
		}

		stats.Scanned++
		if stats.Matched >= maxFiles || totalBlocks >= maxBlocks {
			stats.Truncated = true
			return false, nil
		}
		data, err := fs.DownloadWithURL(ctx, uri)
		if err != nil {
			return false, err
		}
		if maxSize > 0 && len(data) > maxSize {
			data = data[:maxSize]
		}
		if skipBinary && isBinary(data) {
			return true, nil
		}
		text := string(data)
		lines := strings.Split(text, "\n")
		var matchLines []int
		for i, line := range lines {
			if lineMatches(line, matchers, excludeMatchers) {
				matchLines = append(matchLines, i)
			}
		}
		if len(matchLines) == 0 {
			return true, nil
		}
		stats.Matched++
		gf := textutil.GrepFile{Path: rel, URI: uri}
		gf.SearchHash = searchHash
		gf.Matches = len(matchLines)
		// Build snippets depending on mode
		if mode == "head" {
			// Single snippet from the top of the file
			end := limitLines
			if end > len(lines) {
				end = len(lines)
			}
			snippetText := joinLines(lines[:end])
			if len(snippetText) > limitBytes {
				snippetText = snippetText[:limitBytes]
			}
			gf.Snippets = append(gf.Snippets, textutil.Snippet{StartLine: 1, EndLine: end, Text: snippetText})
			gf.RangeKey = fmt.Sprintf("%d-%d", 1, end)
			files = append(files, gf)
			return stats.Matched < maxFiles && totalBlocks < maxBlocks, nil
		}
		// match mode: build a snippet around each match line
		for _, idx := range matchLines {
			if totalBlocks >= maxBlocks {
				stats.Truncated = true
				return false, nil
			}
			start := idx - limitLines/2
			if start < 0 {
				start = 0
			}
			end := start + limitLines
			if end > len(lines) {
				end = len(lines)
			}
			snippetText := joinLines(lines[start:end])
			cut := false
			if len(snippetText) > limitBytes {
				snippetText = snippetText[:limitBytes]
				cut = true
			}
			gf.Snippets = append(gf.Snippets, textutil.Snippet{
				StartLine:   start + 1,
				EndLine:     end,
				Text:        snippetText,
				OffsetBytes: 0,
				LengthBytes: len(snippetText),
				Cut:         cut,
			})
			if gf.RangeKey == "" {
				gf.RangeKey = fmt.Sprintf("%d-%d", start+1, end)
			}
			totalBlocks++
			if totalBlocks >= maxBlocks {
				stats.Truncated = true
				break
			}
		}
		files = append(files, gf)
		if stats.Matched >= maxFiles || totalBlocks >= maxBlocks {
			stats.Truncated = true
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	output.Stats = stats
	output.Files = files
	return nil
}

func (s *Service) grepMCPFiles(
	ctx context.Context,
	input *GrepInput,
	output *GrepOutput,
	rootCtx *rootContext,
	searchHash string,
	matchers []*regexp.Regexp,
	excludeMatchers []*regexp.Regexp,
	includes []string,
	excludes []string,
	mode string,
	limitBytes int,
	limitLines int,
	maxFiles int,
	maxBlocks int,
	maxSize int,
	skipBinary bool,
) error {
	if s == nil || s.mcpMgr == nil {
		return fmt.Errorf("mcp manager not configured")
	}
	wsRoot := rootCtx.Workspace()
	rootMeta, ok := s.mcpRootMeta(ctx, wsRoot)
	if !ok || rootMeta == nil || !rootMeta.Snapshot {
		return fmt.Errorf("grep requires snapshot support for root: %s", wsRoot)
	}
	if !rootMeta.AllowGrep {
		return fmt.Errorf("grep not allowed for root: %s", wsRoot)
	}
	resolver := s.mcpSnapshotResolver(ctx)
	snapURI, rootURI, ok := resolver(wsRoot)
	if !ok {
		return fmt.Errorf("grep requires snapshot support for root: %s", wsRoot)
	}
	mfs, err := s.mcpFS(ctx)
	if err != nil {
		return err
	}
	data, err := mfs.Download(ctx, mcpfs.NewObjectFromURI(snapURI))
	if err != nil {
		return err
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	stripPrefix := detectZipStripPrefix(reader)
	server, rootPath := mcpuri.Parse(rootURI)
	rootPath = strings.TrimRight(rootPath, "/")
	base := rootCtx.Base()
	baseServer, basePath := mcpuri.Parse(base)
	if strings.TrimSpace(baseServer) == "" {
		basePath = rootPath
	} else {
		basePath = strings.TrimRight(basePath, "/")
	}

	stats := textutil.GrepStats{}
	var files []textutil.GrepFile
	totalBlocks := 0

	for _, f := range reader.File {
		if f == nil || f.FileInfo().IsDir() {
			continue
		}
		rel := strings.TrimPrefix(f.Name, stripPrefix)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			continue
		}
		fullPath := mcpuri.JoinResourcePath(rootPath, rel)
		if basePath != "" && basePath != rootPath {
			if fullPath != basePath && !strings.HasPrefix(fullPath, basePath+"/") {
				continue
			}
		}
		uri := mcpuri.Canonical(server, fullPath)
		relPath := relativePath(base, uri)
		if relPath == "" {
			relPath = relativePath(wsRoot, uri)
		}
		if relPath == "" {
			relPath = rel
		}
		name := pathpkg.Base(strings.TrimSuffix(relPath, "/"))
		if !listMatchesFilters(relPath, name, includes, excludes) {
			continue
		}
		stats.Scanned++
		if stats.Matched >= maxFiles || totalBlocks >= maxBlocks {
			stats.Truncated = true
			break
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		fileData, err := readZipFile(rc, maxSize)
		_ = rc.Close()
		if err != nil {
			return err
		}
		if skipBinary && isBinary(fileData) {
			continue
		}
		text := string(fileData)
		lines := strings.Split(text, "\n")
		var matchLines []int
		for i, line := range lines {
			if lineMatches(line, matchers, excludeMatchers) {
				matchLines = append(matchLines, i)
			}
		}
		if len(matchLines) == 0 {
			continue
		}
		stats.Matched++
		gf := textutil.GrepFile{Path: relPath, URI: uri, Matches: len(matchLines)}
		gf.SearchHash = searchHash
		if mode == "head" {
			end := limitLines
			if end > len(lines) {
				end = len(lines)
			}
			snippetText := joinLines(lines[:end])
			if len(snippetText) > limitBytes {
				snippetText = snippetText[:limitBytes]
			}
			gf.Snippets = append(gf.Snippets, textutil.Snippet{StartLine: 1, EndLine: end, Text: snippetText})
			gf.RangeKey = fmt.Sprintf("%d-%d", 1, end)
			files = append(files, gf)
			if stats.Matched >= maxFiles || totalBlocks >= maxBlocks {
				stats.Truncated = true
				break
			}
			continue
		}
		for _, idx := range matchLines {
			if totalBlocks >= maxBlocks {
				stats.Truncated = true
				break
			}
			start := idx - limitLines/2
			if start < 0 {
				start = 0
			}
			end := start + limitLines
			if end > len(lines) {
				end = len(lines)
			}
			snippetText := joinLines(lines[start:end])
			cut := false
			if len(snippetText) > limitBytes {
				snippetText = snippetText[:limitBytes]
				cut = true
			}
			gf.Snippets = append(gf.Snippets, textutil.Snippet{
				StartLine:   start + 1,
				EndLine:     end,
				Text:        snippetText,
				OffsetBytes: 0,
				LengthBytes: len(snippetText),
				Cut:         cut,
			})
			if gf.RangeKey == "" {
				gf.RangeKey = fmt.Sprintf("%d-%d", start+1, end)
			}
			totalBlocks++
			if totalBlocks >= maxBlocks {
				stats.Truncated = true
				break
			}
		}
		files = append(files, gf)
		if stats.Matched >= maxFiles || totalBlocks >= maxBlocks {
			stats.Truncated = true
			break
		}
	}
	output.Stats = stats
	output.Files = files
	return nil
}

func readZipFile(rc io.Reader, maxSize int) ([]byte, error) {
	if maxSize > 0 {
		rc = io.LimitReader(rc, int64(maxSize))
	}
	return io.ReadAll(rc)
}

func detectZipStripPrefix(reader *zip.Reader) string {
	if reader == nil {
		return ""
	}
	common := ""
	for _, f := range reader.File {
		if f == nil || f.FileInfo().IsDir() {
			continue
		}
		name := strings.TrimPrefix(f.Name, "/")
		if name == "" {
			continue
		}
		parts := strings.SplitN(name, "/", 2)
		if len(parts) < 2 || parts[0] == "" {
			return ""
		}
		if common == "" {
			common = parts[0]
			continue
		}
		if common != parts[0] {
			return ""
		}
	}
	if common == "" {
		return ""
	}
	return common + "/"
}

// splitPatterns splits a logical OR expression into individual patterns.
// It supports simple separators like "|" and textual "or" (case-insensitive)
// surrounded by spaces, e.g. "Auth or Token" or "AUTH OR TOKEN".
func splitPatterns(expr string) []string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}
	lower := strings.ToLower(expr)
	sep := " or "
	var parts []string
	start := 0
	for {
		idx := strings.Index(lower[start:], sep)
		if idx == -1 {
			break
		}
		// idx is relative to start; convert to absolute index into expr
		abs := start + idx
		parts = append(parts, expr[start:abs])
		start = abs + len(sep)
	}
	if start == 0 {
		// no textual "or" found; treat whole expr as a single part
		parts = []string{expr}
	} else {
		parts = append(parts, expr[start:])
	}
	var out []string
	for _, p := range parts {
		for _, sub := range strings.Split(p, "|") {
			if v := strings.TrimSpace(sub); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

// helper used by grepFiles to compile patterns.
func compilePatterns(patterns []string, caseInsensitive bool) ([]*regexp.Regexp, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		pat := strings.TrimSpace(p)
		if pat == "" {
			continue
		}
		if caseInsensitive {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			literal := regexp.QuoteMeta(strings.TrimSpace(p))
			if caseInsensitive {
				literal = "(?i)" + literal
			}
			re, err = regexp.Compile(literal)
			if err != nil {
				return nil, fmt.Errorf("invalid pattern %q: %w", p, err)
			}
		}
		out = append(out, re)
	}
	return out, nil
}

// lineMatches reports whether a line matches at least one include pattern and
// none of the exclude patterns.
func lineMatches(line string, includes, excludes []*regexp.Regexp) bool {
	matched := false
	if len(includes) == 0 {
		matched = true
	} else {
		for _, re := range includes {
			if re.FindStringIndex(line) != nil {
				matched = true
				break
			}
		}
	}
	if !matched {
		return false
	}
	for _, re := range excludes {
		if re.FindStringIndex(line) != nil {
			return false
		}
	}
	return true
}

// isBinary provides a simple heuristic for binary files: presence of NUL
// bytes or invalid UTF-8.
func isBinary(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return !utf8.Valid(b)
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(ln)
	}
	return b.String()
}
