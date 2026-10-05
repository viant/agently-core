package auth

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	authcfg "github.com/viant/mcp/client/auth/config"
	authmeta "github.com/viant/scy/auth/metadata"
)

type verifiedSessionImport struct {
	Subject, Email, Username, Provider string
	Scopes                             []string
	Expiry                             time.Time
}

// verifyRawSessionImport applies only to caller-supplied token material. OAuth
// callbacks and server-owned exchanges keep their separately validated flows.
func verifyRawSessionImport(ctx context.Context, cfg *Config, idToken, accessToken, bearer string) (*verifiedSessionImport, error) {
	idToken, accessToken, bearer = strings.TrimSpace(idToken), strings.TrimSpace(accessToken), strings.TrimSpace(bearer)
	local := requiresJWTSessionVerification(cfg)
	// A mixed workspace may explicitly exchange a token issued by its configured
	// local JWT authority. Do not mix that credential with another issuer's body.
	mixedLocal := cfg != nil && cfg.JWT != nil && cfg.JWT.Enabled && bearer != "" && (idToken == "" || idToken == bearer) && (accessToken == "" || accessToken == bearer)
	if local || mixedLocal {
		only := *cfg
		only.OAuth = nil
		identity, err := verifyJWTSessionImport(ctx, &only, idToken, accessToken, bearer)
		if err == nil && identity != nil {
			return &verifiedSessionImport{Subject: identity.Subject, Email: identity.Email, Username: identity.Subject, Provider: "jwt", Scopes: tokenScopesFromStrings(idToken, accessToken, bearer), Expiry: resolveTokenExpiry("", firstNonEmpty(idToken, bearer), firstNonEmpty(accessToken, bearer))}, nil
		}
		if local {
			return nil, err
		}
	}
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	} // Explicitly auth-disabled development mode.
	if cfg.OAuth == nil || cfg.OAuth.Client == nil {
		return nil, fmt.Errorf("session import has no configured token authority")
	}
	if idToken == "" && accessToken == "" && bearer == "" {
		return nil, fmt.Errorf("OAuth session credential required")
	}
	for _, raw := range []string{idToken, accessToken, bearer} {
		if raw != "" {
			if err := checkJWTAlgorithmAllowlist(raw); err != nil {
				return nil, fmt.Errorf("OAuth session credential is not a supported signed token")
			}
		}
	}
	policy, err := resolveSessionOAuthPolicy(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("OAuth session verification unavailable")
	}
	verify := func(raw string) (map[string]interface{}, error) {
		if err := checkJWTAlgorithmAllowlist(raw); err != nil {
			return nil, fmt.Errorf("OAuth session credential is not a supported signed token")
		}
		verifier, err := cachedJWKSVerifier(ctx, policy.jwks)
		if err != nil {
			return nil, fmt.Errorf("OAuth session verification unavailable")
		}
		if _, err = verifier.VerifyClaims(ctx, raw); err != nil {
			return nil, fmt.Errorf("invalid or expired OAuth session credential")
		}
		claims := parseJWTClaims(raw)
		if err = validateVerifiedClaims(claims, policy.issuer, "", time.Now(), 0, true); err != nil {
			return nil, fmt.Errorf("OAuth session issuer or lifetime mismatch")
		}
		if strings.TrimSpace(claimString(claims, "sub")) == "" {
			return nil, fmt.Errorf("OAuth session subject required")
		}
		return claims, nil
	}
	var identity, access map[string]interface{}
	if idToken != "" {
		identity, err = verify(idToken)
		if err != nil {
			return nil, err
		}
		if err = validateIDTokenClaims(identity, policy.issuer, "", time.Now(), 0, policy.clientID); err != nil {
			return nil, fmt.Errorf("OAuth ID token audience or lifetime mismatch")
		}
		authorizedParty := claimString(identity, "azp")
		if (len(claimAudiences(identity)) > 1 || authorizedParty != "") && authorizedParty != policy.clientID {
			return nil, fmt.Errorf("OAuth ID token authorized party mismatch")
		}

	}
	if accessToken != "" && accessToken != idToken {
		access, err = verify(accessToken)
		if err != nil {
			return nil, err
		}
		matched := false
		for _, aud := range policy.audiences {
			if audienceContains(claimAudiences(access), aud) {
				matched = true
			}
		}
		if !matched {
			return nil, fmt.Errorf("OAuth access token audience mismatch")
		}
	}
	if identity == nil && access != nil {
		identity = access
	}
	if bearer != "" && bearer != idToken && bearer != accessToken {
		header, err := verify(bearer)
		if err != nil {
			return nil, err
		}
		matched := audienceContains(claimAudiences(header), policy.clientID)
		for _, aud := range policy.audiences {
			matched = matched || audienceContains(claimAudiences(header), aud)
		}
		if !matched {
			return nil, fmt.Errorf("OAuth bearer audience mismatch")
		}
		if identity == nil {
			identity = header
		} else if claimString(identity, "sub") != claimString(header, "sub") {
			return nil, fmt.Errorf("OAuth credential subjects differ")
		}
	}
	if identity == nil {
		return nil, fmt.Errorf("OAuth session credential required")
	}
	if access != nil && claimString(identity, "sub") != claimString(access, "sub") {
		return nil, fmt.Errorf("OAuth credential subjects differ")
	}
	if idToken != "" && accessToken != "" && accessToken != idToken {
		if err = validateSessionAccessHash(idToken, accessToken, identity); err != nil {
			return nil, err
		}
	}
	scopeClaims := identity
	if access != nil {
		scopeClaims = access
	}
	scopes := scopesFromClaims(scopeClaims)
	if err = validateOAuthScopeSet(cfg.OAuth.Client, nil, scopes); err != nil {
		return nil, err
	}
	exp, _ := claimUnixTime(identity, "exp")
	if access != nil {
		other, _ := claimUnixTime(access, "exp")
		if other.Before(exp) {
			exp = other
		}
	}
	return &verifiedSessionImport{Subject: claimString(identity, "sub"), Email: claimString(identity, "email"), Username: firstNonEmpty(claimString(identity, "preferred_username"), claimString(identity, "name"), claimString(identity, "sub")), Provider: configuredOAuthProvider(cfg), Scopes: scopes, Expiry: exp}, nil
}

