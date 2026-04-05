package sdk

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	convstore "github.com/viant/agently-core/app/store/conversation"
	iauth "github.com/viant/agently-core/internal/auth"
	agentsvc "github.com/viant/agently-core/service/agent"
	svcauth "github.com/viant/agently-core/service/auth"
)

type payloadReader interface {
	GetPayload(ctx context.Context, id string) (*convstore.Payload, error)
}

func handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		httpJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func handleQuery(client Client, authCfg *svcauth.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input agentsvc.QueryInput
		if err := decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		input.UserId = resolveQueryUserID(w, r, input.UserId, authCfg)
		if input.UserId == "" {
			httpError(w, http.StatusUnauthorized, fmt.Errorf("authorization required"))
			return
		}
		out, err := client.Query(context.WithoutCancel(r.Context()), &input)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

const anonymousUserCookieName = "agently_anonymous_user"

func resolveQueryUserID(w http.ResponseWriter, r *http.Request, explicit string, authCfg *svcauth.Config) string {
	userID := strings.TrimSpace(explicit)
	if userID != "" {
		return userID
	}
	if derived := strings.TrimSpace(iauth.EffectiveUserID(r.Context())); derived != "" {
		return derived
	}
	if authCfg != nil && authCfg.Enabled {
		return ""
	}
	if cookie, err := r.Cookie(anonymousUserCookieName); err == nil {
		if existing := strings.TrimSpace(cookie.Value); existing != "" {
			return existing
		}
	}
	anonymousID := "anonymous:" + uuid.NewString()
	http.SetCookie(w, &http.Cookie{
		Name:     anonymousUserCookieName,
		Value:    anonymousID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((30 * 24 * time.Hour).Seconds()),
	})
	return anonymousID
}

func payloadBytes(p *convstore.Payload) []byte {
	if p == nil || p.InlineBody == nil {
		return nil
	}
	return append([]byte(nil), (*p.InlineBody)...)
}

func inflateGZIP(data []byte) ([]byte, bool) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	defer reader.Close()
	var out bytes.Buffer
	if _, err = io.Copy(&out, reader); err != nil {
		return nil, false
	}
	return out.Bytes(), true
}

func queryBool(r *http.Request, key string, fallback bool) bool {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func decodeJSON(r *http.Request, v interface{}) error {
	if r.Body == nil {
		return fmt.Errorf("request body is empty")
	}
	return json.NewDecoder(r.Body).Decode(v)
}

func httpJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func WaitForReady(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/v1/conversations")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("server not ready after %v", timeout)
}
