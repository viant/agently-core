package sdk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	convstore "github.com/viant/agently-core/app/store/conversation"
	gfread "github.com/viant/agently-core/pkg/agently/generatedfile/read"
)

var (
	errGeneratedFileNotFound  = errors.New("generated file not found")
	errGeneratedFileNoContent = errors.New("generated file has no content")
)

func (c *EmbeddedClient) ListGeneratedFiles(ctx context.Context, conversationID string) ([]*gfread.GeneratedFileView, error) {
	convID := strings.TrimSpace(conversationID)
	if convID == "" {
		return nil, errors.New("conversation ID is required")
	}
	if store, ok := c.conv.(convstore.GeneratedFileClient); ok {
		in := &gfread.Input{
			ConversationID: convID,
			Has:            &gfread.Has{ConversationID: true},
		}
		return store.GetGeneratedFiles(ctx, in)
	}
	if c.data != nil {
		return c.data.ListGeneratedFiles(ctx, convID)
	}
	return nil, errors.New("generated file storage is not configured")
}

func (c *EmbeddedClient) DownloadGeneratedFile(ctx context.Context, id string) ([]byte, string, string, error) {
	store, ok := c.conv.(convstore.GeneratedFileClient)
	if !ok {
		return nil, "", "", errGeneratedFileNotFound
	}
	in := &gfread.Input{
		ID:  strings.TrimSpace(id),
		Has: &gfread.Has{ID: true},
	}
	files, err := store.GetGeneratedFiles(ctx, in)
	if err != nil || len(files) == 0 || files[0] == nil {
		return nil, "", "", errGeneratedFileNotFound
	}
	file := files[0]

	filename := strings.TrimSpace(ptrString(file.Filename))
	if filename == "" {
		filename = strings.TrimSpace(ptrString(file.ProviderFileID))
		if filename == "" {
			filename = "generated-file.bin"
		}
	}
	contentType := strings.TrimSpace(ptrString(file.MimeType))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	if payloadID := strings.TrimSpace(ptrString(file.PayloadID)); payloadID != "" {
		payload, pErr := c.conv.GetPayload(ctx, payloadID)
		if pErr != nil || payload == nil || payload.InlineBody == nil || len(*payload.InlineBody) == 0 {
			return nil, "", "", errGeneratedFileNoContent
		}
		if strings.TrimSpace(payload.MimeType) != "" {
			contentType = strings.TrimSpace(payload.MimeType)
		}
		return *payload.InlineBody, contentType, filename, nil
	}

	if !strings.EqualFold(strings.TrimSpace(file.Provider), "openai") || !strings.EqualFold(strings.TrimSpace(file.Mode), "interpreter") {
		return nil, "", "", errGeneratedFileNoContent
	}
	containerID := strings.TrimSpace(ptrString(file.ContainerID))
	providerFileID := strings.TrimSpace(ptrString(file.ProviderFileID))
	if containerID == "" || providerFileID == "" {
		return nil, "", "", errGeneratedFileNoContent
	}

	body, downloadedType, dErr := downloadOpenAIContainerFileContent(ctx, containerID, providerFileID)
	if dErr != nil {
		status := "failed"
		msg := strings.ToLower(strings.TrimSpace(dErr.Error()))
		if strings.Contains(msg, "status=404") {
			status = "expired"
		}
		_ = c.patchGeneratedFileDownloadState(ctx, strings.TrimSpace(file.ID), status, dErr.Error(), "", "", 0, "")
		return nil, "", "", dErr
	}
	if strings.TrimSpace(downloadedType) != "" {
		contentType = strings.TrimSpace(downloadedType)
	}
	if strings.EqualFold(strings.TrimSpace(file.CopyMode), "lazy_cache") || strings.EqualFold(strings.TrimSpace(file.CopyMode), "eager") {
		payloadID, pErr := c.persistGeneratedFilePayload(ctx, body, contentType)
		if pErr != nil {
			_ = c.patchGeneratedFileDownloadState(ctx, strings.TrimSpace(file.ID), "failed", pErr.Error(), "", contentType, len(body), "")
		} else {
			_ = c.patchGeneratedFileDownloadState(ctx, strings.TrimSpace(file.ID), "ready", "", payloadID, contentType, len(body), sha256Hex(body))
		}
	}
	return body, contentType, filename, nil
}

func (c *EmbeddedClient) persistGeneratedFilePayload(ctx context.Context, body []byte, contentType string) (string, error) {
	if len(body) == 0 {
		return "", errGeneratedFileNoContent
	}
	payload := convstore.NewPayload()
	payloadID := uuid.NewString()
	payload.SetId(payloadID)
	payload.SetKind("model_response")
	payload.SetMimeType(strings.TrimSpace(contentType))
	if strings.TrimSpace(payload.MimeType) == "" {
		payload.SetMimeType("application/octet-stream")
	}
	payload.SetSizeBytes(len(body))
	payload.SetStorage("inline")
	payload.SetInlineBody(body)
	if err := c.conv.PatchPayload(ctx, payload); err != nil {
		return "", err
	}
	return payloadID, nil
}

func (c *EmbeddedClient) patchGeneratedFileDownloadState(ctx context.Context, generatedFileID, status, errorMsg, payloadID, mimeType string, sizeBytes int, checksum string) error {
	store, ok := c.conv.(convstore.GeneratedFileClient)
	if !ok {
		return nil
	}
	upd := convstore.NewGeneratedFile()
	upd.SetID(strings.TrimSpace(generatedFileID))
	if strings.TrimSpace(status) != "" {
		upd.SetStatus(strings.TrimSpace(status))
	}
	if strings.TrimSpace(errorMsg) != "" || strings.EqualFold(strings.TrimSpace(status), "ready") {
		upd.SetErrorMessage(strings.TrimSpace(errorMsg))
	}
	if strings.TrimSpace(payloadID) != "" {
		upd.SetPayloadID(strings.TrimSpace(payloadID))
	}
	if strings.TrimSpace(mimeType) != "" {
		upd.SetMimeType(strings.TrimSpace(mimeType))
	}
	if sizeBytes > 0 {
		upd.SetSizeBytes(sizeBytes)
	}
	if strings.TrimSpace(checksum) != "" {
		upd.SetChecksum(strings.TrimSpace(checksum))
	}
	return store.PatchGeneratedFile(ctx, upd)
}

func downloadOpenAIContainerFileContent(ctx context.Context, containerID, fileID string) ([]byte, string, error) {
	containerID = strings.TrimSpace(containerID)
	fileID = strings.TrimSpace(fileID)
	if containerID == "" || fileID == "" {
		return nil, "", fmt.Errorf("container_id and file_id are required")
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return nil, "", fmt.Errorf("OPENAI_API_KEY is not configured")
	}
	base := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	base = strings.TrimRight(base, "/")
	if !strings.HasSuffix(strings.ToLower(base), "/v1") {
		base += "/v1"
	}
	endpoint := fmt.Sprintf("%s/containers/%s/files/%s/content", base, neturl.PathEscape(containerID), neturl.PathEscape(fileID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("openai download failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return body, strings.TrimSpace(resp.Header.Get("Content-Type")), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func ptrString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
