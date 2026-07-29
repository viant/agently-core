package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	runtimediscovery "github.com/viant/agently-core/runtime/discovery"
	agentsvc "github.com/viant/agently-core/service/agent"
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
	storeCalls  int
	stored      *scyauth.Token
	storeErr    error
	ensureErr   error
	ensureEmpty bool
	ensureToken *scyauth.Token
}

func (f *fakeSchedulerTokenProvider) EnsureTokens(ctx context.Context, key token.Key) (context.Context, error) {
	f.key = key
	f.ensureCalls++
	if f.ensureErr != nil {
		return ctx, f.ensureErr
	}
	if f.ensureEmpty {
		return ctx, nil
	}
	ensured := f.ensureToken
	if ensured == nil {
		ensured = &scyauth.Token{
			Token: oauth2.Token{
				AccessToken: "translated-access",
				Expiry:      time.Now().Add(time.Hour),
			},
			IDToken: "translated-id",
		}
	}
	return iauth.WithTokens(ctx, ensured), nil
}

func (f *fakeSchedulerTokenProvider) Store(_ context.Context, _ token.Key, stored *scyauth.Token) error {
	f.storeCalls++
	f.stored = stored
	return f.storeErr
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

	got, err := svc.preloadCreatedByUserTokens(context.Background(), row, "run-1")
	if err != nil {
		t.Fatalf("preloadCreatedByUserTokens() error = %v", err)
	}

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

func TestService_executeRun_CreatedByAuthFailureContinuesWithoutTokens(t *testing.T) {
	store, db := newTestStore(t)
	ensureRunWriteComponent(t, store)
	subject := "agently_scheduler"
	const scheduleID = "sched-created-by-auth-failure"
	insertScheduleRowWithOwner(t, db, scheduleID, "Created-by auth failure", "private", subject)
	users := &fakeSchedulerUserService{
		user: &svcauth.User{
			ID:       "user-uuid-123",
			Subject:  subject,
			Provider: "oauth",
		},
	}
	tokens := &fakeSchedulerTokenProvider{ensureErr: context.DeadlineExceeded}
	queryCalled := false
	svc := New(store, &agentsvc.Service{},
		WithAuthConfig(&svcauth.Config{
			OAuth: &svcauth.OAuth{Name: "oauth"},
		}),
		WithUserService(users),
		WithTokenProvider(tokens),
	)
	svc.queryRunner = func(ctx context.Context, input *agentsvc.QueryInput, _ *agentsvc.QueryOutput) error {
		queryCalled = true
		if iauth.TokensFromContext(ctx) != nil || iauth.Bearer(ctx) != "" || iauth.IDToken(ctx) != "" {
			t.Fatal("query received credentials after created-by auth failure")
		}
		if got := strings.TrimSpace(iauth.EffectiveUserID(ctx)); got != subject {
			t.Fatalf("query effective user = %q, want %q", got, subject)
		}
		if input == nil || input.UserId != subject {
			t.Fatalf("query input user = %#v, want %q", input, subject)
		}
		return nil
	}
	row := &schedulepkg.ScheduleView{
		Id:              scheduleID,
		Name:            "Created-by auth failure",
		CreatedByUserId: &subject,
		AgentRef:        "steward",
		ScheduleType:    "adhoc",
		Timezone:        "UTC",
		Enabled:         true,
	}

	ctx := iauth.WithTokens(context.Background(), &scyauth.Token{
		Token: oauth2.Token{
			AccessToken: "stale-access",
			Expiry:      time.Now().Add(-time.Minute),
		},
		IDToken: "stale-id",
	})
	ctx = iauth.WithBearer(ctx, "stale-access")
	ctx = iauth.WithIDToken(ctx, "stale-id")
	svc.executeRun(ctx, row, "run-created-by-auth-failure", time.Now().UTC())

	if !queryCalled {
		t.Fatal("query was not called after created-by auth failure")
	}
	if tokens.ensureCalls != 1 {
		t.Fatalf("EnsureTokens() calls = %d, want 1", tokens.ensureCalls)
	}
	assertScheduleLastResult(t, db, scheduleID, "succeeded")
}

func TestService_executeRun_CreatedByOwnerLookupMissContinuesWithoutTokens(t *testing.T) {
	store, db := newTestStore(t)
	ensureRunWriteComponent(t, store)
	const subject = "agently_scheduler"
	const email = "scheduler@example.com"
	const scheduleID = "sched-created-by-owner-lookup-miss"
	insertScheduleRowWithOwner(t, db, scheduleID, "Created-by owner lookup miss", "private", subject)
	users := &fakeSchedulerUserService{}
	tokens := &fakeSchedulerTokenProvider{}
	queryCalled := false
	svc := New(store, &agentsvc.Service{},
		WithAuthConfig(&svcauth.Config{
			OAuth: &svcauth.OAuth{Name: "oauth"},
		}),
		WithUserService(users),
		WithTokenProvider(tokens),
	)
	svc.queryRunner = func(ctx context.Context, input *agentsvc.QueryInput, _ *agentsvc.QueryOutput) error {
		queryCalled = true
		if iauth.TokensFromContext(ctx) != nil || iauth.Bearer(ctx) != "" || iauth.IDToken(ctx) != "" {
			t.Fatal("query received credentials after created-by owner lookup miss")
		}
		userInfo := iauth.User(ctx)
		if userInfo == nil || userInfo.Subject != subject || userInfo.Email != email {
			t.Fatalf("query user info = %#v, want subject %q and email %q", userInfo, subject, email)
		}
		if input == nil || input.UserId != subject {
			t.Fatalf("query input user = %#v, want %q", input, subject)
		}
		return nil
	}
	row := &schedulepkg.ScheduleView{
		Id:              scheduleID,
		Name:            "Created-by owner lookup miss",
		CreatedByUserId: stringPtr(subject),
		AgentRef:        "steward",
		ScheduleType:    "adhoc",
		Timezone:        "UTC",
		Enabled:         true,
	}

	ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: subject, Email: email})
	ctx = iauth.WithTokens(ctx, &scyauth.Token{
		Token: oauth2.Token{
			AccessToken: "stale-access",
			Expiry:      time.Now().Add(-time.Minute),
		},
		IDToken: "stale-bundle-id",
	})
	ctx = iauth.WithBearer(ctx, "stale-bearer")
	ctx = iauth.WithIDToken(ctx, "stale-id")
	svc.executeRun(ctx, row, "run-created-by-owner-lookup-miss", time.Now().UTC())

	if !queryCalled {
		t.Fatal("query was not called after created-by owner lookup miss")
	}
	if users.subject != subject || users.provider != "oauth" {
		t.Fatalf("GetBySubjectAndProvider() called with (%q, %q), want (%q, %q)", users.subject, users.provider, subject, "oauth")
	}
	if tokens.ensureCalls != 0 {
		t.Fatalf("EnsureTokens() calls = %d, want 0", tokens.ensureCalls)
	}
	assertScheduleLastResult(t, db, scheduleID, "succeeded")
}

