package resources

import (
	"context"
	"fmt"
	pathpkg "path"
	"sort"
	"strings"

	"github.com/viant/agently-core/internal/agent/systemdoc"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpuri "github.com/viant/agently-core/protocol/mcp/uri"
	aug "github.com/viant/agently-core/service/augmenter"
	svc "github.com/viant/agently-core/protocol/tool/service"
	embopt "github.com/viant/embedius/matching/option"
	embSchema "github.com/viant/embedius/schema"
)

// MatchInput defines parameters for semantic search across one or more roots.
type MatchInput struct {
	Query string `json:"query"`
	// RootURI/Roots are retained for backward compatibility but will default to all accessible roots when omitted.
	RootURI []string `json:"rootUri,omitempty" internal:"true"`
	Roots   []string `json:"roots,omitempty" internal:"true"`
	// RootIDs contains stable identifiers corresponding to roots returned by
	// roots. When provided, they are resolved to URIs before
	// enforcement and search.
	RootIDs      []string        `json:"rootIds,omitempty" description:"resource root ids returned by roots"`
	Path         string          `json:"path,omitempty"`
	Model        string          `json:"model" internal:"true"`
	MaxDocuments int             `json:"maxDocuments,omitempty" `
	IncludeFile  bool            `json:"includeFile,omitempty" internal:"true"`
	Match        *embopt.Options `json:"match,omitempty"`
	Exclude      []string        `json:"exclude,omitempty" description:"optional file/path globs to exclude from match results; supports ** for any depth"`
	// LimitBytes controls the maximum total bytes of matched content returned for the current cursor page.
	LimitBytes int `json:"limitBytes,omitempty" description:"Max total bytes per page of matched content. Default: 7000."`
	// Cursor selects the page (1..N) over the ranked documents, grouped by LimitBytes.
	Cursor int `json:"cursor,omitempty" description:"Result page selector (1..N). Default: 1."`
}

// MatchOutput mirrors augmenter.AugmentDocsOutput for convenience.
type MatchOutput struct {
	aug.AugmentDocsOutput
	// NextCursor points to the next page (cursor+1) when more content is available; 0 means no further pages.
	NextCursor int `json:"nextCursor,omitempty" description:"Next page cursor when available; 0 when none."`
	// Cursor echoes the selected page.
	Cursor int `json:"cursor,omitempty" description:"Selected page cursor (1..N)."`
	// LimitBytes echoes the applied byte limit per page.
	LimitBytes int `json:"limitBytes,omitempty" description:"Applied byte cap per page."`
	// SystemContent mirrors Content but only includes system-role documents so callers can surface
	// protected context as system messages.
	SystemContent string `json:"systemContent,omitempty" description:"Formatted content for system resources only."`
	// DocumentRoots maps document SourceURI values to their originating root IDs.
	DocumentRoots map[string]string `json:"documentRoots,omitempty" description:"Maps document source URIs to their root IDs."`
}

type augmentedDocuments struct {
	documents      []embSchema.Document
	trimPrefix     string
	systemPrefixes []string
}

type searchRootMeta struct {
	id     string
	wsRoot string
}

