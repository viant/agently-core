package scheduler

import (
	"context"
	"strings"

	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	svcauth "github.com/viant/agently-core/service/auth"
)

func (s *Service) preloadCreatedByUserTokens(ctx context.Context, row *schedulepkg.ScheduleView, runID string) context.Context {
	if s == nil || row == nil || s.tokenProvider == nil || s.users == nil {
		return ctx
	}
	subject := strings.TrimSpace(valueOrEmpty(row.CreatedByUserId))
	if subject == "" {
		return ctx
	}
	provider := effectiveCreatedByUserProvider(ctx, s.authCfg)
	ctx = iauth.WithProvider(ctx, provider)

	user, err := s.users.GetBySubjectAndProvider(ctx, subject, provider)
	if err != nil {
		logAuthRunf(row.Id, runID, subject, "created_by_user_id auth user translation failed provider=%q err=%v", provider, err)
		return ctx
	}
	if user == nil || strings.TrimSpace(user.ID) == "" {
		logAuthRunf(row.Id, runID, subject, "created_by_user_id auth user translation miss provider=%q", provider)
		return ctx
	}

	next, err := s.tokenProvider.EnsureTokens(ctx, token.Key{Subject: strings.TrimSpace(user.ID), Provider: provider})
	if err != nil {
		logAuthRunf(row.Id, runID, subject, "created_by_user_id auth token ensure failed provider=%q user_id=%q err=%v", provider, strings.TrimSpace(user.ID), err)
		return ctx
	}
	logAuthRunf(row.Id, runID, subject, "created_by_user_id auth translated provider=%q user_id=%q token_ready=%t", provider, strings.TrimSpace(user.ID), iauth.TokensFromContext(next) != nil)
	return next
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
