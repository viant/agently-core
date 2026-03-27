package sdk

import (
	"fmt"
	"net/http"
	"strings"
)

func handleListFiles(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
		if conversationID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		out, err := client.ListFiles(r.Context(), &ListFilesInput{ConversationID: conversationID})
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleDownloadFile(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
		fileID := strings.TrimSpace(r.PathValue("id"))
		if conversationID == "" || fileID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID and file ID are required"))
			return
		}
		out, err := client.DownloadFile(r.Context(), &DownloadFileInput{
			ConversationID: conversationID,
			FileID:         fileID,
		})
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		if out == nil {
			httpError(w, http.StatusNotFound, fmt.Errorf("file not found"))
			return
		}
		if queryBool(r, "raw", false) {
			contentType := strings.TrimSpace(out.ContentType)
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			w.Header().Set("Content-Type", contentType)
			if name := strings.TrimSpace(out.Name); name != "" {
				w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(out.Data)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleGetPayload(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("payload ID is required"))
			return
		}
		reader, ok := client.(payloadReader)
		if !ok {
			httpError(w, http.StatusNotImplemented, fmt.Errorf("payload endpoint is unavailable for this client mode"))
			return
		}
		payload, err := reader.GetPayload(r.Context(), id)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		if payload == nil {
			httpError(w, http.StatusNotFound, fmt.Errorf("payload not found"))
			return
		}

		rawMode := queryBool(r, "raw", false)
		metaMode := queryBool(r, "meta", false)
		inlineMode := queryBool(r, "inline", true)

		body := payloadBytes(payload)
		compression := strings.TrimSpace(payload.Compression)
		if strings.EqualFold(compression, "gzip") && len(body) > 0 {
			if inflated, ok := inflateGZIP(body); ok {
				body = inflated
				compression = ""
			}
		}

		if rawMode {
			if len(body) == 0 {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			contentType := strings.TrimSpace(payload.MimeType)
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			w.Header().Set("Content-Type", contentType)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
			return
		}

		out := *payload
		out.Compression = compression
		if metaMode || !inlineMode {
			out.InlineBody = nil
		} else {
			copied := append([]byte(nil), body...)
			out.InlineBody = &copied
		}
		httpJSON(w, http.StatusOK, out)
	}
}
