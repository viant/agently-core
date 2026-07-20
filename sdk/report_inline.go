package sdk

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	defaultInlineReportGrammar = "dashboard-v1"
	maxInlineReportsPerMessage = 4
	maxInlineReportFragments   = 64
	maxInlineReportBlocks      = 100
	maxInlineReportDataSources = 32
	maxInlineReportRows        = 10000
	maxInlineReportDataBytes   = 5 * 1024 * 1024
	maxInlineMessageDataBytes  = 10 * 1024 * 1024
	maxInlineReportSourceBytes = 5 * 1024 * 1024
	maxInlineEnvelopeOverhead  = 64 * 1024
	maxInlineReportDepth       = 8
)

var inlineDashboardKinds = map[string]bool{
	"dashboard.summary": true, "dashboard.kpiTable": true, "dashboard.compare": true,
	"dashboard.timeline": true, "dashboard.composition": true, "dashboard.dimensions": true,
	"dashboard.geoMap": true, "dashboard.status": true, "dashboard.filters": true,
	"dashboard.feed": true, "dashboard.table": true, "dashboard.report": true,
	"dashboard.detail": true, "dashboard.messages": true, "dashboard.badges": true,
}

var inlineCanonicalKinds = map[string]bool{
	"markdownBlock": true, "filterBarBlock": true, "refinementBarBlock": true,
	"kpiBlock": true, "badgesBlock": true, "chartBlock": true, "tableBlock": true,
	"geoMapBlock": true, "sectionBlock": true, "tabGroupBlock": true,
	"compositeBlock": true, "stepperBlock": true, "infoPanelBlock": true,
	"calloutBlock": true, "kanbanBlock": true, "timelineBlock": true,
	"collectionBlock": true,
}

var inlineReportEnvelopeFields = map[string]bool{
	"version": true, "scope": true, "id": true, "sequence": true, "mode": true,
	"grammar": true, "target": true, "title": true, "subtitle": true,
	"description": true, "theme": true, "blocks": true, "layout": true,
	"removeBlockIds": true, "fallback": true, "metadata": true,
	"datasets": true, "dataSources": true,
}

var inlineDataEnvelopeFields = map[string]bool{
	"version": true, "scope": true, "id": true, "reportRef": true,
	"sequence": true, "format": true, "mode": true, "data": true,
}

type inlineReportState struct {
	assembly  *RenderedReportAssembly
	source    map[string]interface{}
	seen      map[int]string
	maxSeq    int
	started   bool
	committed bool
	fragments int
}

// AssembleRenderedReports applies normalized forge-data v2 and forge-report
// transactions in transcript order. It is intentionally platform-neutral:
// Forge performs grammar validation and canonical ReportDocument lowering.
func AssembleRenderedReports(parts []*RenderedContentPart) ([]*RenderedReportAssembly, []*RenderedContentWarning) {
	return assembleRenderedReports(parts, true)
}

