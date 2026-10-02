package executionprotection

import (
	"context"
	"strings"
	"testing"
	"time"

	toolprotection "github.com/viant/agently-core/protocol/tool/protection"
)

func TestComponentRepositoryDefersAvailabilityFailure(t *testing.T) {
	repository := NewComponentRepository(nil)
	if repository == nil {
		t.Fatal("NewComponentRepository(nil) = nil")
	}
	_, err := repository.Claim(context.Background(), ClaimRecord{
		ClaimKey:     strings.Repeat("a", 64),
		SemanticHash: strings.Repeat("b", 64),
		CreatedAt:    time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("Claim() with unavailable component host error = nil")
	}
	if err := repository.Finish(context.Background(), strings.Repeat("a", 64), toolprotection.StateCompleted, time.Now().UTC()); err == nil {
		t.Fatal("Finish() with unavailable component host error = nil")
	}
}