func (s *Service) selectSearchRoots(ctx context.Context, roots []Root, input *MatchInput) ([]Root, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("no roots configured")
	}
	allowed := s.agentAllowed(ctx)
	var selected []Root
	seen := map[string]struct{}{}
	add := func(candidates []Root) {
		for _, root := range candidates {
			key := strings.ToLower(strings.TrimSpace(root.ID))
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(root.URI))
			}
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			if !root.AllowedSemanticSearch {
				continue
			}
			seen[key] = struct{}{}
			selected = append(selected, root)
		}
	}
	if len(input.RootIDs) > 0 {
		matchedByID := filterRootsByID(roots, input.RootIDs)
		add(matchedByID)
		missing := missingRootIDs(input.RootIDs, matchedByID)
		if len(missing) > 0 {
			matchedByURI := filterRootsByURI(roots, missing)
			add(matchedByURI)
			if remaining := missingRootIDs(input.RootIDs, append(matchedByID, matchedByURI...)); len(remaining) > 0 {
				var unresolved []string
				curAgent := s.currentAgent(ctx)
				for _, id := range remaining {
					if strings.Contains(id, "://") || mcpuri.Is(id) || strings.HasPrefix(id, "workspace://") || strings.HasPrefix(id, "file://") {
						unresolved = append(unresolved, id)
						continue
					}
					uri, err := s.resolveRootID(ctx, id)
					if err != nil || strings.TrimSpace(uri) == "" {
						unresolved = append(unresolved, id)
						continue
					}
					if len(allowed) > 0 && !isAllowedWorkspace(uri, allowed) {
						return nil, fmt.Errorf("rootId not allowed: %s", id)
					}
					if !s.semanticAllowedForAgent(ctx, curAgent, uri) {
						return nil, fmt.Errorf("rootId not semantic-enabled: %s", id)
					}
					add([]Root{{
						ID:                    strings.TrimSpace(id),
						URI:                   uri,
						AllowedSemanticSearch: true,
						Role:                  "system",
					}})
				}
				if len(unresolved) > 0 {
					return nil, fmt.Errorf("unknown rootId(s): %s", strings.Join(unresolved, ", "))
				}
			}
		}
	}
	if len(selected) == 0 {
		uris := append([]string(nil), input.RootURI...)
		uris = append(uris, input.Roots...)
		if len(uris) > 0 {
			add(filterRootsByURI(roots, uris))
		}
	}
	if len(selected) == 0 {
		add(roots)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no semantic-enabled roots available")
	}
	return selected, nil
}

func filterRootsByID(roots []Root, ids []string) []Root {
	if len(ids) == 0 {
		return nil
	}
	idSet := map[string]struct{}{}
	for _, raw := range ids {
		if trimmed := normalizeRootID(raw); trimmed != "" {
			idSet[trimmed] = struct{}{}
		}
	}
	var out []Root
	for _, root := range roots {
		id := normalizeRootID(root.ID)
		if id == "" {
			id = normalizeRootID(root.URI)
		}
		if id == "" {
			continue
		}
		if _, ok := idSet[id]; ok {
			out = append(out, root)
		}
	}
	return out
}

func filterRootsByURI(roots []Root, uris []string) []Root {
	if len(uris) == 0 {
		return nil
	}
	uriSet := map[string]struct{}{}
	for _, raw := range uris {
		if trimmed := normalizeRootURI(raw); trimmed != "" {
			uriSet[trimmed] = struct{}{}
		}
	}
	var out []Root
	for _, root := range roots {
		uri := normalizeRootURI(root.URI)
		if uri == "" {
			continue
		}
		if _, ok := uriSet[uri]; ok {
			out = append(out, root)
		}
	}
	return out
}

func missingRootIDs(requested []string, matched []Root) []string {
	if len(requested) == 0 {
		return nil
	}
	matchedSet := map[string]struct{}{}
	for _, root := range matched {
		if key := normalizeRootID(root.ID); key != "" {
			matchedSet[key] = struct{}{}
		}
		if key := normalizeRootURI(root.URI); key != "" {
			matchedSet[key] = struct{}{}
		}
	}
	seen := map[string]struct{}{}
	var missing []string
	for _, raw := range requested {
		idKey := normalizeRootID(raw)
		uriKey := normalizeRootURI(raw)
		if idKey == "" && uriKey == "" {
			continue
		}
		if _, ok := matchedSet[idKey]; ok {
			continue
		}
		if _, ok := matchedSet[uriKey]; ok {
			continue
		}
		key := idKey
		if key == "" {
			key = uriKey
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		missing = append(missing, raw)
	}
	return missing
}

func normalizeRootID(value string) string {
	v := strings.TrimSpace(value)
	if mcpuri.Is(v) {
		v = mcpuri.NormalizeForCompare(v)
	}
	return strings.ToLower(strings.TrimSpace(v))
}

func normalizeRootURI(value string) string {
	v := strings.TrimSpace(value)
	if mcpuri.Is(v) {
		v = mcpuri.NormalizeForCompare(v)
	} else {
		v = strings.TrimRight(v, "/")
	}
	return strings.ToLower(strings.TrimSpace(v))
}

func assignRootMetadata(doc *embSchema.Document, roots []searchRootMeta) {
	if doc == nil || len(roots) == 0 {
		return
	}
	path := documentMetadataPath(doc.Metadata)
	if path == "" {
		return
	}
	normalized := normalizeWorkspaceKey(path)
	for _, entry := range roots {
		prefix := normalizeWorkspaceKey(entry.wsRoot)
		if prefix == "" {
			continue
		}
		if normalized == prefix || strings.HasPrefix(normalized, prefix+"/") {
			doc.Metadata["rootId"] = entry.id
			return
		}
	}
}

func normalizeSearchPath(p string, wsRoot string) string {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return ""
	}
	trimmed = toWorkspaceURI(trimmed)
	root := strings.TrimRight(strings.TrimSpace(wsRoot), "/")
	if root == "" {
		return trimmed
	}
	if strings.EqualFold(strings.TrimRight(trimmed, "/"), root) {
		return ""
	}
	if strings.HasPrefix(trimmed, root+"/") {
		return strings.TrimPrefix(trimmed[len(root):], "/")
	}
	return trimmed
}

