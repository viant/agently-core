package auth

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	authcfg "github.com/viant/mcp/client/auth/config"
	"golang.org/x/oauth2"
)

// Only call after the pinned, server-side token exchange; never for token imports.
func verifyTokenResponseIdentity(ctx context.Context, resolved *resolvedProvider, token *oauth2.Token, nonce, clientID string, now time.Time, verify mcpJWTVerifierFunc) (*verifiedGrant, error) {
	if token == nil || resolved == nil || resolved.provider == nil || strings.TrimSpace(clientID) == "" || verify == nil {
		return nil, fmt.Errorf("OIDC token-response identity is not configured")
	}
	raw, _ := token.Extra("id_token").(string)
	if err := checkJWTAlgorithmAllowlist(raw); err != nil {
		return nil, fmt.Errorf("OIDC identity requires a signed ID token")
	}
	claims, err := verify(ctx, resolved, raw)
	if err != nil {
		return nil, fmt.Errorf("OIDC identity signature rejected")
	}
	issuer := authcfg.NormalizeIssuer(resolved.provider.Issuer)
	if err := validateIDTokenClaims(claims, issuer, nonce, now, clockSkew(resolved.client), clientID); err != nil {
		return nil, fmt.Errorf("OIDC identity rejected: %w", err)
	}
	if nonce != "" && claimString(claims, "nonce") != nonce {
		return nil, fmt.Errorf("OIDC identity nonce is missing or mismatched")
	}
	if notBefore, ok := claimUnixTime(claims, "nbf"); ok && notBefore.Add(-clockSkew(resolved.client)).After(now) {
		return nil, fmt.Errorf("OIDC identity is not yet valid")
	}
	party := claimString(claims, "azp")
	if (party != "" && party != clientID) || (len(claimAudiences(claims)) > 1 && party != clientID) {
		return nil, fmt.Errorf("OIDC identity authorized party mismatch")
	}
	subject := claimString(claims, "sub")
	if subject == "" {
		return nil, fmt.Errorf("OIDC identity subject missing")
	}
	if hash, present := claims["at_hash"]; present {
		value, ok := hash.(string)
		if !ok || value == "" || !accessTokenHashMatches(raw, token.AccessToken, value) {
			return nil, fmt.Errorf("OIDC identity access-token hash mismatch")
		}
	}
	return &verifiedGrant{subject: subject, issuer: issuer}, nil
}

func accessTokenHashMatches(verifiedIDToken, accessToken, expected string) bool {
	header, err := decodeJWTSegment(strings.Split(verifiedIDToken, ".")[0])
	if err != nil {
		return false
	}
	var digest []byte
	switch header["alg"] {
	case "RS256", "PS256", "ES256":
		sum := sha256.Sum256([]byte(accessToken))
		digest = sum[:]
	case "RS384", "PS384", "ES384":
		sum := sha512.Sum384([]byte(accessToken))
		digest = sum[:]
	case "RS512", "PS512", "ES512":
		sum := sha512.Sum512([]byte(accessToken))
		digest = sum[:]
	default:
		return false
	}
	actual := base64.RawURLEncoding.EncodeToString(digest[:len(digest)/2])
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}
