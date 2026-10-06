package scheduler

import (
	"fmt"
	dexec "github.com/viant/datly/exec"
)

// ForGoalCommand creates an invocation-local view for goal wakeup reads and
// cancellation. Its writes use the root's scoped native component invoker.
// It starts no ticker, execution goroutine, or independent transaction.
func (s *Service) ForGoalCommand(invoker dexec.ComponentInvoker) (*Service, error) {
	if s == nil {
		return nil, nil
	}
	original, ok := s.store.(*datlyStore)
	if !ok || original.native == nil || invoker == nil {
		return nil, fmt.Errorf("atomic goal commands require a native Datly scheduler store")
	}
	store := &datlyStore{native: invoker, data: original.data}
	return New(store, nil), nil
}

func (s *Service) DatlyInvoker() dexec.ComponentInvoker {
	if s == nil {
		return nil
	}
	store, ok := s.store.(*datlyStore)
	if !ok {
		return nil
	}
	return store.native
}