func mergeMatchOptions(base *embopt.Options, include, exclude []string, maxSizeBytes int64) *embopt.Options {
	if base == nil && len(include) == 0 && len(exclude) == 0 && maxSizeBytes <= 0 {
		return nil
	}
	out := &embopt.Options{}
	if base != nil {
		*out = *base
	}
	if len(out.Inclusions) == 0 && len(include) > 0 {
		out.Inclusions = append([]string(nil), include...)
	}
	if len(out.Exclusions) == 0 && len(exclude) > 0 {
		out.Exclusions = append([]string(nil), exclude...)
	}
	if out.MaxFileSize == 0 && maxSizeBytes > 0 {
		out.MaxFileSize = int(maxSizeBytes)
	}
	return out
}

func mergeEffectiveMatch(rootMatch, inputMatch *embopt.Options) *embopt.Options {
	if rootMatch == nil && inputMatch == nil {
		return nil
	}
	out := &embopt.Options{}
	if rootMatch != nil {
		*out = *rootMatch
	}
	if inputMatch != nil {
		if len(inputMatch.Inclusions) > 0 {
			out.Inclusions = append([]string(nil), inputMatch.Inclusions...)
		}
		if len(inputMatch.Exclusions) > 0 {
			out.Exclusions = append([]string(nil), inputMatch.Exclusions...)
		}
		if inputMatch.MaxFileSize > 0 {
			out.MaxFileSize = inputMatch.MaxFileSize
		}
	}
	if len(out.Inclusions) == 0 && len(out.Exclusions) == 0 && out.MaxFileSize == 0 {
		return nil
	}
	return out
}

func matchKey(rootMatch, inputMatch *embopt.Options) string {
	eff := mergeEffectiveMatch(rootMatch, inputMatch)
	if eff == nil {
		return ""
	}
	return fmt.Sprintf("max=%d|incl=%s|excl=%s", eff.MaxFileSize, strings.Join(eff.Inclusions, ","), strings.Join(eff.Exclusions, ","))
}

func matchesMCPSelector(selectors []string, root mcpcfg.ResourceRoot) bool {
	if len(selectors) == 0 {
		return true
	}
	rootURI := strings.TrimSpace(root.URI)
	rootID := strings.TrimSpace(root.ID)
	if rootID == "" {
		rootID = rootURI
	}
	for _, sel := range selectors {
		if sel == "" {
			continue
		}
		if normalizeRootID(sel) == normalizeRootID(rootID) {
			return true
		}
		if normalizeRootURI(sel) == normalizeRootURI(rootURI) {
			return true
		}
	}
	return false
}

func normalizeMCPURI(value string) string {
	if !mcpuri.Is(value) {
		return value
	}
	server, uri := mcpuri.Parse(value)
	if strings.TrimSpace(server) == "" {
		return value
	}
	normalized := mcpuri.Canonical(server, uri)
	if normalized == "" {
		return value
	}
	return normalized
}

