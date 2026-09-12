package reporting

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	afsscratchpad "github.com/viant/afs/scratchpad"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
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
	publisher := scratchpadsvc.New(scratchpadsvc.WithArtifactClient(s.scratchpad, s.scratchpadFS))
	if _, err := publisher.PublishArtifact(ownerCtx, strings.TrimSpace(artifact.ArtifactID), artifactFileName(artifact), strings.TrimSpace(artifact.ContentType), "", bytes.NewReader(artifact.Data)); err != nil {
		return err
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
