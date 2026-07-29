package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	"github.com/viant/agently-core/internal/authlog"
	"github.com/viant/agently-core/internal/logx"
	svcauth "github.com/viant/agently-core/service/auth"
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
	safeCtx := iauth.WithoutTokens(ctx)
	cfg := s.resolveUserCredAuthConfig()
	if cfg == nil {
		return safeCtx, fmt.Errorf("schedule user_cred_url requires auth.oauth configuration")
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode != "bff" {
		return safeCtx, fmt.Errorf("schedule user_cred_url requires auth.oauth.mode=bff")
	}
	cfgURL := strings.TrimSpace(cfg.ClientConfigURL)
	if cfgURL == "" {
		return safeCtx, fmt.Errorf("schedule user_cred_url requires auth.oauth.client.configURL")
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
	logx.DebugCtxf(ctx, "auth-token", "schedule_id=%q run_id=%q user_id=%q user_cred authorize start ref_kind=%q scopes=%d",
		strings.TrimSpace(meta.ScheduleID), strings.TrimSpace(meta.ScheduleRunID), userID, userCredRefKind(credRef), len(cmd.Scopes))

	authCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 1*time.Minute)
	defer cancel()
	oa := s.oauthAuthz
	if oa == nil {
		oa = authorizer.New()
	}
	oauthTok, err := oa.Authorize(authCtx, cmd)
	if err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "scheduler_oob_authorize",
			Provider:       effectiveSchedulerTokenProvider(cfg),
			ScheduleID:     meta.ScheduleID,
			RunID:          meta.ScheduleRunID,
			Classification: "refresh_error",
			Action:         "preserve_no_inject",
		})
		return safeCtx, fmt.Errorf("schedule user_cred authorize failed")
	}
	if oauthTok == nil {
		return safeCtx, fmt.Errorf("schedule user_cred authorize returned empty token")
	}

	st := &scyauth.Token{Token: *oauthTok}
	st.PopulateIDToken()
	if err := validateSchedulerOAuthToken(st, cmd.Scopes, ""); err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "scheduler_oob_scope_validate",
			Provider:       effectiveSchedulerTokenProvider(cfg),
			ScheduleID:     meta.ScheduleID,
			RunID:          meta.ScheduleRunID,
			Classification: "scope_validation",
			Action:         "preserve_no_inject",
			Err:            err,
		})
		return safeCtx, fmt.Errorf("schedule user_cred token is unusable")
	}
	if s.tokenProvider != nil && strings.TrimSpace(st.RefreshToken) != "" {
		key := token.Key{Subject: credRef, Provider: effectiveSchedulerTokenProvider(cfg)}
		if err := s.tokenProvider.Store(ctx, key, st); err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "scheduler_oob_store",
				Provider:       effectiveSchedulerTokenProvider(cfg),
				ScheduleID:     meta.ScheduleID,
				RunID:          meta.ScheduleRunID,
				Classification: "persistence",
				Action:         "preserve_no_inject",
			})
			return safeCtx, fmt.Errorf("schedule user_cred token persistence failed")
		}
		next, err := s.tokenProvider.EnsureTokens(safeCtx, key)
		if err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "scheduler_oob_ensure",
				Provider:       effectiveSchedulerTokenProvider(cfg),
				ScheduleID:     meta.ScheduleID,
				RunID:          meta.ScheduleRunID,
				Classification: "refresh_error",
				Action:         "preserve_no_inject",
			})
			return safeCtx, fmt.Errorf("schedule user_cred token resolution failed")
		}
		if err := validateSchedulerAuthContext(next, cmd.Scopes, st); err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "scheduler_oob_ensure",
				Provider:       effectiveSchedulerTokenProvider(cfg),
				ScheduleID:     meta.ScheduleID,
				RunID:          meta.ScheduleRunID,
				Classification: "token_unavailable",
				Action:         "preserve_no_inject",
				Err:            err,
			})
			return safeCtx, fmt.Errorf("schedule user_cred returned no usable token")
		}
		logx.DebugCtxf(ctx, "auth-token", "schedule_id=%q run_id=%q user_id=%q user_cred authorize ok ref_kind=%q",
			strings.TrimSpace(meta.ScheduleID), strings.TrimSpace(meta.ScheduleRunID), userID, userCredRefKind(credRef))
		return next, nil
	}
	logx.DebugCtxf(ctx, "auth-token", "schedule_id=%q run_id=%q user_id=%q user_cred authorize ok ref_kind=%q",
		strings.TrimSpace(meta.ScheduleID), strings.TrimSpace(meta.ScheduleRunID), userID, userCredRefKind(credRef))
	return s.withAuthTokens(safeCtx, st), nil
}

func validateSchedulerAuthContext(ctx context.Context, expectedScopes []string, candidate *scyauth.Token) error {
	tokens := iauth.TokensFromContext(ctx)
	if tokens != nil {
		return validateSchedulerOAuthToken(tokens, expectedScopes, iauth.Bearer(ctx))
	}
	bearer := strings.TrimSpace(iauth.Bearer(ctx))
	if bearer == "" || candidate == nil {
		return fmt.Errorf("oauth token was not injected")
	}
	if bearer != strings.TrimSpace(candidate.AccessToken) && bearer != strings.TrimSpace(candidate.IDToken) {
		return fmt.Errorf("oauth bearer cannot be matched to validated token")
	}
	return validateSchedulerOAuthToken(candidate, expectedScopes, bearer)
}

func validateSchedulerOAuthToken(token *scyauth.Token, expectedScopes []string, bearer string) error {
	if token == nil {
		return fmt.Errorf("oauth token is missing")
	}
	if !token.Expiry.IsZero() && !token.Expiry.After(time.Now()) {
		return fmt.Errorf("oauth token is expired")
	}
	if err := svcauth.ValidateOAuthTokenScopes(expectedScopes, token); err != nil {
		return err
	}
	if strings.TrimSpace(token.AccessToken) == "" &&
		strings.TrimSpace(token.IDToken) == "" &&
		strings.TrimSpace(bearer) == "" {
		return fmt.Errorf("oauth token has no usable bearer credential")
	}
	return nil
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