type sessionOAuthPolicy struct {
	issuer, jwks, clientID string
	audiences              []string
}

func resolveSessionOAuthPolicy(ctx context.Context, cfg *Config) (*sessionOAuthPolicy, error) {
	client := cfg.OAuth.Client
	policy := &sessionOAuthPolicy{issuer: authcfg.NormalizeIssuer(client.Issuer), jwks: strings.TrimSpace(client.JWKSURL), clientID: strings.TrimSpace(client.ClientID), audiences: append([]string(nil), client.Audiences...)}
	var endpoints []string
	if client.DiscoveryURL != "" {
		endpoints = append(endpoints, client.DiscoveryURL)
	}
	if client.Issuer != "" {
		if endpoint, err := openIDDiscoveryURL(client.Issuer); err == nil {
			endpoints = append(endpoints, endpoint)
		}
	}
	if client.ConfigURL != "" {
		configured, err := loadOAuthClientConfig(ctx, client.ConfigURL)
		if err != nil {
			return nil, err
		}
		if policy.clientID == "" {
			policy.clientID = strings.TrimSpace(configured.ClientID)
		}
		for _, candidate := range issuerCandidatesFromAuthURL(configured.Endpoint.AuthURL) {
			if endpoint, err := openIDDiscoveryURL(candidate); err == nil {
				endpoints = append(endpoints, endpoint)
			}
		}
	}
	if policy.issuer == "" || policy.jwks == "" {
		for _, endpoint := range endpoints {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err != nil {
				continue
			}
			response, err := oauthMetadataHTTPClient(ctx).Do(req)
			if err != nil {
				continue
			}
			var metadata authmeta.OpenIDConfiguration
			err = json.NewDecoder(response.Body).Decode(&metadata)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || metadata.Issuer == "" || metadata.JwksURI == "" {
				continue
			}
			issuer := authcfg.NormalizeIssuer(metadata.Issuer)
			if policy.issuer != "" && policy.issuer != issuer {
				return nil, fmt.Errorf("configured OAuth issuer differs from discovery")
			}
			if policy.issuer == "" {
				policy.issuer = issuer
			}
			if policy.jwks == "" {
				policy.jwks = metadata.JwksURI
			}
			break
		}
	}
	if policy.issuer == "" || policy.jwks == "" || policy.clientID == "" {
		return nil, fmt.Errorf("OAuth issuer, JWKS and client audience are required")
	}
	if len(policy.audiences) == 0 {
		policy.audiences = []string{policy.clientID}
	}
	return policy, nil
}
func validateSessionAccessHash(idToken, accessToken string, claims map[string]interface{}) error {
	expected := claimString(claims, "at_hash")
	if expected == "" {
		return nil
	} // Optional for token-endpoint ID tokens; both JWTs are independently verified.
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return fmt.Errorf("invalid ID token")
	}
	header, err := decodeJWTSegment(parts[0])
	if err != nil {
		return fmt.Errorf("invalid ID token header")
	}
	alg, _ := header["alg"].(string)
	var digest []byte
	switch {
	case strings.HasSuffix(alg, "256"):
		v := sha256.Sum256([]byte(accessToken))
		digest = v[:]
	case strings.HasSuffix(alg, "384"):
		v := sha512.Sum384([]byte(accessToken))
		digest = v[:]
	case strings.HasSuffix(alg, "512"):
		v := sha512.Sum512([]byte(accessToken))
		digest = v[:]
	default:
		return fmt.Errorf("unsupported ID token access hash algorithm")
	}
	actual := base64.RawURLEncoding.EncodeToString(digest[:len(digest)/2])
	if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
		return fmt.Errorf("ID token does not bind supplied access token")
	}
	return nil
}
