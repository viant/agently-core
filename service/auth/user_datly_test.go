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
