package sdk

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	stagedUploadRootName = "agently-staging-uploads"
	stagedUploadTTL      = 24 * time.Hour
)

func stagedUploadRoot() string {
	return filepath.Join(os.TempDir(), stagedUploadRootName)
}

func cleanupStagedUploads(root string, ttl time.Duration) error {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." || root == string(filepath.Separator) {
		return fmt.Errorf("invalid staged upload root")
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-ttl)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func handleStagedUpload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("parse multipart form: %w", err))
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("missing file field: %w", err))
			return
		}
		defer file.Close()

		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" && header != nil {
			name = strings.TrimSpace(header.Filename)
		}
		name = filepath.Base(name)
		if name == "." || name == string(filepath.Separator) || name == "" {
			name = "upload.bin"
		}

		contentType := strings.TrimSpace(r.FormValue("contentType"))
		if contentType == "" && header != nil {
			contentType = strings.TrimSpace(header.Header.Get("Content-Type"))
		}

		stagingID := uuid.NewString()
		targetDir := filepath.Join(stagedUploadRoot(), stagingID)
		if err := os.MkdirAll(targetDir, 0o700); err != nil {
			httpError(w, http.StatusInternalServerError, fmt.Errorf("create staging dir: %w", err))
			return
		}
		targetPath := filepath.Join(targetDir, name)
		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			httpError(w, http.StatusInternalServerError, fmt.Errorf("create staged file: %w", err))
			return
		}
		size, copyErr := io.Copy(out, file)
		closeErr := out.Close()
		if copyErr != nil {
			_ = os.RemoveAll(targetDir)
			httpError(w, http.StatusInternalServerError, fmt.Errorf("write staged file: %w", copyErr))
			return
		}
		if closeErr != nil {
			_ = os.RemoveAll(targetDir)
			httpError(w, http.StatusInternalServerError, fmt.Errorf("close staged file: %w", closeErr))
			return
		}

		httpJSON(w, http.StatusOK, &UploadFileOutput{
			URI:      filepath.ToSlash(filepath.Join(stagedUploadRootName, stagingID, name)),
			Name:     name,
			Size:     size,
			MimeType: contentType,
		})
	}
}
