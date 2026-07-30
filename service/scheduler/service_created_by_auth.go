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
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	svcauth "github.com/viant/agently-core/service/auth"
)

func (s *Service) preloadCreatedByUserTokens(ctx context.Context, row *schedulepkg.ScheduleView, runID string) (context.Context, error) {
	if s == nil || row == nil || s.tokenProvider == nil || s.users == nil {
		return ctx, nil
	}
	subject := strings.TrimSpace(valueOrEmpty(row.CreatedByUserId))
	if subject == "" {
		return ctx, nil
	}
	provider := effectiveCreatedByUserProvider(ctx, s.authCfg)
	ctx = iauth.WithProvider(ctx, provider)
	safeCtx := iauth.WithoutTokens(ctx)

	user, err := s.users.GetBySubjectAndProvider(ctx, subject, provider)
	if err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "scheduler_owner_resolve",
			Provider:       provider,
			ScheduleID:     row.Id,
			RunID:          runID,
			Classification: "owner_resolution",
			Action:         "preserve_no_inject",
		})
		return safeCtx, fmt.Errorf("scheduler auth owner resolution failed")
	}
	if user == nil || strings.TrimSpace(user.ID) == "" {
		logx.DebugCtxf(ctx, "auth-token", "schedule_id=%q run_id=%q provider=%q owner resolution miss",
			strings.TrimSpace(row.Id), strings.TrimSpace(runID), provider)
		return safeCtx, nil
	}

	next, err := s.tokenProvider.EnsureTokens(safeCtx, token.Key{Subject: strings.TrimSpace(user.ID), Provider: provider})
	if err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "scheduler_token_ensure",
			UserID:         strings.TrimSpace(user.ID),
			Provider:       provider,
			ScheduleID:     row.Id,
			RunID:          runID,
			Classification: "refresh_error",
			Action:         "preserve_no_inject",
		})
		return safeCtx, fmt.Errorf("scheduler created-by token resolution failed")
	}
	if !hasUsableCreatedByAuth(next) {
		authlog.Log(ctx, authlog.Event{
			Op:             "scheduler_token_ensure",
			UserID:         strings.TrimSpace(user.ID),
			Provider:       provider,
			ScheduleID:     row.Id,
			RunID:          runID,
			Classification: "token_unavailable",
			Action:         "preserve_no_inject",
		})
		return safeCtx, fmt.Errorf("scheduler created-by token unavailable")
	}
	logx.DebugCtxf(ctx, "auth-token", "schedule_id=%q run_id=%q user_id=%q provider=%q token_ready=%t",
		strings.TrimSpace(row.Id), strings.TrimSpace(runID), strings.TrimSpace(user.ID), provider, iauth.TokensFromContext(next) != nil)
	return next, nil
}

func hasUsableCreatedByAuth(ctx context.Context) bool {
	if tokens := iauth.TokensFromContext(ctx); tokens != nil {
		if !tokens.Expiry.IsZero() && !tokens.Expiry.After(time.Now()) {
			return false
		}
		if strings.TrimSpace(tokens.AccessToken) != "" || strings.TrimSpace(tokens.IDToken) != "" {
			return true
		}
	}
	return strings.TrimSpace(iauth.Bearer(ctx)) != "" || strings.TrimSpace(iauth.IDToken(ctx)) != ""
}

func effectiveCreatedByUserProvider(ctx context.Context, cfg *svcauth.Config) string {
	if provider := strings.TrimSpace(iauth.Provider(ctx)); provider != "" {
		return provider
	}
	if cfg != nil && cfg.OAuth != nil {
		if provider := strings.TrimSpace(cfg.OAuth.Name); provider != "" {
			return provider
		}
		return "oauth"
	}
	return "oauth"
}
