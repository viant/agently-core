package sdk

import (
	"mime"
	"net/http"
	"strings"
	"unicode"

	"github.com/google/uuid"
	authctx "github.com/viant/agently-core/internal/auth"
	scratchpad "github.com/viant/agently-core/protocol/tool/service/scratchpad"
)

// Artifact downloads are current-user scoped and never use remote URLs or
// tool-response payload storage. Verification finishes before response headers.
func handleArtifactDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, no-store")
		if strings.TrimSpace(authctx.EffectiveUserID(r.Context())) == "" {
			http.Error(w, "artifact authentication required", http.StatusUnauthorized)
			return
		}
		id := r.PathValue("id")
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			http.Error(w, "artifact unavailable", http.StatusNotFound)
			return
		}
		service := scratchpad.New()
		uri := scratchpad.ArtifactURI(id)
		descriptor, err := service.DescribeArtifact(r.Context(), uri)
		if err != nil {
			http.Error(w, "artifact unavailable", http.StatusNotFound)
			return
		}
		data, err := service.ReadArtifactPayload(r.Context(), uri)
		if err != nil {
			http.Error(w, "artifact unavailable", http.StatusNotFound)
			return
		}
		current, err := service.DescribeArtifact(r.Context(), uri)
		if err != nil || *current != *descriptor {
			http.Error(w, "artifact unavailable", http.StatusNotFound)
			return
		}
		mediaType, _, err := mime.ParseMediaType(descriptor.MimeType)
		if err != nil || mediaType == "" {
			http.Error(w, "artifact unavailable", http.StatusNotFound)
			return
		}
		name := descriptor.Name
		// Existing artifacts may predate filename validation; never install
		// unsafe header values from their manifests.
		if name == "" || name == "." || name == ".." || len(name) > 255 || strings.ContainsAny(name, "/\\") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			name = "artifact"
		}
		w.Header().Set("Content-Type", mediaType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}
