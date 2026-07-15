package write

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	forgereport "github.com/viant/agently-core/pkg/forge/reporting"
	"github.com/viant/xdatly/handler"
	"github.com/viant/xdatly/handler/response"
)

type Handler struct{}

func (h *Handler) Exec(ctx context.Context, sess handler.Session) (interface{}, error) {
	out := &Output{}
	out.Status.Status = "ok"
	if err := h.exec(ctx, sess, out); err != nil {
		var respErr *response.Error
		if errors.As(err, &respErr) {
			return out, err
		}
		out.setError(err)
	}
	return out, nil
}

func (h *Handler) exec(ctx context.Context, sess handler.Session, out *Output) error {
	in := &Input{}
	if err := in.Init(ctx, sess, out); err != nil {
		return err
	}
	if in.Artifact == nil {
		return response.NewError(http.StatusBadRequest, "shared artifact is required")
	}
	if strings.TrimSpace(in.Artifact.ArtifactID) == "" {
		return response.NewError(http.StatusBadRequest, "shared artifact artifactId is required")
	}
	if strings.TrimSpace(in.Artifact.OwnerID) == "" {
		return response.NewError(http.StatusBadRequest, "shared artifact ownerId is required")
	}
	sqlSvc, err := sess.Db()
	if err != nil {
		return err
	}
	db, err := sqlSvc.Db(ctx)
	if err != nil {
		return err
	}
	var existingID string
	err = db.QueryRowContext(ctx, `SELECT artifact_id FROM report_shared_artifact WHERE artifact_id = ? LIMIT 1`, strings.TrimSpace(in.Artifact.ArtifactID)).Scan(&existingID)
	switch {
	case err == nil:
		if err = sqlSvc.Update("report_shared_artifact", in.Artifact); err != nil {
			return err
		}
	case errors.Is(err, sql.ErrNoRows):
		if err = sqlSvc.Insert("report_shared_artifact", in.Artifact); err != nil {
			return err
		}
	default:
		return fmt.Errorf("forge reporting shared artifact write lookup failed: %w", err)
	}
	out.Data = cloneSharedArtifact(in.Artifact)
	return nil
}

func cloneSharedArtifact(input *forgereport.SharedArtifact) *forgereport.SharedArtifact {
	if input == nil {
		return nil
	}
	out := *input
	if len(input.Document) > 0 {
		out.Document = append([]byte{}, input.Document...)
	}
	if len(input.ReportSpec) > 0 {
		out.ReportSpec = append([]byte{}, input.ReportSpec...)
	}
	if len(input.CompileState) > 0 {
		out.CompileState = append([]byte{}, input.CompileState...)
	}
	if len(input.ReportFill) > 0 {
		out.ReportFill = append([]byte{}, input.ReportFill...)
	}
	if len(input.ReportPrint) > 0 {
		out.ReportPrint = append([]byte{}, input.ReportPrint...)
	}
	if len(input.SavedViewOverlay) > 0 {
		out.SavedViewOverlay = append([]byte{}, input.SavedViewOverlay...)
	}
	if len(input.Metadata) > 0 {
		out.Metadata = append([]byte{}, input.Metadata...)
	}
	return &out
}
