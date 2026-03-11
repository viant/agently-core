package auth

import (
	"context"

	iauth "github.com/viant/agently-core/internal/auth"
)

// InjectUser stores user identity in context using the agently-core auth key.
// External middleware (outside this module) can call this to bridge their own
// auth context into the context key that EffectiveUserID reads.
func InjectUser(ctx context.Context, subject string) context.Context {
	if subject == "" {
		return ctx
	}
	return iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: subject})
}
