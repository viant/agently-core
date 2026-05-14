package control

import "testing"

func TestServiceMethod_SetValueRegistered(t *testing.T) {
	svc := &Service{}
	method, err := svc.Method("setValue")
	if err != nil {
		t.Fatalf("expected setValue method to resolve, got %v", err)
	}
	if method == nil {
		t.Fatalf("expected setValue method implementation")
	}
}

func TestNormalizeOptionalClientID(t *testing.T) {
	if got := normalizeOptionalClientID(" default "); got != "" {
		t.Fatalf("expected default client id to normalize empty, got %q", got)
	}
	if got := normalizeOptionalClientID("client-1"); got != "client-1" {
		t.Fatalf("expected client id to survive, got %q", got)
	}
}
