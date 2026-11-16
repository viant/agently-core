package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
)

// PersonalReportSourceManifest describes derivation, never caller-supplied
// provenance. A logical copy selects its source on the first authorized write.
// An exact copy also asserts the resolved fingerprint.
type PersonalReportSourceManifest struct {
	Operation                 string                `json:"operation"`
	Source                    *identity.ResourceRef `json:"source,omitempty"`
	Provider                  string                `json:"provider,omitempty"`
	ExpectedSourceFingerprint string                `json:"expectedSourceFingerprint,omitempty"`
}

func clonePersonalReportSource(source *PersonalReportSource) *PersonalReportSource {
	if source == nil {
		return nil
	}
	result := *source
	result.Definition = cloneJSON(source.Definition)
	return &result
}

// IsPersonalCopy reports whether the writer or the store verified this report's source.
func (r *SharedArtifact) IsPersonalCopy() bool {
	return r != nil && r.personalSource != nil
}

// VerifyUserReportProvenance checks the projection against provider-owned
// metadata. Definition JSON alone cannot establish an original or a default.
func VerifyUserReportProvenance(row *SharedArtifact, raw json.RawMessage) error {
	source, err := personalReportSource(raw)
	if err != nil {
		return fmt.Errorf("stored personal report provenance phase=read: %w", err)
	}
	if row == nil || !samePersonalReportSource(source, row.personalSource) {
		return fmt.Errorf("stored personal report provenance does not match its provider metadata")
	}
	return nil
}

// PrepareUserReportWrite runs inside the owning provider, before persistence.
// It reuses report-source admission and existing lossless definition transforms;
// ordinary writes preserve server-owned lineage and cannot introduce it.
func PrepareUserReportWrite(ctx context.Context, factory ResourceReaderFactory, provider string, candidate, previous *SharedArtifact, manifest json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if candidate == nil {
		return identity.ErrResource
	}
	supplied, err := personalReportSource(candidate.ResourceDefinition)
	if err != nil {
		return fmt.Errorf("personal report provenance phase=prepare: %w: %w", identity.ErrResource, err)
	}
	if previous != nil {
		if err = VerifyUserReportProvenance(previous, previous.ResourceDefinition); err != nil {
			return err
		}
	}
	if len(manifest) == 0 {
		var source *PersonalReportSource
		if previous != nil {
			source = previous.personalSource
		}
		if supplied != nil && !samePersonalReportSource(supplied, source) {
			return fmt.Errorf("personal report provenance is provider-owned: %w", identity.ErrResource)
		}
		if source != nil {
			candidate.ResourceDefinition, err = personalDefinitionWithSource(candidate.ResourceDefinition, source)
		}
		candidate.personalSource = clonePersonalReportSource(source)
		return err
	}
	var request PersonalReportSourceManifest
	if err := json.Unmarshal(manifest, &request); err != nil {
		return fmt.Errorf("personal report source manifest: %w: %w", identity.ErrResource, err)
	}
	switch request.Operation {
	case "copy":
		if previous != nil || request.Source == nil || request.Provider != "" && request.ExpectedSourceFingerprint == "" {
			return identity.ErrResource
		}
		_, _, err = PrepareUserReportClone(ctx, factory, provider, candidate, primitive.CloneResourceInput{Source: *request.Source, SourceProvider: request.Provider, ExpectedSourceFingerprint: request.ExpectedSourceFingerprint})
		return err
	case "resetRecorded", "resetLatest":
		if previous == nil || previous.personalSource == nil {
			return ErrReportHasNoOriginal
		}
		if request.Source != nil || request.Provider != "" || request.ExpectedSourceFingerprint != "" {
			return identity.ErrResource
		}
		source := clonePersonalReportSource(previous.personalSource)
		original := cloneJSON(source.Definition)
		if request.Operation == "resetLatest" {
			raw, pin, err := readPersonalReportSource(ctx, factory, identity.ResourceRef{URI: source.URI}, source.Provider)
			if err != nil {
				return fmt.Errorf("personal report reset phase=source-read: %w", err)
			}
			original, err = cleanPersonalDefinition(raw)
			if err != nil {
				return err
			}
			original, err = clearPersonalReportSource(original)
			if err != nil {
				return err
			}
			source.Provider, source.URI, source.Revision, source.Fingerprint, source.Definition = pin.ProviderIdentity, pin.URI, pin.Selector(), pin.ContentFingerprint, cloneJSON(original)
		}
		definition, err := personalDefinitionWithSource(original, source)
		if err != nil {
			return err
		}
		definition, err = retainPersonalReportTitle(previous.ResourceDefinition, definition)
		if err != nil {
			return err
		}
		if candidate.Title != previous.Title {
			definition, err = setPersonalReportTitle(definition, candidate.Title)
			if err != nil {
				return err
			}
		}
		definition, err = bindPersonalReportRoot(definition, source.URI, candidate.ArtifactRef)
		if err != nil {
			return err
		}
		projected, err := personalReportSource(definition)
		if err != nil {
			return err
		}
		candidate.ResourceDefinition, candidate.personalSource = definition, projected
	default:
		return fmt.Errorf("unsupported personal report source operation: %w", identity.ErrResource)
	}
	if !validPersonalReportSource(candidate.personalSource) {
		return fmt.Errorf("invalid personal report source: %w", identity.ErrResource)
	}
	return VerifyUserReportProvenance(candidate, candidate.ResourceDefinition)
}