func (s *Service) buildAugmentedDocuments(ctx context.Context, input *MatchInput) (*augmentedDocuments, error) {
	if s == nil {
		return nil, fmt.Errorf("service not configured")
	}
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	embedderID := strings.TrimSpace(input.Model)
	if embedderID == "" {
		embedderID = strings.TrimSpace(s.defaultEmbedder)
	}
	if embedderID == "" {
		return nil, fmt.Errorf("embedder is required (set default embedder in config or provide internal model)")
	}
	input.IncludeFile = true
	effectiveInputMatch := input.Match
	if excludes := normalizeListGlobs(input.Exclude); len(excludes) > 0 {
		if effectiveInputMatch == nil {
			effectiveInputMatch = &embopt.Options{}
		} else {
			copyMatch := *effectiveInputMatch
			effectiveInputMatch = &copyMatch
		}
		if len(effectiveInputMatch.Exclusions) == 0 {
			effectiveInputMatch.Exclusions = append([]string(nil), excludes...)
		} else {
			effectiveInputMatch.Exclusions = append(append([]string(nil), effectiveInputMatch.Exclusions...), excludes...)
		}
	}

	collectedRoots, err := s.collectRoots(ctx)
	if err != nil {
		return nil, err
	}
	availableRoots := collectedRoots.all()
	if mcpRoots := s.filterMCPRootsByAllowed(ctx, s.collectMCPRoots(ctx)); len(mcpRoots) > 0 {
		byURI := map[string]bool{}
		for _, root := range availableRoots {
			key := strings.TrimRight(strings.TrimSpace(root.URI), "/")
			if key != "" {
				byURI[key] = true
			}
		}
		for _, root := range mcpRoots {
			key := strings.TrimRight(strings.TrimSpace(root.URI), "/")
			if key == "" || byURI[key] {
				continue
			}
			byURI[key] = true
			availableRoots = append(availableRoots, root)
		}
	}
	if len(availableRoots) == 0 {
		return nil, fmt.Errorf("no roots configured for semantic search")
	}
	selectedRoots, err := s.selectSearchRoots(ctx, availableRoots, input)
	if err != nil {
		return nil, err
	}
	var localRoots []aug.LocalRoot
	for _, root := range selectedRoots {
		if mcpuri.Is(root.URI) {
			continue
		}
		localRoots = append(localRoots, aug.LocalRoot{
			ID:          root.ID,
			URI:         root.URI,
			UpstreamRef: root.UpstreamRef,
		})
	}
	if len(localRoots) > 0 {
		ctx = aug.WithLocalRoots(ctx, localRoots)
	}
	type locInfo struct {
		location string
		db       string
		match    *embopt.Options
	}
	locations := make([]string, 0, len(selectedRoots))
	locInfos := make([]locInfo, 0, len(selectedRoots))
	searchRoots := make([]searchRootMeta, 0, len(selectedRoots))
	for _, root := range selectedRoots {
		wsRoot := strings.TrimRight(strings.TrimSpace(root.URI), "/")
		if wsRoot == "" {
			continue
		}
		base := wsRoot
		if mcpuri.Is(wsRoot) {
			base = normalizeMCPURI(wsRoot)
		}
		if strings.HasPrefix(wsRoot, "workspace://") {
			base = workspaceToFile(wsRoot)
		}
		if trimmed := normalizeSearchPath(input.Path, wsRoot); trimmed != "" {
			base, err = joinBaseWithPath(wsRoot, base, trimmed, root.URI)
			if err != nil {
				return nil, err
			}
		}
		locations = append(locations, base)
		locInfos = append(locInfos, locInfo{location: base, db: strings.TrimSpace(root.DB), match: root.Match})
		searchRoots = append(searchRoots, searchRootMeta{
			id:     root.ID,
			wsRoot: wsRoot,
		})
	}
	if len(locations) == 0 {
		return nil, fmt.Errorf("no valid roots provided")
	}
	trimPrefix := commonPrefix(locations)
	type matchGroup struct {
		db    string
		match *embopt.Options
		locs  []string
	}
	grouped := map[string]*matchGroup{}
	for _, li := range locInfos {
		key := li.db + "|" + matchKey(li.match, effectiveInputMatch)
		g, ok := grouped[key]
		if !ok {
			g = &matchGroup{db: li.db, match: mergeEffectiveMatch(li.match, effectiveInputMatch)}
			grouped[key] = g
		}
		g.locs = append(g.locs, li.location)
	}
	var allDocs []embSchema.Document
	backfill := effectiveInputMatch != nil && len(effectiveInputMatch.Exclusions) > 0
	targetDocs := input.MaxDocuments
	if targetDocs <= 0 {
		targetDocs = 40
	}
	for _, group := range grouped {
		if !backfill {
			augIn := &aug.AugmentDocsInput{
				Query:        query,
				Locations:    group.locs,
				Match:        group.match,
				Model:        embedderID,
				DB:           group.db,
				MaxDocuments: input.MaxDocuments,
				IncludeFile:  input.IncludeFile,
				TrimPath:     trimPrefix,
				AllowPartial: true,
			}
			var augOut aug.AugmentDocsOutput
			if err := s.runAugmentDocs(ctx, augIn, &augOut); err != nil {
				return nil, err
			}
			allDocs = append(allDocs, augOut.Documents...)
			continue
		}

		offset := 0
		rounds := 0
		var groupDocs []embSchema.Document
		for len(groupDocs) < targetDocs {
			rounds++
			if rounds > 5 {
				break
			}
			augIn := &aug.AugmentDocsInput{
				Query:        query,
				Locations:    group.locs,
				Match:        group.match,
				Model:        embedderID,
				DB:           group.db,
				MaxDocuments: targetDocs,
				Offset:       offset,
				IncludeFile:  input.IncludeFile,
				TrimPath:     trimPrefix,
				AllowPartial: true,
			}
			var augOut aug.AugmentDocsOutput
			if err := s.runAugmentDocs(ctx, augIn, &augOut); err != nil {
				return nil, err
			}
			if len(augOut.Documents) == 0 {
				break
			}
			groupDocs = append(groupDocs, augOut.Documents...)
			offset += len(augOut.Documents)
			if len(augOut.Documents) < targetDocs {
				break
			}
		}
		allDocs = append(allDocs, groupDocs...)
	}
	sort.SliceStable(allDocs, func(i, j int) bool { return allDocs[i].Score > allDocs[j].Score })

	curAgent := s.currentAgent(ctx)
	sysPrefixes := systemdoc.Prefixes(curAgent)
	for i := range allDocs {
		doc := &allDocs[i]
		if doc.Metadata == nil {
			doc.Metadata = map[string]any{}
		}
		doc.Metadata["score"] = doc.Score
		for _, key := range []string{"path", "docId", "fragmentId"} {
			if p, ok := doc.Metadata[key]; ok {
				if s, _ := p.(string); s != "" {
					doc.Metadata[key] = toWorkspaceURI(s)
				}
			}
		}
		assignRootMetadata(doc, searchRoots)
	}
	return &augmentedDocuments{
		documents:      allDocs,
		trimPrefix:     trimPrefix,
		systemPrefixes: sysPrefixes,
	}, nil
}

