package patch

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// Name of the system/patch action service.
const Name = "system/patch"

// Service exposes filesystem patching capabilities as a Fluxor action service.
// Apply calls stage changes in a conversation-scoped session until host commit or rollback.
type Service struct {
	mu       sync.Mutex
	sessions map[string]*Session // conversationID -> session
}

// New creates the patch service instance.
func New() *Service { return &Service{sessions: map[string]*Session{}} }

// Name returns service identifier.
func (s *Service) Name() string { return Name }

// CacheableMethods declares which methods produce cacheable outputs.
func (s *Service) CacheableMethods() map[string]bool {
	return map[string]bool{
		"diff":     true,
		"snapshot": true,
	}
}

// Methods returns service method catalogue.
func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{
			Name:        "apply",
			Description: "Stage a patch in the active patch session for host review. Requires workdir. Supports unified diff or simplified patch. Patch paths must resolve inside workdir.",
			Input:       reflect.TypeOf(&ApplyInput{}),
			Output:      reflect.TypeOf(&ApplyOutput{}),
		},
		{
			Name:        "replace",
			Description: "Stage an exact string replacement in the active patch session for host review. Requires workdir. Path must resolve inside workdir. The old text must match exactly; ambiguous replacements fail unless replaceAll is true.",
			Input:       reflect.TypeOf(&ReplaceInput{}),
			Output:      reflect.TypeOf(&ReplaceOutput{}),
		},
		{
			Name:        "diff",
			Description: "Compute a patch between old and new content. Optional path and contextLines.",
			Input:       reflect.TypeOf(&DiffInput{}),
			Output:      reflect.TypeOf(&DiffOutput{}),
		},
		{
			Name:        "snapshot",
			Description: "List uncommitted changes captured by the current patch session. Each change includes resolved file locations in origUrl/url and an optional diff. If there are no uncommitted changes, this tool returns {\"status\":\"noFound\"} — treat that as an empty result (equivalent to changes: []). This is not an error. Do not retry system_patch-snapshot after receiving {\"status\":\"noFound\"}; proceed to the next step.",
			Input:       reflect.TypeOf(&EmptyInput{}),
			Output:      reflect.TypeOf(&SnapshotOutput{}),
		},
		{
			Name:        "commit",
			Description: "Persist current patch session changes and clear the session.",
			Input:       reflect.TypeOf(&EmptyInput{}),
			Output:      reflect.TypeOf(&EmptyOutput{}),
		},
		{
			Name:        "rollback",
			Description: "Discard current patch session changes and clear the session.",
			Input:       reflect.TypeOf(&EmptyInput{}),
			Output:      reflect.TypeOf(&EmptyOutput{}),
		},
	}
}

