package toolexecutionclaim

import (
	"errors"
	"fmt"
	"github.com/viant/sqlx/io/errx"
	"testing"
)

func TestClaimDuplicateUsesSQLXClassification(t *testing.T) {
	cause := errors.New("driver-specific extended constraint code")
	duplicate := errx.DuplicateKey("insert", "tool_execution_claim", cause)
	if !isUniqueViolation(fmt.Errorf("claim failed: %w", duplicate)) {
		t.Fatal("typed SQLX duplicate must survive wrapping")
	}
	if isUniqueViolation(errx.Constraint("insert", "tool_execution_claim", cause)) {
		t.Fatal("ordinary constraints must not be treated as an existing claim")
	}
	if isUniqueViolation(nil) {
		t.Fatal("nil classified as duplicate")
	}
}
