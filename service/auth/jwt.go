package auth

import (
	"context"
	"crypto/rsa"
	"fmt"
	"sync"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/scy"
	"github.com/viant/scy/auth/jwt/signer"
	"github.com/viant/scy/auth/jwt/verifier"
)

// JWTService wraps scy's JWT signer and verifier for the agently-core auth layer.
type JWTService struct {
	verifier *verifier.Service
	signer   *signer.Service
	mu       sync.Mutex
	inited   bool
	cfg      *iauth.JWT
}

// NewJWTService creates a JWT service from the given config.
// Call Init() before use to load keys.
func NewJWTService(cfg *iauth.JWT) *JWTService {
	return &JWTService{cfg: cfg}
}

// Init loads keys from scy resources. Safe to call multiple times.
func (j *JWTService) Init(ctx context.Context) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.inited {
		return nil
	}

	// Build verifier config.
	vCfg := &verifier.Config{
		CertURL: j.cfg.CertURL,
	}
	for _, rsaURL := range j.cfg.RSA {
		vCfg.RSA = append(vCfg.RSA, scy.NewResource("", rsaURL, ""))
	}
	if j.cfg.HMAC != "" {
		vCfg.HMAC = scy.NewResource("", j.cfg.HMAC, "")
	}
	j.verifier = verifier.New(vCfg)
	if err := j.verifier.Init(ctx); err != nil {
		return fmt.Errorf("jwt verifier init: %w", err)
	}

	// Build signer config (optional — only when private key is provided).
	if j.cfg.RSAPrivateKey != "" {
		sCfg := &signer.Config{
			RSA: scy.NewResource("", j.cfg.RSAPrivateKey, ""),
		}
		j.signer = signer.New(sCfg)
		if err := j.signer.Init(ctx); err != nil {
			return fmt.Errorf("jwt signer init: %w", err)
		}
	} else if j.cfg.HMAC != "" {
		sCfg := &signer.Config{
			HMAC: scy.NewResource("", j.cfg.HMAC, ""),
		}
		j.signer = signer.New(sCfg)
		if err := j.signer.Init(ctx); err != nil {
			return fmt.Errorf("jwt signer init: %w", err)
		}
	}

	j.inited = true
	return nil
}

// Verify validates a JWT token string and returns the parsed claims.
// Returns an error if the token is invalid, expired, or signature verification fails.
func (j *JWTService) Verify(ctx context.Context, tokenString string) (*iauth.UserInfo, error) {
	if j.verifier == nil {
		return nil, fmt.Errorf("jwt verifier not initialized")
	}
	claims, err := j.verifier.VerifyClaims(ctx, tokenString)
	if err != nil {
		return nil, fmt.Errorf("jwt verify: %w", err)
	}
	ui := &iauth.UserInfo{}
	if claims.Subject != "" {
		ui.Subject = claims.Subject
	}
	if claims.Email != "" {
		ui.Email = claims.Email
	}
	// Fallback: use UserID or Username from custom claims.
	if ui.Subject == "" {
		if claims.UserID > 0 {
			ui.Subject = fmt.Sprintf("%d", claims.UserID)
		} else if claims.Username != "" {
			ui.Subject = claims.Username
		}
	}
	return ui, nil
}

// Sign creates a signed JWT token with the given claims and TTL.
// Returns an error if no signer is configured.
func (j *JWTService) Sign(ttl time.Duration, claims interface{}) (string, error) {
	if j.signer == nil {
		return "", fmt.Errorf("jwt signer not configured (no private key)")
	}
	return j.signer.Create(ttl, claims)
}

// PublicKeys returns the loaded RSA public keys (for diagnostics/testing).
func (j *JWTService) PublicKeys() (map[string]*rsa.PublicKey, error) {
	if j.verifier == nil {
		return nil, fmt.Errorf("jwt verifier not initialized")
	}
	return j.verifier.PublicKeys()
}
