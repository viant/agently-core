package agent

import (
	"context"
	bundle "github.com/viant/agently-core/protocol/tool/bundle"
	"github.com/viant/agently-core/service/planner"
	bundlerepo "github.com/viant/agently-core/workspace/repository/toolbundle"
)

type HostDocumentReader interface {
	ReadURI(context.Context, string) ([]byte, error)
	ContainsURI(string) bool
	RootURI() string
	LoadAll(context.Context) ([]*bundle.Bundle, error)
}

// SetHostDocuments is startup wiring for existing host defaults. Explicit
// caller-provided bundle/contract providers keep their existing semantics.
func (s *Service) SetHostDocuments(reader HostDocumentReader) {
	if s == nil || reader == nil {
		return
	}
	s.hostDocuments = reader
	repo := bundlerepo.NewWithReader(reader)
	if !s.toolBundlesConfigured {
		s.toolBundles = reader.LoadAll
		s.toolBundleRepo = repo
	}
	if s.intakeSvc != nil {
		s.intakeSvc.SetHostDocuments(reader, repo)
	}
	if contract, ok := s.plannerContracts.(*planner.FileResolver); ok && !s.plannerContractsConfigured {
		contract.SetDocumentReader(reader)
	}
}