func assembleRenderedReports(parts []*RenderedContentPart, complete bool) ([]*RenderedReportAssembly, []*RenderedContentWarning) {
	states := map[string]*inlineReportState{}
	order := make([]string, 0)
	warnings := make([]*RenderedContentWarning, 0)

	stateFor := func(scope, id string) *inlineReportState {
		scope = normalizeInlineSegment(scope)
		if scope == "" {
			scope = "message"
		}
		id = normalizeInlineSegment(id)
		key := scope + ":" + id
		if state := states[key]; state != nil {
			return state
		}
		if len(states) >= maxInlineReportsPerMessage {
			return nil
		}
		state := &inlineReportState{
			assembly: &RenderedReportAssembly{Scope: scope, ID: id, Status: "pending", DataSources: map[string]*RenderedData{}},
			source:   map[string]interface{}{}, seen: map[int]string{},
		}
		states[key] = state
		order = append(order, key)
		return state
	}

	for _, part := range parts {
		if part == nil {
			continue
		}
		if part.Data != nil && part.Data.Version == 2 {
			data := part.Data
			if strings.TrimSpace(data.ReportRef) == "" {
				warnings = append(warnings, inlineWarning("REPORT_DATA_REF_REQUIRED", "forge-data version 2 requires reportRef.", "", data.Sequence, "forge-data", "reportRef"))
				continue
			}
			if !safeInlineSegment(data.ReportRef) || (strings.TrimSpace(data.Scope) != "" && !safeInlineSegment(data.Scope)) {
				warnings = append(warnings, inlineWarning("REPORT_ID_INVALID", "scope and reportRef must use letters, numbers, dots, underscores, or hyphens.", data.ReportRef, data.Sequence, "forge-data", "reportRef"))
				continue
			}
			state := stateFor(data.Scope, data.ReportRef)
			if state == nil {
				warnings = append(warnings, inlineWarning("REPORT_LIMIT_EXCEEDED", "No more than four inline reports may be assembled in one assistant message.", data.ReportRef, data.Sequence, "forge-data", "reportRef"))
				continue
			}
			canonical := canonicalRaw(part.Payload)
			if canonical == "" {
				canonical = canonicalValue(data)
			}
			if state.rejectPostCommit(data.Sequence, canonical, &warnings, "forge-data") || !state.acceptSequence(data.Sequence, canonical, &warnings, "forge-data") {
				continue
			}
			state.fragments++
			if state.fragments > maxInlineReportFragments {
				warnings = append(warnings, inlineWarning("REPORT_FRAGMENT_LIMIT_EXCEEDED", "An inline report may contain at most 64 transactions.", state.assembly.ID, data.Sequence, "forge-data", "sequence"))
				continue
			}
			previousData := cloneInlineDataSources(state.assembly.DataSources)
			if err := validateInlineEnvelopeFields(part.Payload, inlineDataEnvelopeFields, "forge-data"); err != nil {
				state.assembly.DataSources = previousData
				warnings = append(warnings, inlineWarning("REPORT_DATA_INVALID", err.Error(), state.assembly.ID, data.Sequence, "forge-data", "$", "", data.ID))
			} else if err := preflightInlineData(states, state, data); err != nil {
				state.assembly.DataSources = previousData
				warnings = append(warnings, inlineWarning("REPORT_DATA_LIMIT_EXCEEDED", err.Error(), state.assembly.ID, data.Sequence, "forge-data", "data", "", data.ID))
			} else if err := applyInlineData(state, data); err != nil {
				state.assembly.DataSources = previousData
				warnings = append(warnings, inlineWarning("REPORT_DATA_INVALID", err.Error(), state.assembly.ID, data.Sequence, "forge-data", "data", "", data.ID))
			} else if err := validateInlineDataLimits(states); err != nil {
				state.assembly.DataSources = previousData
				warnings = append(warnings, inlineWarning("REPORT_DATA_LIMIT_EXCEEDED", err.Error(), state.assembly.ID, data.Sequence, "forge-data", "data", "", data.ID))
			}
			continue
		}

		if part.Report == nil {
			continue
		}
		tx := part.Report
		if !safeInlineSegment(tx.ID) || (strings.TrimSpace(tx.Scope) != "" && !safeInlineSegment(tx.Scope)) {
			warnings = append(warnings, inlineWarning("REPORT_ID_INVALID", "scope and report id must use letters, numbers, dots, underscores, or hyphens.", tx.ID, tx.Sequence, "forge-report", "id"))
			continue
		}
		state := stateFor(tx.Scope, tx.ID)
		if state == nil {
			warnings = append(warnings, inlineWarning("REPORT_LIMIT_EXCEEDED", "No more than four inline reports may be assembled in one assistant message.", tx.ID, tx.Sequence, "forge-report", "id"))
			continue
		}
		canonical := canonicalRaw(tx.Payload)
		if state.rejectPostCommit(tx.Sequence, canonical, &warnings, "forge-report") || !state.acceptSequence(tx.Sequence, canonical, &warnings, "forge-report") {
			continue
		}
		state.fragments++
		if state.fragments > maxInlineReportFragments {
			warnings = append(warnings, inlineWarning("REPORT_FRAGMENT_LIMIT_EXCEEDED", "An inline report may contain at most 64 transactions.", state.assembly.ID, tx.Sequence, "forge-report", "sequence"))
			continue
		}
		previousSource := cloneInlineMap(state.source)
		previousStarted := state.started
		previousGrammar := state.assembly.Grammar
		previousStatus := state.assembly.Status
		previousResetVersion := state.assembly.ResetVersion
		previousCommitted := state.committed
		err := applyInlineReport(state, tx)
		if err == nil {
			err = validateInlineReportState(state)
		}
		if err != nil {
			if tx.Mode != "commit" {
				state.source = previousSource
				state.started = previousStarted
				state.assembly.Grammar = previousGrammar
				state.assembly.Status = previousStatus
				state.assembly.ResetVersion = previousResetVersion
			} else {
				state.committed = previousCommitted
				state.assembly.Status = "incomplete"
			}
			warnings = append(warnings, inlineWarning("REPORT_TRANSACTION_INVALID", err.Error(), state.assembly.ID, tx.Sequence, "forge-report", "mode", inlineTransactionBlockID(tx.Payload), ""))
		}
	}

	reports := make([]*RenderedReportAssembly, 0, len(order))
	for _, key := range order {
		state := states[key]
		if !state.started {
			state.assembly.Status = "orphaned"
			warnings = append(warnings, inlineWarning("REPORT_DATA_ORPHANED", "Progressive report data has no matching forge-report start transaction.", state.assembly.ID, state.maxSeq, "forge-data", "reportRef"))
		} else if !state.committed {
			if missing := state.missingSequences(); len(missing) > 0 {
				state.assembly.Status = "incomplete"
				warnings = append(warnings, inlineWarning("REPORT_SEQUENCE_GAP", fmt.Sprintf("Report sequence is missing transaction %d.", missing[0]), state.assembly.ID, missing[0], "forge-report", "sequence"))
			} else if complete {
				// NormalizeRenderedContent receives a complete content snapshot. A later
				// streaming update is reassembled from scratch, so implicit commit is safe.
				state.assembly.Status = "committed"
			} else {
				state.assembly.Status = "rendering"
			}
		}
		state.publishSource()
		reports = append(reports, state.assembly)
	}
	return reports, warnings
}

func (s *inlineReportState) acceptSequence(sequence int, canonical string, warnings *[]*RenderedContentWarning, fence string) bool {
	if sequence <= 0 {
		*warnings = append(*warnings, inlineWarning("REPORT_SEQUENCE_REQUIRED", "Progressive transactions require a positive sequence.", s.assembly.ID, sequence, fence, "sequence"))
		return false
	}
	if prior, ok := s.seen[sequence]; ok {
		if prior != canonical {
			*warnings = append(*warnings, inlineWarning("REPORT_SEQUENCE_CONFLICT", "The sequence was replayed with different content.", s.assembly.ID, sequence, fence, "sequence"))
		}
		return false
	}
	if sequence < s.maxSeq {
		s.seen[sequence] = canonical
		*warnings = append(*warnings, inlineWarning("REPORT_SEQUENCE_STALE", "A lower sequence arrived after a newer transaction and was ignored.", s.assembly.ID, sequence, fence, "sequence"))
		return false
	}
	s.seen[sequence] = canonical
	if sequence > s.maxSeq {
		s.maxSeq = sequence
		s.assembly.Sequence = sequence
	}
	return true
}

