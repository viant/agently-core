package auth

import (
	"context"
	"net/http"
	"strings"

	iauth "github.com/viant/agently-core/internal/auth"
	scyauth "github.com/viant/scy/auth"
	vcfg "github.com/viant/scy/auth/jwt/verifier"
)

type Runtime struct {
	cfg         *Config
	sessions    *Manager
	jwtMintKey  string
	jwtVerifier *vcfg.Service
	jwtService  *JWTService
	handlerOpts []HandlerOption
	ext         *authExtension
	stopRefresh func()
}

type runtimeAuthUser struct {
	Subject  string
	Email    string
	Provider string
	Tokens   *scyauth.Token
}

type runtimeAuthContextKey struct{}

func WithAuthExtensions(base http.Handler, runtime *Runtime) http.Handler {
	if runtime == nil || runtime.ext == nil {
		return base
	}
	mux := http.NewServeMux()
	runtime.ext.Register(mux)
	mux.Handle("/", base)
	return runtime.protect(mux)
}

func WithAuthProtection(base http.Handler, runtime *Runtime) http.Handler {
	if runtime == nil {
		return base
	}
	return runtime.protectAll(base)
}

func withRuntimeAuthUser(ctx context.Context, user *runtimeAuthUser) context.Context {
	if user == nil {
		return ctx
	}
	ctx = context.WithValue(ctx, runtimeAuthContextKey{}, *user)
	ctx = InjectUser(ctx, user.Subject)
	if strings.TrimSpace(user.Provider) != "" {
		ctx = iauth.WithProvider(ctx, user.Provider)
	}
	if user.Tokens != nil {
		ctx = InjectTokens(ctx, user.Tokens)
	}
	return ctx
}

func RuntimeUserFromContext(ctx context.Context) *UserInfo {
	if ctx == nil {
		return nil
	}
	if raw, ok := ctx.Value(runtimeAuthContextKey{}).(runtimeAuthUser); ok {
		return &UserInfo{
			Subject: raw.Subject,
			Email:   raw.Email,
		}
	}
	return nil
}
