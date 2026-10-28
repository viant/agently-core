package cohort

import (
	"context"
	"errors"
	"testing"

	forgetypes "github.com/viant/forge/backend/types"
)

type admissionSource struct {
	err          error
	admissions   int
	reads        int
	requestedURI string
}

func (*admissionSource) Sync(context.Context) error { return nil }
func (s *admissionSource) EnsureAdmitted(context.Context) error {
	s.admissions++
	return s.err
}
func (s *admissionSource) OpenWindowVariant(_ context.Context, uri string, _ *forgetypes.WindowTarget) (*forgetypes.WindowResourceVariant, string, error) {
	s.reads++
	s.requestedURI = uri
	return &forgetypes.WindowResourceVariant{}, "revision", nil
}

func TestWindowSourcePreservesProducerAdmission(t *testing.T) {
	denied := errors.New("producer admission unavailable")
	source := &admissionSource{err: denied}
	reader := (Binding{Source: source}).WindowSource()
	if _, _, err := reader.OpenWindowVariant(context.Background(), "window://catalog/example", nil); !errors.Is(err, denied) {
		t.Fatalf("expected admission error, got %v", err)
	}
	if source.reads != 0 || source.admissions != 1 {
		t.Fatal("read occurred without producer admission")
	}
	source.err = nil
	window, revision, err := reader.OpenWindowVariant(context.Background(), "window://catalog/example", nil)
	if err != nil || window == nil || revision != "revision" || source.reads != 1 || source.admissions != 2 || source.requestedURI != "window://catalog/example" {
		t.Fatalf("admitted read changed: %v, %q", err, revision)
	}
}