func (s *inlineReportState) rejectPostCommit(sequence int, canonical string, warnings *[]*RenderedContentWarning, fence string) bool {
	if !s.committed {
		return false
	}
	if prior, ok := s.seen[sequence]; ok {
		if prior != canonical {
			*warnings = append(*warnings, inlineWarning("REPORT_SEQUENCE_CONFLICT", "The sequence was replayed with different content.", s.assembly.ID, sequence, fence, "sequence"))
		}
		return true
	}
	*warnings = append(*warnings, inlineWarning("REPORT_ALREADY_COMMITTED", "The report assembly is already committed.", s.assembly.ID, sequence, fence, "sequence"))
	return true
}

func (s *inlineReportState) missingSequences() []int {
	missing := make([]int, 0)
	for sequence := 1; sequence <= s.maxSeq; sequence++ {
		if _, ok := s.seen[sequence]; !ok {
			missing = append(missing, sequence)
		}
	}
	return missing
}

func (s *inlineReportState) publishSource() {
	if len(s.source) == 0 {
		return
	}
	payload, err := json.Marshal(s.source)
	if err == nil {
		s.assembly.Source = payload
	}
}

func applyInlineData(state *inlineReportState, data *RenderedData) error {
	id := strings.TrimSpace(data.ID)
	if id == "" {
		return fmt.Errorf("forge-data.id is required")
	}
	if !safeInlineSegment(id) {
		return fmt.Errorf("datasource id %q must use letters, numbers, dots, underscores, or hyphens", id)
	}
	format := strings.ToLower(strings.TrimSpace(data.Format))
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		return fmt.Errorf("unsupported forge-data format %q", format)
	}
	mode := strings.ToLower(strings.TrimSpace(data.Mode))
	if mode == "" {
		mode = "replace"
	}
	if mode != "replace" && mode != "append" && mode != "patch" {
		return fmt.Errorf("unsupported forge-data mode %q", mode)
	}
	next := *data
	next.Format, next.Mode = format, mode
	existing := state.assembly.DataSources[id]
	if existing == nil || mode == "replace" {
		next.Payload = append(json.RawMessage(nil), data.Payload...)
		state.assembly.DataSources[id] = &next
		return nil
	}
	if format != "json" || strings.ToLower(existing.Format) == "csv" {
		return fmt.Errorf("%s requires JSON data", mode)
	}
	var before, incoming interface{}
	if json.Unmarshal(existing.Payload, &before) != nil || json.Unmarshal(data.Payload, &incoming) != nil {
		return fmt.Errorf("%s requires valid JSON data", mode)
	}
	switch mode {
	case "append":
		left, leftOK := before.([]interface{})
		right, rightOK := incoming.([]interface{})
		if !leftOK || !rightOK {
			return fmt.Errorf("append requires row arrays")
		}
		if len(left)+len(right) > maxInlineReportRows {
			return fmt.Errorf("a static datasource may contain at most %d rows", maxInlineReportRows)
		}
		incoming = append(left, right...)
	case "patch":
		left, leftOK := before.(map[string]interface{})
		right, rightOK := incoming.(map[string]interface{})
		if !leftOK || !rightOK {
			return fmt.Errorf("patch requires JSON objects")
		}
		incoming = mergeJSON(left, right)
	}
	next.Payload, _ = json.Marshal(incoming)
	state.assembly.DataSources[id] = &next
	return nil
}

func applyInlineReport(state *inlineReportState, tx *RenderedReport) error {
	if tx.Version != 1 {
		return fmt.Errorf("unsupported forge-report version %d", tx.Version)
	}
	mode := strings.ToLower(strings.TrimSpace(tx.Mode))
	if mode == "" {
		return fmt.Errorf("forge-report.mode is required")
	}
	var envelope map[string]interface{}
	if json.Unmarshal(tx.Payload, &envelope) != nil {
		return fmt.Errorf("forge-report payload must be a JSON object")
	}
	if err := validateInlineEnvelopeFields(tx.Payload, inlineReportEnvelopeFields, "forge-report"); err != nil {
		return err
	}
	grammar := strings.ToLower(strings.TrimSpace(tx.Grammar))
	if grammar == "" {
		if value, ok := envelope["grammar"].(string); ok {
			grammar = strings.ToLower(strings.TrimSpace(value))
		}
	}
	if grammar == "" {
		grammar = defaultInlineReportGrammar
	}
	if grammar != "dashboard-v1" && grammar != "report-document-v1" {
		return fmt.Errorf("unsupported report grammar %q", grammar)
	}

	switch mode {
	case "start":
		if state.started {
			return fmt.Errorf("report start was already accepted")
		}
		state.started = true
		state.assembly.Grammar = grammar
		state.assembly.Status = "rendering"
		state.source = reportSource(envelope)
		return validateInlineBlockIDs(state.source)
	case "append", "patch", "replace", "commit":
		if !state.started {
			return fmt.Errorf("report %s requires an accepted start transaction", mode)
		}
	default:
		return fmt.Errorf("unsupported report mode %q", mode)
	}
	if tx.Grammar != "" && grammar != state.assembly.Grammar {
		return fmt.Errorf("report grammar is immutable after start")
	}

	switch mode {
	case "append":
		return appendInlineReport(state, tx, envelope)
	case "patch":
		return patchInlineReport(state, envelope)
	case "replace":
		if strings.TrimSpace(tx.Grammar) == "" {
			return fmt.Errorf("report replace must restate the established grammar")
		}
		state.source = reportSource(envelope)
		state.assembly.ResetVersion++
		return validateInlineBlockIDs(state.source)
	case "commit":
		if missing := state.missingSequences(); len(missing) > 0 {
			state.assembly.Status = "incomplete"
			return fmt.Errorf("cannot commit with missing sequence %d", missing[0])
		}
		state.committed = true
		state.assembly.Status = "committed"
	}
	return nil
}

