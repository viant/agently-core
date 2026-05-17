package auth

import (
	"context"
	"testing"

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

	firstID, err := svc.UpsertWithProvider(ctx, "awitas", "awitas", "awitas@viantinc.com", "oauth", "awitas_viant_devtest")
	if err != nil {
		t.Fatalf("first UpsertWithProvider() error = %v", err)
	}
	if firstID == "" {
		t.Fatalf("first UpsertWithProvider() id was empty")
	}

	secondID, err := svc.UpsertWithProvider(ctx, "awitas_viant_devtest", "awitas_viant_devtest", "awitas@viantinc.com", "oauth", "awitas_viant_devtest")
	if err != nil {
		t.Fatalf("second UpsertWithProvider() error = %v", err)
	}
	if secondID != firstID {
		t.Fatalf("second UpsertWithProvider() id = %q, want %q", secondID, firstID)
	}

	user, err := svc.GetBySubjectAndProvider(ctx, "awitas_viant_devtest", "oauth")
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
		Email:       "awitas@viantinc.com",
		Provider:    "oauth",
		Subject:     "awitas_viant_devtest",
	}
	if !userMatchesDesired(user,
		"bc027324-cdef-4df8-bb5c-36cda0550722",
		"bc027324-cdef-4df8-bb5c-36cda0550722",
		"awitas@viantinc.com",
		"oauth",
		"awitas_viant_devtest",
		"",
		nil,
	) {
		t.Fatalf("expected exact existing canonical user to be treated as no-op")
	}
	if userMatchesDesired(user,
		"awitas",
		"awitas",
		"awitas@viantinc.com",
		"oauth",
		"awitas_viant_devtest",
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
		Email:       "awitas@viantinc.com",
		Provider:    "oauth",
		Subject:     "awitas_viant_devtest",
	}
	if !subjectIdentityReusable(user,
		"awitas@viantinc.com",
		"oauth",
		"awitas_viant_devtest",
		"",
		nil,
	) {
		t.Fatalf("expected canonical subject identity to be reusable without alias rewrite")
	}
	if subjectIdentityReusable(user,
		"other@viantinc.com",
		"oauth",
		"awitas_viant_devtest",
		"",
		nil,
	) {
		t.Fatalf("expected email mismatch to require write")
	}
}

func TestDatlyUserService_UpsertWithProvider_PreservesFriendlyDisplayIdentity(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}

	svc := NewDatlyUserService(dao)
	if svc == nil {
		t.Fatalf("NewDatlyUserService() = nil")
	}

	firstID, err := svc.UpsertWithProvider(ctx, "awitas", "awitas", "awitas@viantinc.com", "oauth", "awitas_viant_devtest")
	if err != nil {
		t.Fatalf("first UpsertWithProvider() error = %v", err)
	}
	if firstID == "" {
		t.Fatalf("first UpsertWithProvider() id was empty")
	}

	secondID, err := svc.UpsertWithProvider(ctx, "bc027324-cdef-4df8-bb5c-36cda0550722", "bc027324-cdef-4df8-bb5c-36cda0550722", "awitas@viantinc.com", "oauth", "awitas_viant_devtest")
	if err != nil {
		t.Fatalf("second UpsertWithProvider() error = %v", err)
	}
	if secondID != firstID {
		t.Fatalf("second UpsertWithProvider() id = %q, want %q", secondID, firstID)
	}

	user, err := svc.GetBySubjectAndProvider(ctx, "awitas_viant_devtest", "oauth")
	if err != nil {
		t.Fatalf("GetBySubjectAndProvider() error = %v", err)
	}
	if user == nil {
		t.Fatalf("GetBySubjectAndProvider() = nil")
	}
	if user.Username != "awitas" {
		t.Fatalf("user.Username = %q, want %q", user.Username, "awitas")
	}
	if user.DisplayName != "Awitas" {
		t.Fatalf("user.DisplayName = %q, want %q", user.DisplayName, "Awitas")
	}
}