func readPersonalReportSource(ctx context.Context, factory ResourceReaderFactory, ref identity.ResourceRef, sourceProvider string) (json.RawMessage, *identity.ResolvedResource, error) {
	uri, err := identity.ParseResourceURI(ref.URI)
	if err != nil || uri.Kind != "report" {
		return nil, nil, identity.ErrResource
	}
	if factory == nil {
		return nil, nil, fmt.Errorf("personal report source reading: %w", errors.ErrUnsupported)
	}
	reader, err := checkedResourceReader(factory(ctx, "report.retrieve"))
	if err != nil {
		return nil, nil, err
	}
	return readAuthoringReport(ctx, reader, ref, nil, sourceProvider)
}

// PrepareUserReportClone admits the chosen source and derives a new personal
// definition. The admitted input also identifies receipts written by earlier
// hosts which expressed an unchanged duplicate through CreateResource.
func PrepareUserReportClone(ctx context.Context, factory ResourceReaderFactory, provider string, candidate *SharedArtifact, input primitive.CloneResourceInput) (json.RawMessage, *identity.ResolvedResource, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if candidate == nil {
		return nil, nil, identity.ErrResource
	}
	raw, pin, err := readPersonalReportSource(ctx, factory, input.Source, input.SourceProvider)
	if err != nil {
		return nil, nil, fmt.Errorf("personal report clone phase=source-read: %w", err)
	}
	if input.ExpectedSourceFingerprint != "" && pin.ContentFingerprint != input.ExpectedSourceFingerprint {
		return nil, nil, identity.ErrResourceStale
	}
	if err = applyPersonalReportCopy(candidate, provider, raw, pin); err != nil {
		return nil, nil, err
	}
	return raw, pin, nil
}

func applyPersonalReportCopy(candidate *SharedArtifact, provider string, raw json.RawMessage, pin *identity.ResolvedResource) error {
	var err error
	// Only this provider's verified projection can carry inherited personal
	// lineage. A shared publisher's lookalike metadata is never authoritative.
	if pin.ProviderIdentity != provider {
		raw, err = clearPersonalReportSource(raw)
		if err != nil {
			return err
		}
	}
	definition, err := preparePersonalDefinition(raw, candidate.ResourceDefinition, pin)
	if err != nil {
		return err
	}
	if candidate.Title != "" {
		definition, err = setPersonalReportTitle(definition, candidate.Title)
		if err != nil {
			return err
		}
	}
	source, err := personalReportSource(definition)
	if err != nil {
		return err
	}
	definition, err = bindPersonalReportRoot(definition, pin.URI, candidate.ArtifactRef)
	if err != nil {
		return err
	}
	candidate.ResourceDefinition, candidate.personalSource = definition, clonePersonalReportSource(source)
	if !validPersonalReportSource(candidate.personalSource) {
		return fmt.Errorf("invalid personal report source: %w", identity.ErrResource)
	}
	return VerifyUserReportProvenance(candidate, candidate.ResourceDefinition)
}

// Share the persisted-source validity predicate with the owning write path.
func validPersonalReportSource(source *PersonalReportSource) bool {
	return source != nil && isCanonicalReportURI(source.URI) && json.Valid(source.Definition) && strings.TrimSpace(source.Provider) != "" && (identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: source.Fingerprint}).Valid()
}

// UserReportDefinition projects legacy experimental provenance away without
// modifying stored content. Such reports remain editable, but cannot establish
// source-based defaults or reset rights until derived through the owner.
func UserReportDefinition(row *SharedArtifact, raw json.RawMessage) (json.RawMessage, error) {
	if row == nil {
		return nil, ErrNotFound
	}
	if row.personalSource == nil {
		// A legacy public block is not evidence. Remove it before decoding its
		// contents; the existing readback still rejects ambiguous leftovers.
		return projectWithoutPersonalReportSource(raw)
	}
	if err := VerifyUserReportProvenance(row, raw); err != nil {
		return nil, err
	}
	return cloneJSON(raw), nil
}