func validateInlineEnvelopeFields(payload json.RawMessage, allowed map[string]bool, label string) error {
	var envelope map[string]json.RawMessage
	if len(payload) == 0 || json.Unmarshal(payload, &envelope) != nil || envelope == nil {
		return fmt.Errorf("%s payload must be a JSON object", label)
	}
	for key := range envelope {
		if !allowed[key] {
			return fmt.Errorf("%s contains unknown field %q; future fields belong under metadata.extensions", label, key)
		}
	}
	return nil
}

func appendInlineReport(state *inlineReportState, tx *RenderedReport, envelope map[string]interface{}) error {
	var target *RenderedReportTarget
	if tx.Target != nil {
		copyTarget := *tx.Target
		target = &copyTarget
	}
	if target == nil {
		target = &RenderedReportTarget{Kind: "report", Ref: "root", Position: "append"}
	}
	if target.Kind == "" {
		target.Kind = "report"
	}
	if target.Ref == "" {
		target.Ref = "root"
	}
	if target.Position == "" {
		target.Position = "append"
	}
	incoming, _ := envelope["blocks"].([]interface{})
	if target.Kind == "report" {
		if target.Ref != "root" || target.Position != "append" {
			return fmt.Errorf("the report root supports append only")
		}
		return appendBlocks(state.source, incoming)
	}
	if state.assembly.Grammar != "report-document-v1" {
		return fmt.Errorf("block targets require report-document-v1")
	}
	parent := findInlineBlock(state.source["blocks"], target.Ref)
	if parent == nil {
		return fmt.Errorf("target block %q does not exist", target.Ref)
	}
	parentKind := strings.TrimSpace(fmt.Sprint(parent["kind"]))
	if target.Slot != "childBlockIds" && target.Slot != "sectionIds" {
		return fmt.Errorf("unsupported target slot %q", target.Slot)
	}
	if target.Slot == "childBlockIds" && parentKind != "compositeBlock" {
		return fmt.Errorf("childBlockIds requires a compositeBlock target")
	}
	if target.Slot == "sectionIds" && parentKind != "tabGroupBlock" {
		return fmt.Errorf("sectionIds requires a tabGroupBlock target")
	}
	if target.Position != "append" {
		return fmt.Errorf("unsupported target position %q", target.Position)
	}
	ids := make([]interface{}, 0, len(incoming))
	for _, value := range incoming {
		block, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("targeted blocks must be objects")
		}
		if target.Slot == "sectionIds" && strings.TrimSpace(fmt.Sprint(block["kind"])) != "sectionBlock" {
			return fmt.Errorf("sectionIds accepts sectionBlock entries only")
		}
		id, err := inlineBlockID(block)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := appendBlocks(state.source, incoming); err != nil {
		return err
	}
	existing, _ := parent[target.Slot].([]interface{})
	parent[target.Slot] = append(existing, ids...)
	return validateInlineBlockIDs(state.source)
}

func patchInlineReport(state *inlineReportState, envelope map[string]interface{}) error {
	patch := reportSource(envelope)
	if incoming, ok := patch["blocks"].([]interface{}); ok {
		delete(patch, "blocks")
		blocks, _ := state.source["blocks"].([]interface{})
		for _, value := range incoming {
			blockPatch, ok := value.(map[string]interface{})
			if !ok {
				return fmt.Errorf("block patches must be objects")
			}
			id, _ := blockPatch["id"].(string)
			block := findInlineBlock(blocks, id)
			if block == nil {
				return fmt.Errorf("patch references unknown block %q", id)
			}
			mergeJSON(block, blockPatch)
		}
	}
	mergeJSON(state.source, patch)
	if removals, ok := envelope["removeBlockIds"].([]interface{}); ok {
		for _, value := range removals {
			state.source["blocks"] = removeInlineBlock(state.source["blocks"], fmt.Sprint(value))
		}
	}
	return validateInlineBlockIDs(state.source)
}

func appendBlocks(source map[string]interface{}, incoming []interface{}) error {
	blocks, _ := source["blocks"].([]interface{})
	for _, value := range incoming {
		block, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("appended blocks must be objects")
		}
		id, err := inlineBlockID(block)
		if err != nil {
			return err
		}
		if findInlineBlock(blocks, id) != nil {
			return fmt.Errorf("duplicate block id %q", id)
		}
		blocks = append(blocks, block)
	}
	source["blocks"] = blocks
	return nil
}

func validateInlineBlockIDs(source map[string]interface{}) error {
	seen := map[string]bool{}
	blocks, _ := source["blocks"].([]interface{})
	for _, value := range blocks {
		block, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("report blocks must be objects")
		}
		id, err := inlineBlockID(block)
		if err != nil {
			return err
		}
		if seen[id] {
			return fmt.Errorf("duplicate block id %q", id)
		}
		seen[id] = true
	}
	return nil
}

