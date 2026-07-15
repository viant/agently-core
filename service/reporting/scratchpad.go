package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	afsscratchpad "github.com/viant/afs/scratchpad"
	afsurl "github.com/viant/afs/url"
)

func scratchpadArtifactURL(artifactID string) string {
	normalizedArtifactID := strings.Trim(strings.TrimSpace(artifactID), "/")
	if normalizedArtifactID == "" {
		return ""
	}
	return afsscratchpad.Scheme + "://artifact/" + normalizedArtifactID
}

func artifactFileName(artifact *Artifact) string {
	if artifact == nil {
		return "artifact.bin"
	}
	ext := "bin"
	switch artifact.Format {
	case ExportFormatPDF:
		ext = "pdf"
	case ExportFormatCSV:
		ext = "csv"
	case ExportFormatXLSX:
		ext = "xlsx"
	}
	return strings.TrimSpace(artifact.ArtifactID) + "." + ext
}

func (s *Service) artifactScratchpadContext(ctx context.Context, ownerID string) context.Context {
	if strings.TrimSpace(ownerID) == "" {
		return ctx
	}
	return afsscratchpad.ContextWithUserID(ctx, ownerID)
}

func (s *Service) publishArtifactToScratchpad(ctx context.Context, artifact *Artifact) error {
	if s == nil || s.scratchpad == nil || artifact == nil {
		return nil
	}
	if strings.TrimSpace(artifact.ArtifactID) == "" {
		return fmt.Errorf("reporting scratchpad publish: artifactId is required")
	}
	if len(artifact.Data) == 0 {
		return fmt.Errorf("reporting scratchpad publish: artifact data is required")
	}
	ownerCtx := s.artifactScratchpadContext(ctx, strings.TrimSpace(artifact.OwnerID))
	root, _, err := s.scratchpad.ResolveRootURIContext(ownerCtx)
	if err != nil {
		return err
	}
	artifactsRoot := afsurl.Join(root, "artifacts")
	if err := s.scratchpadFS.Create(ownerCtx, artifactsRoot, 0o755, true); err != nil {
		return fmt.Errorf("reporting scratchpad publish: create artifacts root failed: %w", err)
	}
	sourceURL := afsurl.Join(artifactsRoot, artifactFileName(artifact))
	if err := s.scratchpadFS.Upload(ownerCtx, sourceURL, 0o644, bytes.NewReader(artifact.Data)); err != nil {
		return fmt.Errorf("reporting scratchpad publish: upload artifact bytes failed: %w", err)
	}
	metadata, err := json.Marshal(afsscratchpad.Artifact{
		Kind:        "artifact",
		ArtifactID:  strings.TrimSpace(artifact.ArtifactID),
		Name:        artifactFileName(artifact),
		ContentType: strings.TrimSpace(artifact.ContentType),
		SourceURL:   sourceURL,
	})
	if err != nil {
		return fmt.Errorf("reporting scratchpad publish: encode artifact metadata failed: %w", err)
	}
	if _, err = s.scratchpad.Memorize(ownerCtx, &afsscratchpad.MemorizeInput{
		Key:         afsscratchpad.ArtifactKey(strings.TrimSpace(artifact.ArtifactID)),
		Description: "Reporting export artifact " + strings.TrimSpace(artifact.ArtifactID),
		Body:        string(metadata),
	}); err != nil {
		return fmt.Errorf("reporting scratchpad publish: persist artifact metadata failed: %w", err)
	}
	artifact.SourceURL = scratchpadArtifactURL(artifact.ArtifactID)
	return nil
}

func (s *Service) hydrateArtifactFromScratchpad(ctx context.Context, artifact *Artifact) error {
	if s == nil || s.scratchpad == nil || artifact == nil {
		return nil
	}
	if strings.TrimSpace(artifact.ArtifactID) == "" {
		return nil
	}
	ownerCtx := s.artifactScratchpadContext(ctx, strings.TrimSpace(artifact.OwnerID))
	meta, reader, err := s.scratchpad.OpenArtifact(ownerCtx, strings.TrimSpace(artifact.ArtifactID))
	if err != nil {
		return err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("reporting scratchpad hydrate: read artifact bytes failed: %w", err)
	}
	artifact.Data = append([]byte{}, data...)
	if strings.TrimSpace(artifact.ContentType) == "" && meta != nil {
		artifact.ContentType = strings.TrimSpace(meta.ContentType)
	}
	artifact.SourceURL = scratchpadArtifactURL(artifact.ArtifactID)
	return nil
}

func (s *Service) enrichArtifactWithScratchpad(ctx context.Context, artifact *Artifact) (*Artifact, error) {
	if artifact == nil {
		return nil, nil
	}
	next := cloneArtifact(artifact)
	if next == nil {
		return nil, nil
	}
	if s == nil || s.scratchpad == nil {
		return next, nil
	}
	next.SourceURL = scratchpadArtifactURL(next.ArtifactID)
	if len(next.Data) > 0 {
		if err := s.publishArtifactToScratchpad(ctx, next); err != nil {
			return nil, err
		}
		return next, nil
	}
	if err := s.hydrateArtifactFromScratchpad(ctx, next); err != nil {
		return nil, err
	}
	return next, nil
}
