package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	scyauth "github.com/viant/scy/auth"
	"github.com/viant/scy/auth/authorizer"
)

// applyUserCred loads credentials from a scy secret URL and injects tokens
// into the context for downstream MCP tool calls.
func (s *Service) applyUserCred(ctx context.Context, credRef string) (context.Context, error) {
	if credRef == "" {
		return ctx, nil
	}
	return s.applyUserCredLegacyOOB(ctx, credRef)
}

func (s *Service) applyUserCredLegacyOOB(ctx context.Context, credRef string) (context.Context, error) {
	cfg := s.resolveUserCredAuthConfig()
	if cfg == nil {
		return ctx, fmt.Errorf("schedule user_cred_url requires auth.oauth configuration")
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode != "bff" {
		return ctx, fmt.Errorf("schedule user_cred_url requires auth.oauth.mode=bff")
	}
	cfgURL := strings.TrimSpace(cfg.ClientConfigURL)
	if cfgURL == "" {
		return ctx, fmt.Errorf("schedule user_cred_url requires auth.oauth.client.configURL")
	}

	cmd := &authorizer.Command{
		AuthFlow:   "OOB",
		UsePKCE:    true,
		SecretsURL: strings.TrimSpace(credRef),
		OAuthConfig: authorizer.OAuthConfig{
			ConfigURL: cfgURL,
		},
	}
	if scopes := cfg.Scopes; len(scopes) > 0 {
		cmd.Scopes = append([]string(nil), scopes...)
	} else {
		cmd.Scopes = []string{"openid"}
	}
	meta, userID := schedulerAuthMeta(ctx)
	logAuthf("schedule=%q run=%q user=%q user_cred authorize start ref_kind=%q scopes=%d",
		strings.TrimSpace(meta.ScheduleID), strings.TrimSpace(meta.ScheduleRunID), userID, userCredRefKind(credRef), len(cmd.Scopes))

	authCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 1*time.Minute)
	defer cancel()
	oa := s.oauthAuthz
	if oa == nil {
		oa = authorizer.New()
	}
	oauthTok, err := oa.Authorize(authCtx, cmd)
	if err != nil {
		logAuthf("schedule=%q run=%q user=%q user_cred authorize failed ref_kind=%q err=%v",
			strings.TrimSpace(meta.ScheduleID), strings.TrimSpace(meta.ScheduleRunID), userID, userCredRefKind(credRef), err)
		return ctx, fmt.Errorf("schedule user_cred authorize failed: %w", err)
	}
	if oauthTok == nil {
		return ctx, fmt.Errorf("schedule user_cred authorize returned empty token")
	}

	st := &scyauth.Token{Token: *oauthTok}
	st.PopulateIDToken()
	if s.tokenProvider != nil && strings.TrimSpace(st.RefreshToken) != "" {
		key := token.Key{Subject: credRef, Provider: effectiveSchedulerTokenProvider(cfg)}
		_ = s.tokenProvider.Store(ctx, key, st)
		next, ensureErr := s.tokenProvider.EnsureTokens(ctx, key)
		if ensureErr == nil {
			logAuthf("schedule=%q run=%q user=%q user_cred authorize ok ref_kind=%q has_access=%t has_refresh=%t has_id=%t",
				strings.TrimSpace(meta.ScheduleID), strings.TrimSpace(meta.ScheduleRunID), userID, userCredRefKind(credRef),
				strings.TrimSpace(st.AccessToken) != "", strings.TrimSpace(st.RefreshToken) != "", strings.TrimSpace(st.IDToken) != "")
			return next, nil
		}
		log.Printf("scheduler: ensure tokens after oob auth failed, using oauth token directly: %v", ensureErr)
	}
	logAuthf("schedule=%q run=%q user=%q user_cred authorize ok ref_kind=%q has_access=%t has_refresh=%t has_id=%t",
		strings.TrimSpace(meta.ScheduleID), strings.TrimSpace(meta.ScheduleRunID), userID, userCredRefKind(credRef),
		strings.TrimSpace(st.AccessToken) != "", strings.TrimSpace(st.RefreshToken) != "", strings.TrimSpace(st.IDToken) != "")
	return s.withAuthTokens(ctx, st), nil
}

func effectiveSchedulerTokenProvider(cfg *UserCredAuthConfig) string {
	if cfg != nil {
		return "oauth"
	}
	return "default"
}

func (s *Service) withAuthTokens(ctx context.Context, tok *scyauth.Token) context.Context {
	if tok == nil {
		return ctx
	}
	ctx = iauth.WithTokens(ctx, tok)
	if v := strings.TrimSpace(tok.AccessToken); v != "" {
		ctx = iauth.WithBearer(ctx, v)
	}
	if v := strings.TrimSpace(tok.IDToken); v != "" {
		ctx = iauth.WithIDToken(ctx, v)
	}
	return ctx
}