func findInlineBlock(value interface{}, id string) map[string]interface{} {
	items, _ := value.([]interface{})
	for _, item := range items {
		block, ok := item.(map[string]interface{})
		if ok && strings.TrimSpace(fmt.Sprint(block["id"])) == strings.TrimSpace(id) {
			return block
		}
	}
	return nil
}

func removeInlineBlock(value interface{}, id string) interface{} {
	items, ok := value.([]interface{})
	if !ok {
		return value
	}
	result := make([]interface{}, 0, len(items))
	for _, item := range items {
		block, ok := item.(map[string]interface{})
		if ok && strings.TrimSpace(fmt.Sprint(block["id"])) == strings.TrimSpace(id) {
			continue
		}
		result = append(result, item)
	}
	return result
}

func reportSource(envelope map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}
	for key, value := range envelope {
		switch key {
		case "version", "scope", "id", "sequence", "mode", "grammar", "target", "removeBlockIds":
			continue
		default:
			result[key] = value
		}
	}
	if _, ok := result["blocks"]; !ok {
		result["blocks"] = []interface{}{}
	}
	return result
}

func mergeJSON(target, patch map[string]interface{}) map[string]interface{} {
	for key, value := range patch {
		if value == nil {
			delete(target, key)
			continue
		}
		patchObject, patchOK := value.(map[string]interface{})
		targetObject, targetOK := target[key].(map[string]interface{})
		if patchOK && targetOK {
			target[key] = mergeJSON(targetObject, patchObject)
			continue
		}
		target[key] = value
	}
	return target
}

func canonicalRaw(value json.RawMessage) string {
	var decoded interface{}
	if json.Unmarshal(value, &decoded) != nil {
		return string(bytes.TrimSpace(value))
	}
	payload, _ := json.Marshal(decoded)
	return string(payload)
}

func canonicalValue(value interface{}) string {
	payload, _ := json.Marshal(value)
	return canonicalRaw(payload)
}

func cloneInlineMap(value map[string]interface{}) map[string]interface{} {
	if value == nil {
		return map[string]interface{}{}
	}
	payload, _ := json.Marshal(value)
	result := map[string]interface{}{}
	_ = json.Unmarshal(payload, &result)
	return result
}

func cloneInlineDataSources(value map[string]*RenderedData) map[string]*RenderedData {
	result := make(map[string]*RenderedData, len(value))
	for id, source := range value {
		if source == nil {
			continue
		}
		copySource := *source
		copySource.Payload = append(json.RawMessage(nil), source.Payload...)
		result[id] = &copySource
	}
	return result
}

func inlineBlockID(block map[string]interface{}) (string, error) {
	value, ok := block["id"].(string)
	id := strings.TrimSpace(value)
	if !ok || id == "" {
		return "", fmt.Errorf("every progressive block requires a stable id")
	}
	if !safeInlineSegment(id) {
		return "", fmt.Errorf("block id %q must use letters, numbers, dots, underscores, or hyphens", id)
	}
	return id, nil
}

func validateInlineDataLimits(states map[string]*inlineReportState) error {
	messageBytes := 0
	for _, state := range states {
		if len(state.assembly.DataSources) > maxInlineReportDataSources {
			return fmt.Errorf("an inline report may contain at most %d datasources", maxInlineReportDataSources)
		}
		reportBytes := 0
		for _, source := range state.assembly.DataSources {
			if source == nil {
				continue
			}
			payload := inlineDecodedPayload(source)
			reportBytes += len(payload)
			if strings.EqualFold(source.Format, "csv") {
				rows, err := inlineCSVRowCount(payload)
				if err != nil {
					return fmt.Errorf("datasource %q contains invalid CSV: %w", source.ID, err)
				}
				if rows > maxInlineReportRows {
					return fmt.Errorf("a static datasource may contain at most %d rows", maxInlineReportRows)
				}
				continue
			}
			var rows []interface{}
			if json.Unmarshal(source.Payload, &rows) == nil && len(rows) > maxInlineReportRows {
				return fmt.Errorf("a static datasource may contain at most %d rows", maxInlineReportRows)
			}
		}
		if reportBytes > maxInlineReportDataBytes {
			return fmt.Errorf("static data for one report may not exceed 5 MB")
		}
		messageBytes += reportBytes
	}
	if messageBytes > maxInlineMessageDataBytes {
		return fmt.Errorf("static report data in one assistant message may not exceed 10 MB")
	}
	return nil
}

