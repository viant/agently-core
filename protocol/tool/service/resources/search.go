package resources

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/viant/agently-core/internal/agent/systemdoc"
	mcpuri "github.com/viant/agently-core/protocol/mcp/uri"
	svc "github.com/viant/agently-core/protocol/tool/service"
	aug "github.com/viant/agently-core/service/augmenter"
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
	debugf("match request query=%q roots=%v rootIds=%v path=%q", input.Query, input.Roots, input.RootIDs, input.Path)
	res, err := s.buildAugmentedDocuments(ctx, input)
	if err != nil {
		debugf("match error query=%q err=%v", input.Query, err)
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
	debugf("match response query=%q docs=%d cursor=%d next=%d", input.Query, len(pageDocs), output.Cursor, output.NextCursor)
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
	debugf("matchDocuments request query=%q rootIds=%v path=%q", input.Query, input.RootIDs, input.Path)
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
		debugf("matchDocuments error query=%q err=%v", input.Query, err)
		return err
	}
	docs := res.documents
	ranked := uniqueMatchedDocuments(docs, maxDocs)
	if len(ranked) == 0 {
		output.Documents = nil
		debugf("matchDocuments response query=%q docs=0", input.Query)
		return nil
	}
	output.Documents = ranked
	debugf("matchDocuments response query=%q docs=%d", input.Query, len(ranked))
	return nil
}