func (s *Service) match(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*MatchInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*MatchOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	fmt.Printf("resources: match request query=%q roots=%v rootIds=%v path=%q\n", input.Query, input.Roots, input.RootIDs, input.Path)
	res, err := s.buildAugmentedDocuments(ctx, input)
	if err != nil {
		fmt.Printf("resources: match error query=%q err=%v\n", input.Query, err)
		return err
	}

	// Apply byte-limited pagination for presentation
	limit := effectiveLimitBytes(input.LimitBytes)
	cursor := effectiveCursor(input.Cursor)
	pageDocs, hasNext := selectDocPage(res.documents, limit, cursor, res.trimPrefix)

	// If the total formatted size of all documents does not exceed the limit,
	// there is no next page regardless of internal grouping.
	if total := totalFormattedBytes(res.documents, res.trimPrefix); total <= limit {
		hasNext = false
	}

	// Rebuild Content for selected page using same format as augmenter
	content := buildDocumentContent(pageDocs, res.trimPrefix)
	output.AugmentDocsOutput.Content = content
	output.AugmentDocsOutput.Documents = pageDocs
	output.AugmentDocsOutput.DocumentsSize = augmenterDocumentsSize(pageDocs)
	output.DocumentRoots = buildDocumentRootsMap(pageDocs)
	output.Cursor = cursor
	output.LimitBytes = limit
	if sys := buildDocumentContent(filterSystemDocuments(pageDocs, res.systemPrefixes), res.trimPrefix); strings.TrimSpace(sys) != "" {
		output.SystemContent = sys
	}
	if hasNext {
		output.NextCursor = cursor + 1
	}
	fmt.Printf("resources: match response query=%q docs=%d cursor=%d next=%d\n", input.Query, len(pageDocs), output.Cursor, output.NextCursor)
	return nil
}