func preflightInlineData(states map[string]*inlineReportState, state *inlineReportState, data *RenderedData) error {
	if state == nil || state.assembly == nil || data == nil {
		return fmt.Errorf("inline datasource transaction is unavailable")
	}
	id := strings.TrimSpace(data.ID)
	existing := state.assembly.DataSources[id]
	if existing == nil && len(state.assembly.DataSources) >= maxInlineReportDataSources {
		return fmt.Errorf("an inline report may contain at most %d datasources", maxInlineReportDataSources)
	}
	incomingBytes := len(inlineDecodedPayload(data))
	if incomingBytes > maxInlineReportDataBytes {
		return fmt.Errorf("static data for one report may not exceed 5 MB")
	}
	format := strings.ToLower(strings.TrimSpace(data.Format))
	if format == "" {
		format = "json"
	}
	mode := strings.ToLower(strings.TrimSpace(data.Mode))
	if mode == "" {
		mode = "replace"
	}
	if format == "csv" {
		rows, err := inlineCSVRowCount(inlineDecodedPayload(data))
		if err != nil {
			return fmt.Errorf("datasource %q contains invalid CSV: %w", id, err)
		}
		if rows > maxInlineReportRows {
			return fmt.Errorf("a static datasource may contain at most %d rows", maxInlineReportRows)
		}
	} else if rows, isArray, err := inlineJSONArrayRowCount(data.Payload, maxInlineReportRows); err != nil {
		return fmt.Errorf("datasource %q contains invalid JSON: %w", id, err)
	} else if isArray && rows > maxInlineReportRows {
		return fmt.Errorf("a static datasource may contain at most %d rows", maxInlineReportRows)
	}

	existingBytes := 0
	if existing != nil {
		existingBytes = len(inlineDecodedPayload(existing))
	}
	projectedSourceBytes := incomingBytes
	if existing != nil && (mode == "append" || mode == "patch") {
		projectedSourceBytes += existingBytes
		if projectedSourceBytes > maxInlineReportDataBytes {
			return fmt.Errorf("static data for one report may not exceed 5 MB")
		}
		if mode == "append" {
			existingRows, existingArray, err := inlineJSONArrayRowCount(existing.Payload, maxInlineReportRows)
			if err != nil || !existingArray {
				return fmt.Errorf("append requires valid JSON row arrays")
			}
			incomingRows, incomingArray, err := inlineJSONArrayRowCount(data.Payload, maxInlineReportRows)
			if err != nil || !incomingArray {
				return fmt.Errorf("append requires valid JSON row arrays")
			}
			if existingRows+incomingRows > maxInlineReportRows {
				return fmt.Errorf("a static datasource may contain at most %d rows", maxInlineReportRows)
			}
		}
	}

	messageBytes := 0
	reportBytes := 0
	for _, candidate := range states {
		for candidateID, source := range candidate.assembly.DataSources {
			if candidate == state && candidateID == id {
				continue
			}
			sourceBytes := len(inlineDecodedPayload(source))
			messageBytes += sourceBytes
			if candidate == state {
				reportBytes += sourceBytes
			}
		}
	}
	reportBytes += projectedSourceBytes
	if reportBytes > maxInlineReportDataBytes {
		return fmt.Errorf("static data for one report may not exceed 5 MB")
	}
	messageBytes += projectedSourceBytes
	if messageBytes > maxInlineMessageDataBytes {
		return fmt.Errorf("static report data in one assistant message may not exceed 10 MB")
	}
	return nil
}

// inlineJSONArrayRowCount streams over array elements so oversized row sets
// are rejected before decoding every row into interface maps.
func inlineJSONArrayRowCount(payload []byte, stopAfter int) (int, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil {
		return 0, false, err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '[' {
		return 0, false, nil
	}
	rows := 0
	for decoder.More() {
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return 0, true, err
		}
		rows++
		if stopAfter > 0 && rows > stopAfter {
			return rows, true, nil
		}
	}
	if _, err := decoder.Token(); err != nil {
		return 0, true, err
	}
	return rows, true, nil
}

func inlineDecodedPayload(source *RenderedData) []byte {
	if source == nil {
		return nil
	}
	if strings.EqualFold(source.Format, "csv") {
		var text string
		if json.Unmarshal(source.Payload, &text) == nil {
			return []byte(text)
		}
	}
	return source.Payload
}

func inlineCSVRowCount(payload []byte) (int, error) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return 0, nil
	}
	reader := csv.NewReader(bytes.NewReader(payload))
	reader.FieldsPerRecord = -1
	count := 0
	for {
		_, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		count++
	}
	if count <= 1 {
		return 0, nil
	}
	return count - 1, nil
}