func TestService_executeRun_UsesSchedulerModeAndCreatedByAuth(t *testing.T) {
	store, _ := newTestStore(t)
	ensureRunWriteComponent(t, store)
	subject := "agently_scheduler"
	users := &fakeSchedulerUserService{
		user: &svcauth.User{
			ID:       "user-uuid-123",
			Subject:  subject,
			Provider: "oauth",
		},
	}
	tokens := &fakeSchedulerTokenProvider{}
	called := false
	svc := New(store, &agentsvc.Service{},
		WithAuthConfig(&svcauth.Config{
			OAuth: &svcauth.OAuth{Name: "oauth"},
		}),
		WithUserService(users),
		WithTokenProvider(tokens),
	)
	svc.queryRunner = func(ctx context.Context, input *agentsvc.QueryInput, _ *agentsvc.QueryOutput) error {
		called = true
		mode, ok := runtimediscovery.ModeFromContext(ctx)
		if !ok || !mode.Scheduler {
			t.Fatalf("query runner context mode = %#v, %t; want scheduler mode", mode, ok)
		}
		if mode.ScheduleID != "sched-created-by" || mode.ScheduleRunID != "run-created-by" {
			t.Fatalf("query runner schedule mode = (%q, %q), want (%q, %q)",
				mode.ScheduleID, mode.ScheduleRunID, "sched-created-by", "run-created-by")
		}
		if got := strings.TrimSpace(iauth.EffectiveUserID(ctx)); got != subject {
			t.Fatalf("query runner effective user = %q, want %q", got, subject)
		}
		if got := iauth.TokensFromContext(ctx); got == nil || got.AccessToken != "translated-access" {
			t.Fatalf("query runner did not receive translated created-by tokens")
		}
		if input == nil || input.UserId != subject {
			t.Fatalf("query input user = %#v, want %q", input, subject)
		}
		return nil
	}
	row := &schedulepkg.ScheduleView{
		Id:              "sched-created-by",
		Name:            "Created-by auth",
		CreatedByUserId: &subject,
		AgentRef:        "steward",
		ScheduleType:    "adhoc",
		Timezone:        "UTC",
		Enabled:         true,
	}

	svc.executeRun(context.Background(), row, "run-created-by", time.Now().UTC())

	if !called {
		t.Fatal("query runner was not called")
	}
	if tokens.ensureCalls != 1 {
		t.Fatalf("EnsureTokens() calls = %d, want 1", tokens.ensureCalls)
	}
}
