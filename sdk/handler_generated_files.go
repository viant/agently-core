package sdk

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	gfread "github.com/viant/agently-core/pkg/agently/generatedfile/read"
)

type generatedFileLister interface {
	ListGeneratedFiles(ctx context.Context, conversationID string) ([]*gfread.GeneratedFileView, error)
}

type generatedFileDownloader interface {
	DownloadGeneratedFile(ctx context.Context, id string) ([]byte, string, string, error)
}

func handleListGeneratedFiles(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID := strings.TrimSpace(r.PathValue("id"))
		if conversationID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		lister, ok := client.(generatedFileLister)
		if !ok {
			httpError(w, http.StatusNotImplemented, fmt.Errorf("generated file listing is unavailable for this client mode"))
			return
		}
		files, err := lister.ListGeneratedFiles(r.Context(), conversationID)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, files)
	}
}

func handleDownloadGeneratedFile(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("generated file ID is required"))
			return
		}
		downloader, ok := client.(generatedFileDownloader)
		if !ok {
			httpError(w, http.StatusNotImplemented, fmt.Errorf("generated file download is unavailable for this client mode"))
			return
		}
		body, contentType, filename, err := downloader.DownloadGeneratedFile(r.Context(), id)
		if err != nil {
			switch {
			case errors.Is(err, errGeneratedFileNotFound):
				httpError(w, http.StatusNotFound, err)
			case errors.Is(err, errGeneratedFileNoContent):
				w.WriteHeader(http.StatusNoContent)
			default:
				httpError(w, http.StatusBadGateway, err)
			}
			return
		}
		if strings.TrimSpace(contentType) == "" {
			contentType = "application/octet-stream"
		}
		if strings.TrimSpace(filename) == "" {
			filename = "generated-file.bin"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}