func validateInlineReportState(state *inlineReportState) error {
	if !state.started {
		return nil
	}
	blocks, _ := state.source["blocks"].([]interface{})
	if payload, err := json.Marshal(state.source); err != nil || len(payload) > maxInlineReportSourceBytes {
		return fmt.Errorf("assembled inline report source may not exceed 5 MB")
	}
	allBlocks := collectInlineReportBlocks(blocks, state.assembly.Grammar)
	if len(allBlocks) > maxInlineReportBlocks {
		return fmt.Errorf("an inline report may contain at most %d blocks", maxInlineReportBlocks)
	}
	if path := inlineForbiddenSourcePath(state.source, "$"); path != "" {
		return fmt.Errorf("report source must not declare credentials, ownership, or connection secrets at %s", path)
	}
	knownBlocks := map[string]bool{}
	blockKinds := map[string]string{}
	for _, block := range allBlocks {
		id, err := inlineBlockID(block)
		if err != nil {
			return err
		}
		if knownBlocks[id] {
			return fmt.Errorf("duplicate block id %q", id)
		}
		kind, _ := block["kind"].(string)
		kind = strings.TrimSpace(kind)
		if state.assembly.Grammar == "dashboard-v1" && !inlineDashboardKinds[kind] {
			return fmt.Errorf("unsupported dashboard block kind %q", kind)
		}
		if state.assembly.Grammar == "report-document-v1" && !inlineCanonicalKinds[kind] {
			return fmt.Errorf("unsupported canonical report block kind %q", kind)
		}
		knownBlocks[id] = true
		blockKinds[id] = kind
	}
	if state.assembly.Grammar == "dashboard-v1" {
		if inlineDashboardDepth(blocks, 0) > maxInlineReportDepth {
			return fmt.Errorf("inline report nesting may not exceed depth %d", maxInlineReportDepth)
		}
	} else if err := validateInlineCanonicalDepth(allBlocks, maxInlineReportDepth); err != nil {
		return err
	}
	if layout, ok := state.source["layout"].(map[string]interface{}); ok {
		if items, ok := layout["items"].([]interface{}); ok {
			for _, value := range items {
				item, _ := value.(map[string]interface{})
				blockID, _ := item["blockId"].(string)
				if blockID != "" && !knownBlocks[blockID] {
					return fmt.Errorf("layout references unknown block %q", blockID)
				}
			}
		}
	}
	available := map[string]bool{}
	for id := range state.assembly.DataSources {
		available[id] = true
	}
	for _, key := range []string{"datasets", "dataSources"} {
		switch declarations := state.source[key].(type) {
		case []interface{}:
			for _, value := range declarations {
				entry, _ := value.(map[string]interface{})
				if id, ok := entry["id"].(string); ok && id != "" {
					available[id] = true
				}
			}
		case map[string]interface{}:
			for id := range declarations {
				available[id] = true
			}
		}
	}
	for _, block := range allBlocks {
		for _, key := range []string{"dataSourceRef", "dataSource", "datasetRef"} {
			if ref, ok := block[key].(string); ok && strings.TrimSpace(ref) != "" && !available[strings.TrimSpace(ref)] {
				return fmt.Errorf("block %q references unavailable datasource %q", block["id"], ref)
			}
		}
		for _, key := range []string{"childBlockIds", "sectionIds"} {
			references, ok := block[key].([]interface{})
			if !ok {
				continue
			}
			for _, value := range references {
				ref, ok := value.(string)
				if !ok || strings.TrimSpace(ref) == "" || !knownBlocks[strings.TrimSpace(ref)] {
					return fmt.Errorf("block %q references unknown block %q in %s", block["id"], ref, key)
				}
				if key == "sectionIds" && blockKinds[strings.TrimSpace(ref)] != "sectionBlock" {
					return fmt.Errorf("block %q sectionIds references non-section block %q", block["id"], ref)
				}
			}
		}
	}
	return nil
}

func inlineForbiddenSourcePath(value interface{}, path string) string {
	forbidden := map[string]bool{
		"ownerid": true, "userid": true, "authheader": true, "authorization": true,
		"authtoken": true, "accesstoken": true, "dsn": true, "dbdsn": true,
		"secret": true, "secrets": true, "secretref": true, "password": true,
		"credentials": true,
	}
	switch actual := value.(type) {
	case map[string]interface{}:
		for key, child := range actual {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "_", ""), "-", ""))
			childPath := path + "." + key
			if forbidden[normalized] {
				return childPath
			}
			if found := inlineForbiddenSourcePath(child, childPath); found != "" {
				return found
			}
		}
	case []interface{}:
		for index, child := range actual {
			if found := inlineForbiddenSourcePath(child, fmt.Sprintf("%s[%d]", path, index)); found != "" {
				return found
			}
		}
	}
	return ""
}

func collectInlineReportBlocks(values []interface{}, grammar string) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(values))
	var walkContainers func(interface{})
	walkContainers = func(value interface{}) {
		switch actual := value.(type) {
		case []interface{}:
			for _, item := range actual {
				block, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				result = append(result, block)
				if grammar == "dashboard-v1" {
					walkDashboardContainers(block, walkContainers)
				}
			}
		}
	}
	walkContainers(values)
	return result
}

func walkDashboardContainers(value interface{}, consume func(interface{})) {
	switch actual := value.(type) {
	case map[string]interface{}:
		for key, child := range actual {
			if key == "containers" {
				consume(child)
				continue
			}
			if key == "dashboard" {
				walkDashboardContainers(child, consume)
			}
		}
	case []interface{}:
		for _, child := range actual {
			walkDashboardContainers(child, consume)
		}
	}
}

// inlineDashboardDepth counts only nested dashboard containers. Deep chart
// configuration, formatter options, and other ordinary JSON objects are not
// report composition and therefore do not consume the composition budget.
func inlineDashboardDepth(values interface{}, parentDepth int) int {
	maxDepth := parentDepth
	items, _ := values.([]interface{})
	for _, value := range items {
		block, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		depth := parentDepth + 1
		if depth > maxDepth {
			maxDepth = depth
		}
		if childDepth := inlineDashboardChildDepth(block, depth); childDepth > maxDepth {
			maxDepth = childDepth
		}
	}
	return maxDepth
}

func inlineDashboardChildDepth(value interface{}, parentDepth int) int {
	maxDepth := parentDepth
	switch actual := value.(type) {
	case map[string]interface{}:
		for key, child := range actual {
			if key == "containers" {
				if depth := inlineDashboardDepth(child, parentDepth); depth > maxDepth {
					maxDepth = depth
				}
				continue
			}
			if depth := inlineDashboardChildDepth(child, parentDepth); depth > maxDepth {
				maxDepth = depth
			}
		}
	case []interface{}:
		for _, child := range actual {
			if depth := inlineDashboardChildDepth(child, parentDepth); depth > maxDepth {
				maxDepth = depth
			}
		}
	}
	return maxDepth
}

