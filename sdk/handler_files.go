package sdk

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/viant/agently-core/app/store/conversation"
)

func handleUploadFile(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("parse multipart form: %w", err))
			return
		}
		conversationID := strings.TrimSpace(r.FormValue("conversationId"))
		if conversationID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("missing file field: %w", err))
			return
		}
		defer file.Close()

		data, err := io.ReadAll(file)
		if err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("read file: %w", err))
			return
		}

		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" && header != nil {
			name = strings.TrimSpace(header.Filename)
		}
		contentType := strings.TrimSpace(r.FormValue("contentType"))
		if contentType == "" && header != nil {
			contentType = strings.TrimSpace(header.Header.Get("Content-Type"))
		}

		out, err := client.UploadFile(r.Context(), &UploadFileInput{
			ConversationID: conversationID,
			Name:           name,
			ContentType:    contentType,
			Data:           data,
		})
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		if out == nil {
			httpError(w, http.StatusInternalServerError, fmt.Errorf("upload returned no result"))
			return
		}
		if strings.TrimSpace(out.URI) == "" && strings.TrimSpace(out.ID) != "" {
			out.URI = "/v1/files/" + strings.TrimSpace(out.ID) + "?conversationId=" + conversationID
		}
		httpJSON(w, http.StatusOK, out)
	}
}

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

		body, _ := payloadResponseBody(payload)

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

		httpJSON(w, http.StatusOK, payloadJSONView(payload, metaMode, inlineMode))
	}
}

func handleGetPayloads(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input GetPayloadsInput
		if err := decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		ids := normalizePayloadIDs(input.IDs)
		if len(ids) == 0 {
			httpJSON(w, http.StatusOK, map[string]*conversation.Payload{})
			return
		}
		out, err := client.GetPayloads(r.Context(), ids)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		if out == nil {
			out = map[string]*conversation.Payload{}
		}
		httpJSON(w, http.StatusOK, payloadJSONViewMap(out))
	}
}

func payloadJSONViewMap(payloads map[string]*conversation.Payload) map[string]*conversation.Payload {
	out := make(map[string]*conversation.Payload, len(payloads))
	for id, payload := range payloads {
		if payload == nil {
			continue
		}
		out[id] = payloadJSONView(payload, false, true)
	}
	return out
}

func payloadJSONView(payload *conversation.Payload, metaMode, inlineMode bool) *conversation.Payload {
	if payload == nil {
		return nil
	}
	body, compression := payloadResponseBody(payload)
	out := *payload
	out.Compression = compression
	if metaMode || !inlineMode {
		out.InlineBody = nil
		return &out
	}
	copied := append([]byte(nil), body...)
	out.InlineBody = &copied
	return &out
}

func payloadResponseBody(payload *conversation.Payload) ([]byte, string) {
	body := payloadBytes(payload)
	compression := strings.TrimSpace(payload.Compression)
	if strings.EqualFold(compression, "gzip") && len(body) > 0 {
		if inflated, ok := inflateGZIP(body); ok {
			return inflated, ""
		}
	}
	return body, compression
}
