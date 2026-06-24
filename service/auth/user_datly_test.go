package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/viant/agently-core/app/store/data"
)

func TestDatlyUserService_UpsertWithProvider_ReusesExistingSubjectIdentity(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}

	svc := NewDatlyUserService(dao)
	if svc == nil {
		t.Fatalf("NewDatlyUserService() = nil")
	}
	username, email, subject := uniqueDatlyUserIdentity(t, "reuse")

	firstID, err := svc.UpsertWithProvider(ctx, username, username, email, "oauth", subject)
	if err != nil {
		t.Fatalf("first UpsertWithProvider() error = %v", err)
	}
	if firstID == "" {
		t.Fatalf("first UpsertWithProvider() id was empty")
	}

	secondID, err := svc.UpsertWithProvider(ctx, subject, subject, email, "oauth", subject)
	if err != nil {
		t.Fatalf("second UpsertWithProvider() error = %v", err)
	}
	if secondID != firstID {
		t.Fatalf("second UpsertWithProvider() id = %q, want %q", secondID, firstID)
	}

	user, err := svc.GetBySubjectAndProvider(ctx, subject, "oauth")
	if err != nil {
		t.Fatalf("GetBySubjectAndProvider() error = %v", err)
	}
	if user == nil {
		t.Fatalf("GetBySubjectAndProvider() = nil")
	}
	if user.ID != firstID {
		t.Fatalf("user.ID = %q, want %q", user.ID, firstID)
	}
}

func TestUserMatchesDesired(t *testing.T) {
	user := &User{
		ID:          "u1",
		Username:    "bc027324-cdef-4df8-bb5c-36cda0550722",
		DisplayName: "bc027324-cdef-4df8-bb5c-36cda0550722",
		Email:       "user@example.test",
		Provider:    "oauth",
		Subject:     "oauth_subject_test",
	}
	if !userMatchesDesired(user,
		"bc027324-cdef-4df8-bb5c-36cda0550722",
		"bc027324-cdef-4df8-bb5c-36cda0550722",
		"user@example.test",
		"oauth",
		"oauth_subject_test",
		"",
		nil,
	) {
		t.Fatalf("expected exact existing canonical user to be treated as no-op")
	}
	if userMatchesDesired(user,
		"localuser",
		"localuser",
		"user@example.test",
		"oauth",
		"oauth_subject_test",
		"",
		nil,
	) {
		t.Fatalf("expected changed username/display name to require write")
	}
}

func TestSubjectIdentityReusable_IgnoresAliasUsernameDrift(t *testing.T) {
	user := &User{
		ID:          "bc027324-cdef-4df8-bb5c-36cda0550722",
		Username:    "bc027324-cdef-4df8-bb5c-36cda0550722",
		DisplayName: "bc027324-cdef-4df8-bb5c-36cda0550722",
		Email:       "user@example.test",
		Provider:    "oauth",
		Subject:     "oauth_subject_test",
	}
	if !subjectIdentityReusable(user,
		"user@example.test",
		"oauth",
		"oauth_subject_test",
		"",
		nil,
	) {
		t.Fatalf("expected canonical subject identity to be reusable without alias rewrite")
	}
	if subjectIdentityReusable(user,
		"other@example.test",
		"oauth",
		"oauth_subject_test",
		"",
		nil,
	) {
		t.Fatalf("expected email mismatch to require write")
	}
}

func TestDatlyUserService_UpsertWithProvider_PreservesExactDisplayIdentity(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}

	svc := NewDatlyUserService(dao)
	if svc == nil {
		t.Fatalf("NewDatlyUserService() = nil")
	}
	username, email, subject := uniqueDatlyUserIdentity(t, "exact")

	firstID, err := svc.UpsertWithProvider(ctx, username, username, email, "oauth", subject)
	if err != nil {
		t.Fatalf("first UpsertWithProvider() error = %v", err)
	}
	if firstID == "" {
		t.Fatalf("first UpsertWithProvider() id was empty")
	}

	secondID, err := svc.UpsertWithProvider(ctx, "bc027324-cdef-4df8-bb5c-36cda0550722", "bc027324-cdef-4df8-bb5c-36cda0550722", email, "oauth", subject)
	if err != nil {
		t.Fatalf("second UpsertWithProvider() error = %v", err)
	}
	if secondID != firstID {
		t.Fatalf("second UpsertWithProvider() id = %q, want %q", secondID, firstID)
	}

	user, err := svc.GetBySubjectAndProvider(ctx, subject, "oauth")
	if err != nil {
		t.Fatalf("GetBySubjectAndProvider() error = %v", err)
	}
	if user == nil {
		t.Fatalf("GetBySubjectAndProvider() = nil")
	}
	if user.Username != username {
		t.Fatalf("user.Username = %q, want %q", user.Username, username)
	}
	if user.DisplayName != username {
		t.Fatalf("user.DisplayName = %q, want %q", user.DisplayName, username)
	}
}

func uniqueDatlyUserIdentity(t *testing.T, prefix string) (username, email, subject string) {
	t.Helper()
	token := strings.ReplaceAll(uuid.NewString(), "-", "")
	name := prefix + "_" + token
	return name, name + "@example.test", "oauth_" + name
}