// Method maps method names to executable handlers.
func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(name) {
	case "apply":
		return s.apply, nil
	case "replace":
		return s.replace, nil
	case "diff":
		return s.diff, nil
	case "snapshot":
		return s.snapshot, nil
	case "commit":
		return s.commit, nil
	case "rollback":
		return s.rollback, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

// -------------------------------------------------------------------------
// method executors
// -------------------------------------------------------------------------

func (s *Service) apply(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ApplyInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ApplyOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	output.Status = "ok"
	err := s.applyPatch(ctx, input, output)
	if err != nil {
		output.Error = err.Error()
		output.Status = "error"
	}
	return nil
}

func (s *Service) applyPatch(ctx context.Context, input *ApplyInput, output *ApplyOutput) error {
	// Enforce workdir to avoid accidental writes outside the intended workspace.
	// This prevents creating directories like "/internal" when workdir is missing.
	if strings.TrimSpace(input.Workdir) == "" {
		return fmt.Errorf("workdir is required for system/patch:apply")
	}
	sess, err := s.sessionForContext(ctx)
	if err != nil {
		return err
	}

	if err := sess.ApplyPatch(ctx, input.Patch, input.Workdir); err != nil {
		// Do not auto-rollback; leave the session active so the caller
		// can inspect snapshot and explicitly decide to commit or rollback.
		// Bubble up the error to the caller.
		return err
	}

	// Compute basic stats for user feedback.
	output.Stats = patchStats(input.Patch)
	// Session remains open for further apply calls until commit/rollback.
	return nil
}

func (s *Service) replace(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ReplaceInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ReplaceOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	output.Status = "ok"
	err := s.replaceText(ctx, input, output)
	if err != nil {
		output.Error = err.Error()
		output.Status = "error"
	}
	return nil
}

func (s *Service) replaceText(ctx context.Context, input *ReplaceInput, output *ReplaceOutput) error {
	if strings.TrimSpace(input.Workdir) == "" {
		return fmt.Errorf("workdir is required for system/patch:replace")
	}
	if strings.TrimSpace(input.Path) == "" {
		return fmt.Errorf("path is required for system/patch:replace")
	}
	path, err := resolvePath(input.Path, input.Workdir)
	if err != nil {
		return err
	}
	sess, err := s.sessionForContext(ctx)
	if err != nil {
		return err
	}
	replacements, stats, err := sess.Replace(ctx, path, input.Old, input.New, input.ReplaceAll, input.ExpectedOccurrences)
	if err != nil {
		return err
	}
	output.Path = path
	output.Replacements = replacements
	output.Stats = stats
	return nil
}

func (s *Service) sessionForContext(ctx context.Context) (*Session, error) {
	convID := runtimerequestctx.ConversationIDFromContext(ctx)
	if convID == "" {
		convID = "_global"
	}
	s.mu.Lock()
	sess := s.sessions[convID]
	if sess == nil {
		var err error
		sess, err = NewSessionFor(convID)
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		s.sessions[convID] = sess
	}
	s.mu.Unlock()
	return sess, nil
}

// commit finalises the active session and clears it.
func (s *Service) commit(ctx context.Context, in, out interface{}) error {
	if _, ok := in.(*EmptyInput); !ok {
		return svc.NewInvalidInputError(in)
	}
	if _, ok := out.(*EmptyOutput); !ok {
		return svc.NewInvalidOutputError(out)
	}

	convID := runtimerequestctx.ConversationIDFromContext(ctx)
	if convID == "" {
		convID = "_global"
	}
	s.mu.Lock()
	sess := s.sessions[convID]
	s.mu.Unlock()
	if sess == nil {
		return nil
	}
	err := sess.Commit(ctx)
	s.mu.Lock()
	delete(s.sessions, convID)
	s.mu.Unlock()
	return err
}

// rollback aborts the active session and clears it.
func (s *Service) rollback(ctx context.Context, in, out interface{}) error {
	if _, ok := in.(*EmptyInput); !ok {
		return svc.NewInvalidInputError(in)
	}
	if _, ok := out.(*EmptyOutput); !ok {
		return svc.NewInvalidOutputError(out)
	}

	convID := runtimerequestctx.ConversationIDFromContext(ctx)
	if convID == "" {
		convID = "_global"
	}
	s.mu.Lock()
	sess := s.sessions[convID]
	s.mu.Unlock()
	if sess == nil {
		return nil
	}
	err := sess.Rollback(ctx)
	s.mu.Lock()
	delete(s.sessions, convID)
	s.mu.Unlock()
	return err
}

func (s *Service) diff(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*DiffInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*DiffOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}

	// Context is not used in GenerateDiff, but we're keeping it for consistency
	res, stats, err := GenerateDiff([]byte(input.OldContent), []byte(input.NewContent), input.Path, input.ContextLines)
	if err != nil {
		return err
	}
	output.Patch = res
	output.Stats = stats
	return nil
}

// snapshot returns the list of changes tracked by the active session without mutating it.
func (s *Service) snapshot(ctx context.Context, in, out interface{}) error {
	if _, ok := in.(*EmptyInput); !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*SnapshotOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}

	convID := runtimerequestctx.ConversationIDFromContext(ctx)
	if convID == "" {
		convID = "_global"
	}
	s.mu.Lock()
	sess := s.sessions[convID]
	s.mu.Unlock()

	output.Status = "ok"
	if sess == nil {
		output.Changes = nil
		output.Status = "noFound"
		return nil
	}

	changes, err := sess.Snapshot(ctx)
	if err != nil {
		output.Status = "error"
		output.Error = err.Error()
	}
	if len(changes) == 0 {
		output.Status = "noFound"
	}
	output.Changes = changes
	return nil
}

// patchStats extracts basic statistics from a unified-diff string.
func patchStats(p string) DiffStats {
	stats := DiffStats{}
	for _, l := range strings.Split(p, "\n") {
		switch {
		case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
			stats.Added++
		case strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---"):
			stats.Removed++
		}
	}
	return stats
}
