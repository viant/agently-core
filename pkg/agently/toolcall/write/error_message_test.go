package write

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeErrorMessageShortValue(t *testing.T) {
	const value = "execute tool failed: exit status 1"
	if got := SanitizeErrorMessage(value); got != value {
		t.Fatalf("expected unchanged short value, got %q", got)
	}
}

func TestSanitizeErrorMessageLongASCIIKeepsHeadMarkerAndTail(t *testing.T) {
	value := strings.Repeat("A", 40000) + "MIDDLE" + strings.Repeat("Z", 40000)
	got := SanitizeErrorMessage(value)
	if len(got) > MaxErrorMessageBytes {
		t.Fatalf("expected at most %d bytes, got %d", MaxErrorMessageBytes, len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8")
	}
	if !strings.HasPrefix(got, strings.Repeat("A", 1000)) {
		t.Fatalf("expected head to be preserved")
	}
	if !strings.Contains(got, "[tool_call.error_message truncated:") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	if !strings.HasSuffix(got, strings.Repeat("Z", 1000)) {
		t.Fatalf("expected tail to be preserved")
	}
	if strings.Contains(got, "MIDDLE") {
		t.Fatalf("expected middle content to be omitted")
	}
}

func TestSanitizeErrorMessageLongUTF8DoesNotSplitRune(t *testing.T) {
	value := strings.Repeat("ą", 40000)
	got := SanitizeErrorMessage(value)
	if len(got) > MaxErrorMessageBytes {
		t.Fatalf("expected at most %d bytes, got %d", MaxErrorMessageBytes, len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8")
	}
	if !strings.HasPrefix(got, "ąąą") || !strings.HasSuffix(got, "ąąą") {
		t.Fatalf("expected UTF-8 head and tail to be preserved")
	}
}

func TestSanitizeErrorMessageInvalidUTF8(t *testing.T) {
	value := string([]byte{'o', 'k', 0xff, 'x'})
	got := SanitizeErrorMessage(value)
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8")
	}
	if !strings.Contains(got, replacementRune) {
		t.Fatalf("expected invalid byte replacement, got %q", got)
	}
}
