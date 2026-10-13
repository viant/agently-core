package user

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	iauth "github.com/viant/agently-core/internal/auth"
	identity "github.com/viant/agently-core/protocol/resource"
	useruri "github.com/viant/agently-core/protocol/resource/user"
)

// CanonicalOwner accepts only the verified persistence identity carried by the
// ingress context. A URI or a display/email name never supplies that identity.
func CanonicalOwner(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", identity.ErrResourceDenied
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	owner := iauth.CanonicalUserID(ctx)
	if _, err := useruri.NewURI("report", owner, "record"); err != nil || iauth.EffectiveUserID(ctx) == "" || iauth.Provider(ctx) == "" {
		return "", identity.ErrResourceDenied
	}
	return owner, nil
}

func OwnURI(ctx context.Context, uri identity.ResourceURI) (string, string, error) {
	owner, err := CanonicalOwner(ctx)
	if err != nil {
		return "", "", err
	}
	named, path, ok := useruri.Parse(uri)
	if !ok || named != owner {
		return "", "", identity.ErrResourceDenied
	}
	return owner, path, nil
}

// Authorize is shared ownership enforcement for any managing user namespace
// source. Corporate read actors may remain PublicRead; verified workspace
// ingress identity is the independent authority for private user resources.
func Authorize(ctx context.Context, _ identity.VerifiedActor, uri identity.ResourceURI, operation string) error {
	if _, err := CanonicalOwner(ctx); err != nil {
		return err
	}
	if uri.Namespace != useruri.Namespace || !uri.Valid() {
		return identity.ErrResourceDenied
	}
	if operation == "namespace.list" || operation == "namespace.get" {
		return nil
	}
	_, _, err := OwnURI(ctx, uri)
	return err
}
func userAuthority(ctx context.Context, uri string) (string, error) {
	owner, err := CanonicalOwner(ctx)
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal([]string{owner, iauth.Provider(ctx), iauth.EffectiveUserID(ctx), uri})
	return fmt.Sprintf("user:%x", sha256.Sum256(data)), nil
}

// canonicalStorageContext is private to persistence adaptation. It retains
// caller tokens and verified-provider markers without replacing the original
// ingress identity used for dispatch, logging or user-facing operations.
func canonicalStorageContext(ctx context.Context) (context.Context, error) {
	owner, err := CanonicalOwner(ctx)
	if err != nil {
		return nil, err
	}
	copy := iauth.UserInfo{}
	if current := iauth.User(ctx); current != nil {
		copy = *current
	}
	copy.Subject = owner
	return iauth.WithUserInfo(ctx, &copy), nil
}
func rowStorageContext(ctx context.Context, owner string) (context.Context, error) {
	canonical, err := CanonicalOwner(ctx)
	if err != nil {
		return nil, err
	}
	if owner == canonical {
		return canonicalStorageContext(ctx)
	}
	if owner == iauth.EffectiveUserID(ctx) {
		return ctx, nil
	}
	return nil, identity.ErrResourceDenied
}
