package scheduler

import (
	"context"
	"testing"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	svcauth "github.com/viant/agently-core/service/auth"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

type fakeSchedulerUserService struct {
	user     *svcauth.User
	subject  string
	provider string
}

func (f *fakeSchedulerUserService) GetByUsername(context.Context, string) (*svcauth.User, error) {
	return nil, nil
}

func (f *fakeSchedulerUserService) GetBySubjectAndProvider(_ context.Context, subject, provider string) (*svcauth.User, error) {
	f.subject = subject
	f.provider = provider
	return f.user, nil
}

func (f *fakeSchedulerUserService) Upsert(context.Context, *svcauth.User) error { return nil }

func (f *fakeSchedulerUserService) UpsertWithProvider(context.Context, string, string, string, string, string) (string, error) {
	return "", nil
}

func (f *fakeSchedulerUserService) UpdateHashIPByID(context.Context, string, string) error {
	return nil
}

func (f *fakeSchedulerUserService) UpdatePreferences(context.Context, string, *svcauth.PreferencesPatch) error {
	return nil
}

type fakeSchedulerTokenProvider struct {
	key         token.Key
	ensureCalls int
}

func (f *fakeSchedulerTokenProvider) EnsureTokens(ctx context.Context, key token.Key) (context.Context, error) {
	f.key = key
	f.ensureCalls++
	return iauth.WithTokens(ctx, &scyauth.Token{
		Token: oauth2.Token{
			AccessToken: "translated-access",
			Expiry:      time.Now().Add(time.Hour),
		},
		IDToken: "translated-id",
	}), nil
}

func (f *fakeSchedulerTokenProvider) Store(context.Context, token.Key, *scyauth.Token) error {
	return nil
}

func (f *fakeSchedulerTokenProvider) Invalidate(context.Context, token.Key) error { return nil }

func TestService_preloadCreatedByUserTokens_UsesTranslatedUserID(t *testing.T) {
	subject := "agently_scheduler"
	row := &schedulepkg.ScheduleView{
		Id:              "sched-1",
		CreatedByUserId: &subject,
	}
	users := &fakeSchedulerUserService{
		user: &svcauth.User{
			ID:       "user-uuid-123",
			Subject:  subject,
			Provider: "oauth",
		},
	}
	tokens := &fakeSchedulerTokenProvider{}
	svc := New(nil, nil,
		WithAuthConfig(&svcauth.Config{
			OAuth: &svcauth.OAuth{Name: "oauth"},
		}),
		WithUserService(users),
		WithTokenProvider(tokens),
	)

	got := svc.preloadCreatedByUserTokens(context.Background(), row, "run-1")

	if users.subject != subject || users.provider != "oauth" {
		t.Fatalf("GetBySubjectAndProvider() called with (%q, %q), want (%q, %q)", users.subject, users.provider, subject, "oauth")
	}
	if tokens.ensureCalls != 1 {
		t.Fatalf("EnsureTokens() calls = %d, want 1", tokens.ensureCalls)
	}
	if tokens.key.Subject != "user-uuid-123" || tokens.key.Provider != "oauth" {
		t.Fatalf("EnsureTokens() key = (%q, %q), want (%q, %q)", tokens.key.Subject, tokens.key.Provider, "user-uuid-123", "oauth")
	}
	if iauth.Provider(got) != "oauth" {
		t.Fatalf("Provider() = %q, want %q", iauth.Provider(got), "oauth")
	}
	if tok := iauth.TokensFromContext(got); tok == nil || tok.AccessToken != "translated-access" {
		t.Fatalf("expected translated tokens in context")
	}
}
