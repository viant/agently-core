package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/viant/scy"
	"github.com/viant/scy/auth/jwt/signer"
)

func (a *authExtension) handleJWTKeyPair() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Bits           int    `json:"bits"`
			PrivateKeyPath string `json:"privateKeyPath,omitempty"`
			PublicKeyPath  string `json:"publicKeyPath,omitempty"`
			Overwrite      bool   `json:"overwrite,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		if in.Bits <= 0 {
			in.Bits = 2048
		}
		key, err := rsa.GenerateKey(rand.Reader, in.Bits)
		if err != nil {
			runtimeError(w, http.StatusInternalServerError, fmt.Errorf("unable to generate rsa key: %w", err))
			return
		}
		privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
		publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			runtimeError(w, http.StatusInternalServerError, fmt.Errorf("unable to encode public key: %w", err))
			return
		}
		publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
		if err := writePEMFiles(in.PrivateKeyPath, in.PublicKeyPath, privatePEM, publicPEM, in.Overwrite); err != nil {
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		runtimeJSON(w, http.StatusOK, map[string]any{"privateKey": string(privatePEM), "publicKey": string(publicPEM), "privateKeyPath": strings.TrimSpace(in.PrivateKeyPath), "publicKeyPath": strings.TrimSpace(in.PublicKeyPath), "bits": in.Bits})
	}
}

func (a *authExtension) handleJWTMint() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			PrivateKeyPath string                 `json:"privateKeyPath,omitempty"`
			Subject        string                 `json:"subject,omitempty"`
			Email          string                 `json:"email,omitempty"`
			Username       string                 `json:"username,omitempty"`
			Name           string                 `json:"name,omitempty"`
			TTLSeconds     int                    `json:"ttlSeconds,omitempty"`
			Claims         map[string]interface{} `json:"claims,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		ttl := time.Hour
		if in.TTLSeconds > 0 {
			ttl = time.Duration(in.TTLSeconds) * time.Second
		}
		claims := map[string]interface{}{}
		for k, v := range in.Claims {
			claims[k] = v
		}
		if strings.TrimSpace(in.Subject) != "" {
			claims["sub"] = strings.TrimSpace(in.Subject)
		}
		if strings.TrimSpace(in.Email) != "" {
			claims["email"] = strings.TrimSpace(in.Email)
		}
		if strings.TrimSpace(in.Username) != "" {
			claims["username"] = strings.TrimSpace(in.Username)
		}
		if strings.TrimSpace(in.Name) != "" {
			claims["name"] = strings.TrimSpace(in.Name)
		}
		var (
			token string
			err   error
		)
		privatePath := strings.TrimSpace(in.PrivateKeyPath)
		if privatePath != "" {
			token, err = signWithPrivateKey(r.Context(), privatePath, ttl, claims)
		} else if a.jwtSignKey != "" {
			token, err = signWithPrivateKey(r.Context(), a.jwtSignKey, ttl, claims)
		} else {
			err = fmt.Errorf("jwt signer not configured; set auth.jwt.rsaPrivateKey or provide privateKeyPath")
		}
		if err != nil {
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		runtimeJSON(w, http.StatusOK, map[string]any{"token": token, "tokenType": "Bearer", "expiresAt": time.Now().Add(ttl).UTC().Format(time.RFC3339), "ttlSeconds": int(ttl.Seconds())})
	}
}

func signWithPrivateKey(ctx context.Context, privateKeyPath string, ttl time.Duration, claims map[string]interface{}) (string, error) {
	cfg := &signer.Config{RSA: scy.NewResource("", privateKeyPath, "")}
	s := signer.New(cfg)
	if err := s.Init(ctx); err != nil {
		return "", fmt.Errorf("unable to init jwt signer: %w", err)
	}
	token, err := s.Create(ttl, claims)
	if err != nil {
		return "", fmt.Errorf("unable to sign jwt: %w", err)
	}
	return token, nil
}

func writePEMFiles(privatePath, publicPath string, privatePEM, publicPEM []byte, overwrite bool) error {
	privatePath = strings.TrimSpace(privatePath)
	publicPath = strings.TrimSpace(publicPath)
	if privatePath == "" && publicPath == "" {
		return nil
	}
	writeFile := func(path string, mode os.FileMode, data []byte) error {
		if path == "" {
			return nil
		}
		if !overwrite {
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("file already exists: %s", path)
			}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, data, mode)
	}
	if err := writeFile(privatePath, 0o600, privatePEM); err != nil {
		return err
	}
	return writeFile(publicPath, 0o644, publicPEM)
}

func wantsJSON(r *http.Request) bool {
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("format")), "json") {
		return true
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json")
}