func validateInlineCanonicalDepth(blocks []map[string]interface{}, limit int) error {
	byID := make(map[string]map[string]interface{}, len(blocks))
	for _, block := range blocks {
		if id, err := inlineBlockID(block); err == nil {
			byID[id] = block
		}
	}
	visiting := map[string]bool{}
	depths := map[string]int{}
	var depthFor func(string) (int, error)
	depthFor = func(id string) (int, error) {
		if depth := depths[id]; depth > 0 {
			return depth, nil
		}
		if visiting[id] {
			return 0, fmt.Errorf("canonical report composition contains a cycle at block %q", id)
		}
		visiting[id] = true
		depth := 1
		block := byID[id]
		for _, key := range []string{"childBlockIds", "sectionIds"} {
			references, _ := block[key].([]interface{})
			for _, value := range references {
				ref, _ := value.(string)
				childDepth, err := depthFor(strings.TrimSpace(ref))
				if err != nil {
					return 0, err
				}
				if childDepth+1 > depth {
					depth = childDepth + 1
				}
			}
		}
		visiting[id] = false
		depths[id] = depth
		if depth > limit {
			return 0, fmt.Errorf("inline report nesting may not exceed depth %d", limit)
		}
		return depth, nil
	}
	for id := range byID {
		if _, err := depthFor(id); err != nil {
			return err
		}
	}
	return nil
}

func normalizeInlineSegment(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '.' || char == '_' || char == '-' {
			builder.WriteRune(char)
		} else {
			builder.WriteRune('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

func safeInlineSegment(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && normalizeInlineSegment(value) == value
}

func inlineTransactionBlockID(payload json.RawMessage) string {
	var envelope struct {
		Blocks []struct {
			ID string `json:"id"`
		} `json:"blocks"`
	}
	if json.Unmarshal(payload, &envelope) == nil && len(envelope.Blocks) > 0 {
		return strings.TrimSpace(envelope.Blocks[0].ID)
	}
	return ""
}

func inlineWarning(code, message, reportID string, sequence int, fence, path string, identities ...string) *RenderedContentWarning {
	blockID, dataSourceID := "", ""
	if len(identities) > 0 {
		blockID = identities[0]
	}
	if len(identities) > 1 {
		dataSourceID = identities[1]
	}
	suggestedFix := "Correct this report fragment and emit it with the next sequence number."
	if fence == "forge-data" {
		suggestedFix = "Correct this datasource transaction and emit it with the next sequence number."
	}
	if strings.Contains(code, "SEQUENCE") {
		suggestedFix = "Emit a new transaction with the next sequence number; do not reuse a rejected sequence."
	}
	if code == "REPORT_ALREADY_COMMITTED" {
		suggestedFix = "Start a new report instance or save and update the committed report through the report API."
	}
	if code == "REPORT_DATA_ORPHANED" {
		suggestedFix = "Add a matching forge-report start transaction in this assistant message."
	}
	return &RenderedContentWarning{
		Code: code, Message: message, ReportID: reportID, BlockID: blockID, DataSourceID: dataSourceID,
		Sequence: sequence, Fence: fence, Path: path, SuggestedFix: suggestedFix,
	}
}

// ValidateInlineReportWorkspaceReferences gates live datasource declarations
// immediately before backend execution. An empty allowlist denies every
// workspaceRef; callers build the allowlist from the effective authenticated
// user's workspace catalog rather than from fence-authored metadata.
func ValidateInlineReportWorkspaceReferences(assembly *RenderedReportAssembly, allowed []string) []*RenderedContentWarning {
	if assembly == nil || len(assembly.Source) == 0 {
		return nil
	}
	allowlist := make(map[string]bool, len(allowed))
	for _, value := range allowed {
		if id := strings.TrimSpace(value); id != "" {
			allowlist[id] = true
		}
	}
	var source map[string]interface{}
	if json.Unmarshal(assembly.Source, &source) != nil {
		return []*RenderedContentWarning{inlineWarning(
			"REPORT_SOURCE_INVALID", "The assembled report source is not valid JSON.", assembly.ID,
			assembly.Sequence, "forge-report", "$",
		)}
	}
	var warnings []*RenderedContentWarning
	for _, key := range []string{"datasets", "dataSources"} {
		collectInlineWorkspaceDeclarations(source[key], "$."+key, func(entry map[string]interface{}, path string) {
			if !strings.EqualFold(inlineString(entry["kind"]), "workspaceRef") {
				return
			}
			ref := inlineString(entry["dataSourceRef"])
			id := inlineString(entry["id"])
			if ref == "" || !allowlist[ref] {
				warning := inlineWarning(
					"REPORT_WORKSPACE_REF_DENIED",
					fmt.Sprintf("Workspace datasource %q is not available to the effective authenticated user.", ref),
					assembly.ID, assembly.Sequence, "forge-report", path+".dataSourceRef", "", id,
				)
				warning.SuggestedFix = "Use a datasource id from the current workspace catalog or remove the live datasource declaration."
				warnings = append(warnings, warning)
			}
		})
	}
	return warnings
}

func inlineString(value interface{}) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func collectInlineWorkspaceDeclarations(value interface{}, path string, consume func(map[string]interface{}, string)) {
	switch actual := value.(type) {
	case []interface{}:
		for index, item := range actual {
			if entry, ok := item.(map[string]interface{}); ok {
				consume(entry, fmt.Sprintf("%s[%d]", path, index))
			}
		}
	case map[string]interface{}:
		for id, item := range actual {
			if entry, ok := item.(map[string]interface{}); ok {
				if _, exists := entry["id"]; !exists {
					entry = cloneInlineMap(entry)
					entry["id"] = id
				}
				consume(entry, path+"."+id)
			}
		}
	}
}
