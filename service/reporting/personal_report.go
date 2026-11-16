package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/tidwall/sjson"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	useruri "github.com/viant/agently-core/protocol/resource/user"
	svc "github.com/viant/agently-core/protocol/tool/service"
	authsvc "github.com/viant/agently-core/service/auth"
)

var ErrReportHasNoOriginal = fmt.Errorf("personal report has no original source: %w", errors.ErrUnsupported)
var ErrPersonalDefaultUnavailable = errors.New("Your saved default report could not be opened. You can open the original report or choose another default.")
var ErrPersonalDefaultAmbiguous = errors.New("Multiple personal defaults match this report. Please choose a personal report or open the original explicitly.")

// PersonalReportSource is authoring provenance only. It contains no actor grant
// or execution result and cannot authorize a datasource call.
type PersonalReportSource struct {
	Provider    string          `json:"provider,omitempty"`
	URI         string          `json:"uri"`
	Revision    string          `json:"revision"`
	Fingerprint string          `json:"fingerprint"`
	Definition  json.RawMessage `json:"definition,omitempty"`
	ParentURI   string          `json:"parentUri,omitempty"`
}

func personalReportSource(raw json.RawMessage) (*PersonalReportSource, error) {
	var fields struct {
		Metadata struct {
			PersonalReport *PersonalReportSource `json:"personalReport"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	return fields.Metadata.PersonalReport, nil
}

func cleanPersonalDefinition(raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, fmt.Errorf("report definition must be an object")
	}
	result := cloneJSON(raw)
	for _, key := range []string{"reportFill", "reportPrint", "compileState"} {
		if _, exists := fields[key]; !exists {
			continue
		}
		var err error
		result, err = sjson.DeleteBytes(result, key)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func preparePersonalDefinition(original, candidate json.RawMessage, pin *identity.ResolvedResource) (json.RawMessage, error) {
	if pin == nil {
		return nil, identity.ErrResource
	}
	uri, err := identity.ParseResourceURI(pin.URI)
	if err != nil {
		return nil, err
	}
	var source *PersonalReportSource
	if uri.Namespace == "users" {
		source, err = personalReportSource(original)
		if err != nil {
			return nil, err
		}
	}
	// Shared publishers cannot supply the personal copy's root provenance.
	if source == nil {
		snapshot, err := cleanPersonalDefinition(original)
		if err != nil {
			return nil, err
		}
		snapshot, err = clearPersonalReportSource(snapshot)
		if err != nil {
			return nil, err
		}
		source = &PersonalReportSource{Provider: pin.ProviderIdentity, URI: pin.URI, Revision: pin.Selector(), Fingerprint: pin.ContentFingerprint, Definition: snapshot}
	}
	copySource := *source
	copySource.ParentURI = pin.URI
	if len(candidate) == 0 {
		candidate = original
	}
	return personalDefinitionWithSource(candidate, &copySource)
}

func personalCopyTarget(ctx context.Context, input *DuplicateReportRequest) (string, error) {
	target := input.TargetURI
	if target == "" {
		if input.IdempotencyKey == "" {
			return "", fmt.Errorf("personal copy requires a stable operation idempotencyKey")
		}
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(iauth.CanonicalUserID(ctx)+"\x00"+input.IdempotencyKey)).String()
		uri, err := useruri.NewURI("report", iauth.CanonicalUserID(ctx), "copies/"+id)
		if err != nil {
			return "", err
		}
		target = uri.String()
	}
	return target, nil
}

func (s *Service) duplicatePersonalReport(ctx context.Context, input *DuplicateReportRequest, ref identity.ResourceRef) (*SharedArtifact, error) {
	target, err := personalCopyTarget(ctx, input)
	if err != nil {
		return nil, err
	}
	// The receipt binds the caller's selector. The writer admits the source
	// on the first write; an identical retry can recover the existing result.
	copy := primitive.CloneResourceInput{Source: ref, TargetURI: target, TargetWorkspaceID: input.TargetWorkspaceID, Title: input.Title, IdempotencyKey: input.IdempotencyKey}
	if pin := input.ResolvedResource; pin != nil {
		if !pin.ResourceCandidate.Valid() || pin.ProviderIdentity == "" {
			return nil, identity.ErrResource
		}
		copy.Source.Revision = pin.Selector()
		copy.SourceProvider = pin.ProviderIdentity
		copy.ExpectedSourceFingerprint = pin.ContentFingerprint
	}
	var result *primitive.ResourceResult
	if len(input.Definition) == 0 {
		result, err = s.reportResourceService.CloneResource(ctx, copy)
	} else {
		var formatVersion int64
		formatVersion, err = reportDefinitionFormatVersion(input.Definition)
		if err != nil {
			return nil, err
		}
		var manifest json.RawMessage
		manifest, err = json.Marshal(PersonalReportSourceManifest{Operation: "copy", Source: &copy.Source, Provider: copy.SourceProvider, ExpectedSourceFingerprint: copy.ExpectedSourceFingerprint})
		if err != nil {
			return nil, err
		}
		result, err = s.reportResourceService.CreateResource(ctx, primitive.CreateResourceInput{
			URI: target, WorkspaceID: input.TargetWorkspaceID, Title: input.Title,
			FormatVersion: formatVersion, Definition: input.Definition, SourceManifest: manifest, IdempotencyKey: input.IdempotencyKey,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("personal report copy target=%s phase=write: %w", target, err)
	}
	report, err := s.resourceWriteResult(ctx, result)
	if err != nil {
		return nil, fmt.Errorf("personal report copy saved uri=%s phase=readback: %w", target, err)
	}
	return report, nil
}

type ResetReportRequest struct {
	Resource                   *identity.ResourceRef `json:"resource"`
	Latest                     bool                  `json:"latest,omitempty" description:"Resolve the latest original source instead of restoring the recorded original snapshot."`
	ExpectedContentFingerprint string                `json:"expectedContentFingerprint"`
}

// ResetReport uses the existing owner writer and preserves the personal URI.
// Restoring a snapshot does not fetch a revoked/unavailable original.
func (s *Service) ResetReport(ctx context.Context, input *ResetReportRequest) (*SharedArtifact, error) {
	if input == nil || input.Resource == nil || input.ExpectedContentFingerprint == "" {
		return nil, fmt.Errorf("reset requires an exact report and expectedContentFingerprint")
	}
	uri, parseErr := identity.ParseResourceURI(input.Resource.URI)
	if parseErr != nil || uri.Namespace != "users" {
		return nil, fmt.Errorf("reset requires a personal report")
	}
	raw, pin, err := s.readAuthoringResource(ctx, *input.Resource, nil, "report.edit", "")
	if err != nil {
		return nil, err
	}
	if pin.ContentFingerprint != input.ExpectedContentFingerprint {
		return nil, identity.ErrResourceStale
	}
	source, err := personalReportSource(raw)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, ErrReportHasNoOriginal
	}
	original := source.Definition
	var originalPin *identity.ResolvedResource
	if input.Latest {
		original, originalPin, err = s.readAuthoringResource(ctx, identity.ResourceRef{URI: source.URI}, nil, "report.retrieve", source.Provider)
		if err != nil {
			return nil, err
		}
	} else {
		// Retain the exact recorded revision without fabricating a new grant.
		definition, err := preservePersonalLineage(raw, original)
		if err != nil {
			return nil, err
		}
		definition, err = retainPersonalReportTitle(raw, definition)
		if err != nil {
			return nil, err
		}
		definition, err = bindPersonalReportRoot(definition, source.URI, pin.URI)
		if err != nil {
			return nil, err
		}
		formatVersion, err := reportDefinitionFormatVersion(definition)
		if err != nil {
			return nil, err
		}
		result, err := s.reportResourceService.UpdateDraft(ctx, pin.URI, primitive.UpdateDraftInput{FormatVersion: formatVersion, ExpectedContentFingerprint: pin.ContentFingerprint, Definition: definition, SourceManifest: json.RawMessage(`{"operation":"resetRecorded"}`)})
		if err != nil {
			return nil, err
		}
		return s.resourceWriteResult(ctx, result)
	}
	snapshot, err := cleanPersonalDefinition(original)
	if err != nil {
		return nil, err
	}
	snapshot, err = clearPersonalReportSource(snapshot)
	if err != nil {
		return nil, err
	}
	latestSource := &PersonalReportSource{Provider: originalPin.ProviderIdentity, URI: originalPin.URI, Revision: originalPin.Selector(), Fingerprint: originalPin.ContentFingerprint, Definition: snapshot, ParentURI: source.ParentURI}
	definition, err := personalDefinitionWithSource(original, latestSource)
	if err != nil {
		return nil, err
	}
	definition, err = retainPersonalReportTitle(raw, definition)
	if err != nil {
		return nil, err
	}
	definition, err = bindPersonalReportRoot(definition, originalPin.URI, pin.URI)
	if err != nil {
		return nil, err
	}
	formatVersion, err := reportDefinitionFormatVersion(definition)
	if err != nil {
		return nil, err
	}
	result, err := s.reportResourceService.UpdateDraft(ctx, pin.URI, primitive.UpdateDraftInput{FormatVersion: formatVersion, ExpectedContentFingerprint: pin.ContentFingerprint, Definition: definition, SourceManifest: json.RawMessage(`{"operation":"resetLatest"}`)})
	if err != nil {
		return nil, err
	}
	return s.resourceWriteResult(ctx, result)
}

// preservePersonalLineage keeps authoring provenance out of model-edited fields.
func preservePersonalLineage(original, candidate json.RawMessage) (json.RawMessage, error) {
	source, err := personalReportSource(original)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return clearPersonalReportSource(candidate)
	}
	return personalDefinitionWithSource(candidate, source)
}

func personalDefinitionWithSource(candidate json.RawMessage, source *PersonalReportSource) (json.RawMessage, error) {
	raw, err := cleanPersonalDefinition(candidate)
	if err != nil {
		return nil, err
	}
	var header struct {
		Metadata map[string]json.RawMessage `json:"metadata"`
	}
	if err = json.Unmarshal(raw, &header); err != nil {
		return nil, err
	}
	copySource := *source
	copySource.Definition = nil
	encoded, err := json.Marshal(&copySource)
	if err != nil {
		return nil, err
	}
	if len(source.Definition) > 0 {
		if !json.Valid(source.Definition) {
			return nil, fmt.Errorf("invalid original report snapshot")
		}
		encoded, err = sjson.SetRawBytes(encoded, "definition", source.Definition)
		if err != nil {
			return nil, err
		}
	}
	result, err := sjson.SetRawBytes(raw, "metadata.personalReport", encoded)
	if err != nil {
		return nil, err
	}
	actual, err := personalReportSource(result)
	if err != nil {
		return nil, fmt.Errorf("invalid personal report provenance: %w: %w", identity.ErrResource, err)
	}
	if !samePersonalReportSource(actual, source) {
		return nil, fmt.Errorf("ambiguous reserved personal report provenance: %w", identity.ErrResource)
	}
	return result, nil
}

func (s *Service) resetReportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ResetReportRequest)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*SharedArtifact)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	result, err := s.ResetReport(ctx, input)
	if err != nil {
		return err
	}
	*output = *personalReportReceipt(result)
	return nil
}

func retainPersonalReportTitle(current, candidate json.RawMessage) (json.RawMessage, error) {
	var before map[string]json.RawMessage
	if err := json.Unmarshal(current, &before); err != nil {
		return nil, err
	}
	for _, key := range []string{"reportDocument", "reportSpec"} {
		var document struct {
			Title string `json:"title"`
		}
		if len(before[key]) == 0 {
			continue
		}
		if err := json.Unmarshal(before[key], &document); err != nil {
			return nil, err
		}
		if document.Title != "" {
			return setPersonalReportTitle(candidate, document.Title)
		}
	}
	return candidate, nil
}

func setPersonalReportTitle(raw json.RawMessage, title string) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	result := cloneJSON(raw)
	for _, key := range []string{"reportDocument", "reportSpec"} {
		if len(fields[key]) == 0 || string(fields[key]) == "null" {
			continue
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(fields[key], &object); err != nil {
			return nil, err
		}
		var err error
		result, err = sjson.SetBytes(result, key+".title", title)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func personalReportReceipt(report *SharedArtifact) *SharedArtifact {
	receipt := &SharedArtifact{ArtifactID: report.ArtifactID, ArtifactRef: report.ArtifactRef, ReportID: report.ReportID, Title: report.Title, OwnerID: report.OwnerID, Resource: cloneResolvedResource(report.Resource), RowEtag: report.RowEtag, DraftRevision: report.DraftRevision, Lifecycle: report.Lifecycle, Version: report.Version}
	// Return provenance references, never the stored original snapshot or rows.
	if source, err := personalReportSource(report.ResourceDefinition); err == nil && source != nil {
		summary := *source
		summary.Definition = nil
		receipt.Metadata, _ = json.Marshal(map[string]any{"personalReport": &summary})
	}
	return receipt
}

// ValidatePersonalDefault reuses report resolution and stored root provenance.
// This verifies the preference association; it adds no datasource access rule.
func (s *Service) ValidatePersonalDefault(ctx context.Context, entry authsvc.WorkspaceReportDefault) error {
	ref, err := personalDefaultReference(entry.TargetURI)
	if err != nil {
		return &authsvc.WorkspacePreferenceError{Cause: err}
	}
	report, err := s.GetReport(ctx, &GetReportInput{Resource: &ref})
	if err != nil {
		return err
	}
	source, err := personalReportSource(report.ResourceDefinition)
	if err != nil {
		return err
	}
	if source == nil || source.URI != entry.SourceURI || source.Provider != entry.SourceProvider {
		return &authsvc.WorkspacePreferenceError{Cause: fmt.Errorf("personal report does not derive from the requested original")}
	}
	return nil
}

// SetPersonalWorkspacePreferences binds the existing user preference owner at
// service construction. It is consulted only for explicit preference resolution.
func (s *Service) SetPersonalWorkspacePreferences(read func(context.Context) (*authsvc.WorkspacePreferences, error)) {
	s.personalWorkspacePreferences = read
}

func (s *Service) preferredPersonalReport(ctx context.Context, input *GetReportInput) (*SharedArtifact, bool, error) {
	if input == nil || !input.PreferPersonal || input.SourceProvider != "" || s.personalWorkspacePreferences == nil {
		return nil, false, nil
	}
	requested := input.ArtifactRef
	if requested == "" {
		requested = input.ReportID
	}
	if input.Resource != nil {
		requested = input.Resource.URI
	}
	if input.ResolvedResource != nil || (input.Resource != nil && input.Resource.Revision != "") {
		return nil, false, nil // Exact pins and revisions retain their requested target.
	}
	preferences, err := s.personalWorkspacePreferences(ctx)
	if err != nil {
		return nil, true, err
	}
	if preferences == nil {
		return nil, false, nil
	}
	var selected *authsvc.WorkspaceReportDefault
	for _, entry := range preferences.Defaults {
		if entry.SourceURI == requested {
			if selected != nil {
				return nil, true, ErrPersonalDefaultAmbiguous
			}
			copy := entry
			selected = &copy
		}
	}
	if selected == nil {
		return nil, false, nil
	}
	ref, err := personalDefaultReference(selected.TargetURI)
	if err != nil {
		return nil, true, err
	}
	report, err := s.getCompleteReport(ctx, &GetReportInput{Resource: &ref})
	if err != nil {
		var status interface{ HTTPStatusCode() int }
		if errors.Is(err, ErrNotFound) || errors.Is(err, identity.ErrResourceDenied) || errors.Is(err, identity.ErrResourceStale) ||
			errors.As(err, &status) && status.HTTPStatusCode() == 404 {
			return nil, true, fmt.Errorf("%w: %w", ErrPersonalDefaultUnavailable, err)
		}
		return nil, true, err
	}
	source, err := personalReportSource(report.ResourceDefinition)
	if err != nil {
		return nil, true, err
	}
	if source == nil || source.URI != selected.SourceURI || source.Provider != selected.SourceProvider {
		return nil, true, fmt.Errorf("personal default no longer matches its original; choose the original explicitly")
	}
	return report, true, nil
}

// bindPersonalReportRoot updates only the authored copy's self dependency.
// Datasource and builder references retain their producer identities and hashes.
func bindPersonalReportRoot(raw json.RawMessage, previousURI, uri string) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if len(fields["dependencies"]) == 0 {
		return raw, nil
	}
	var dependencies []json.RawMessage
	if err := json.Unmarshal(fields["dependencies"], &dependencies); err != nil {
		return nil, err
	}
	result := raw
	var fingerprint string
	for i, dependency := range dependencies {
		var header struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal(dependency, &header); err != nil {
			return nil, err
		}
		if header.Kind != "report" || header.ID != previousURI && header.ID != uri {
			continue
		}
		if fingerprint == "" {
			document := fields["reportDocument"]
			if len(document) == 0 {
				document = fields["reportSpec"]
			}
			if len(document) == 0 {
				return nil, fmt.Errorf("self report dependency requires a report document or spec")
			}
			fingerprint = identity.ContentFingerprint(document)
		}
		var err error
		path := fmt.Sprintf("dependencies.%d.", i)
		result, err = sjson.SetBytes(result, path+"id", uri)
		if err != nil {
			return nil, err
		}
		result, err = sjson.SetBytes(result, path+"contentFingerprint", fingerprint)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Personal defaults point only to the existing current-user report provider.
// URI ownership is still checked there, through normal report resolution.
func personalDefaultReference(value string) (identity.ResourceRef, error) {
	uri, err := identity.ParseResourceURI(value)
	if err != nil {
		return identity.ResourceRef{}, err
	}
	if uri.Kind != "report" || uri.Namespace != "users" {
		return identity.ResourceRef{}, fmt.Errorf("personal default target must be a user report")
	}
	return identity.ResourceRef{URI: uri.String()}, nil
}

// Use the same owning decoder for readback: JSON path edits and Go struct
// decoding differ on duplicate and case-variant keys. Never accept a write
// whose decoded reserved provenance differs from the server's intention.
func clearPersonalReportSource(raw json.RawMessage) (json.RawMessage, error) {
	result, err := projectWithoutPersonalReportSource(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", identity.ErrResource, err)
	}
	return result, nil
}

func projectWithoutPersonalReportSource(raw json.RawMessage) (json.RawMessage, error) {
	result, err := sjson.DeleteBytes(raw, "metadata.personalReport")
	if err != nil {
		return nil, err
	}
	source, err := personalReportSource(result)
	if err != nil {
		return nil, fmt.Errorf("invalid personal report provenance: %w", err)
	}
	if source != nil {
		return nil, errors.New("ambiguous reserved personal report provenance")
	}
	return result, nil
}
func samePersonalReportSource(a, b *PersonalReportSource) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Provider == b.Provider && a.URI == b.URI && a.Revision == b.Revision && a.Fingerprint == b.Fingerprint && a.ParentURI == b.ParentURI && bytes.Equal(bytes.TrimSpace(a.Definition), bytes.TrimSpace(b.Definition))
}