type MatchDocumentsInput struct {
	Query        string          `json:"query" description:"semantic search query" required:"true"`
	RootIDs      []string        `json:"rootIds,omitempty" description:"resource root ids returned by roots"`
	Path         string          `json:"path,omitempty" description:"optional subpath relative to selected roots"`
	Model        string          `json:"model,omitempty" internal:"true"`
	MaxDocuments int             `json:"maxDocuments,omitempty" description:"maximum number of matched documents (distinct URIs); defaults to 5"`
	Match        *embopt.Options `json:"match,omitempty" internal:"true"`
	Exclude      []string        `json:"exclude,omitempty" description:"optional file/path globs to exclude from match results; supports ** for any depth"`
}

type MatchedDocument struct {
	URI    string  `json:"uri"`
	RootID string  `json:"rootId,omitempty"`
	Score  float32 `json:"score"`
}

type MatchDocumentsOutput struct {
	Documents []MatchedDocument `json:"documents"`
}

func (s *Service) matchDocuments(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*MatchDocumentsInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	if strings.TrimSpace(input.Query) == "" {
		return fmt.Errorf("query is required")
	}
	output, ok := out.(*MatchDocumentsOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	fmt.Printf("resources: matchDocuments request query=%q rootIds=%v path=%q\n", input.Query, input.RootIDs, input.Path)
	maxDocs := input.MaxDocuments
	if maxDocs <= 0 {
		maxDocs = 5
	}
	matchInput := &MatchInput{
		Query:        input.Query,
		RootIDs:      append([]string(nil), input.RootIDs...),
		Path:         input.Path,
		Model:        input.Model,
		MaxDocuments: maxDocs,
		Match:        input.Match,
	}
	res, err := s.buildAugmentedDocuments(ctx, matchInput)
	if err != nil {
		fmt.Printf("resources: matchDocuments error query=%q err=%v\n", input.Query, err)
		return err
	}
	docs := res.documents
	ranked := uniqueMatchedDocuments(docs, maxDocs)
	if len(ranked) == 0 {
		output.Documents = nil
		fmt.Printf("resources: matchDocuments response query=%q docs=0\n", input.Query)
		return nil
	}
	output.Documents = ranked
	fmt.Printf("resources: matchDocuments response query=%q docs=%d\n", input.Query, len(ranked))
	return nil
}

func uniqueMatchedDocuments(docs []embSchema.Document, max int) []MatchedDocument {
	if len(docs) == 0 {
		return nil
	}
	type entry struct {
		doc MatchedDocument
		idx int
	}
	seen := map[string]entry{}
	order := make([]string, 0, len(docs))
	for idx, doc := range docs {
		uri := documentMetadataPath(doc.Metadata)
		if uri == "" {
			continue
		}
		current := MatchedDocument{
			URI:    uri,
			RootID: documentRootID(doc.Metadata),
			Score:  doc.Score,
		}
		if existing, ok := seen[uri]; ok {
			if current.Score > existing.doc.Score {
				seen[uri] = entry{doc: current, idx: existing.idx}
			}
			continue
		}
		seen[uri] = entry{doc: current, idx: idx}
		order = append(order, uri)
	}
	if len(seen) == 0 {
		return nil
	}
	result := make([]MatchedDocument, 0, len(seen))
	for _, uri := range order {
		if rec, ok := seen[uri]; ok {
			result = append(result, rec.doc)
			if max > 0 && len(result) >= max {
				break
			}
		}
	}
	return result
}

// effectiveLimitBytes returns the per-page byte cap (default 7000, upper bound 200k)
func effectiveLimitBytes(limit int) int {
	if limit <= 0 {
		return 7000
	}
	if limit > 200000 {
		return 200000
	}
	return limit
}

// effectiveCursor normalizes the page cursor (1..N)
func effectiveCursor(cursor int) int {
	if cursor <= 0 {
		return 1
	}
	return cursor
}

// selectDocPage splits documents into pages of at most limitBytes (based on formatted content length) and returns the selected page.
func selectDocPage(docs []embSchema.Document, limitBytes int, cursor int, trimPrefix string) ([]embSchema.Document, bool) {
	if limitBytes <= 0 || len(docs) == 0 {
		return nil, false
	}
	// Build pages iteratively using formatted size to match presentation size
	pages := make([][]embSchema.Document, 0, 4)
	var cur []embSchema.Document
	used := 0
	for _, d := range docs {
		loc := documentLocation(d, trimPrefix)
		formatted := formatDocument(loc, d.PageContent)
		fragBytes := len(formatted)
		if fragBytes > limitBytes {
			if len(cur) > 0 {
				pages = append(pages, cur)
				cur = nil
				used = 0
			}
			pages = append(pages, []embSchema.Document{d})
			continue
		}
		if used+fragBytes > limitBytes {
			pages = append(pages, cur)
			cur = nil
			used = 0
		}
		cur = append(cur, d)
		used += fragBytes
	}
	if len(cur) > 0 {
		pages = append(pages, cur)
	}
	if len(pages) == 0 {
		return nil, false
	}
	if cursor < 1 {
		cursor = 1
	}
	if cursor > len(pages) {
		cursor = len(pages)
	}
	sel := pages[cursor-1]
	hasNext := cursor < len(pages)
	return sel, hasNext
}

// formatDocument mirrors augmenter.addDocumentContent formatting.
func formatDocument(loc string, content string) string {
	ext := strings.Trim(pathpkg.Ext(loc), ".")
	return fmt.Sprintf("file: %v\n```%v\n%v\n````\n\n", loc, ext, content)
}

// augmenterDocumentsSize computes combined size using augmenter.Document.Size()
func augmenterDocumentsSize(docs []embSchema.Document) int {
	total := 0
	for _, d := range docs {
		total += aug.Document(d).Size()
	}
	return total
}

// getStringFromMetadata safely extracts a string value from metadata map.
func getStringFromMetadata(metadata map[string]any, key string) string {
	if value, ok := metadata[key]; ok {
		if text, ok := value.(string); ok {
			return text
		}
	}
	return ""
}

// totalFormattedBytes sums the presentation bytes across all documents,
// matching the formatting used in Content output.
func totalFormattedBytes(docs []embSchema.Document, trimPrefix string) int {
	total := 0
	for _, d := range docs {
		loc := documentLocation(d, trimPrefix)
		total += len(formatDocument(loc, d.PageContent))
	}
	return total
}

func buildDocumentContent(docs []embSchema.Document, trimPrefix string) string {
	if len(docs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, doc := range docs {
		loc := documentLocation(doc, trimPrefix)
		_, _ = b.WriteString(formatDocument(loc, doc.PageContent))
	}
	return b.String()
}

func documentLocation(doc embSchema.Document, trimPrefix string) string {
	loc := strings.TrimPrefix(getStringFromMetadata(doc.Metadata, "path"), trimPrefix)
	if loc == "" {
		loc = getStringFromMetadata(doc.Metadata, "docId")
	}
	return loc
}

func filterSystemDocuments(docs []embSchema.Document, prefixes []string) []embSchema.Document {
	if len(prefixes) == 0 || len(docs) == 0 {
		return nil
	}
	out := make([]embSchema.Document, 0, len(docs))
	for _, doc := range docs {
		if systemdoc.Matches(prefixes, documentMetadataPath(doc.Metadata)) {
			out = append(out, doc)
		}
	}
	return out
}

func documentMetadataPath(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	for _, key := range []string{"path", "docId", "fragmentId"} {
		if v, ok := metadata[key]; ok {
			if s, _ := v.(string); strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

func documentRootID(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	if v, ok := metadata["rootId"]; ok {
		if s, _ := v.(string); strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func buildDocumentRootsMap(docs []embSchema.Document) map[string]string {
	if len(docs) == 0 {
		return nil
	}
	roots := make(map[string]string)
	for _, doc := range docs {
		path := documentMetadataPath(doc.Metadata)
		rootID := documentRootID(doc.Metadata)
		if path == "" || rootID == "" {
			continue
		}
		roots[path] = rootID
	}
	if len(roots) == 0 {
		return nil
	}
	return roots
}
