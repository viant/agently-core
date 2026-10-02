package terminalartifact

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	snapshot "github.com/viant/agently-core/internal/datly/terminalartifact/snapshot/read"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

type Store struct{ Invoker dexec.ComponentInvoker }

var snapshotTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[snapshot.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/terminal-artifacts/snapshot"}}
var cleanupTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[CleanupComponent]().PkgPath(), Name: "TerminalArtifactCleanup"}, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/terminal-artifacts/cleanup"}}

func (s *Store) Snapshot(ctx context.Context, since time.Time, limit int) ([]Candidate, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("terminal turn limit must be positive")
	}
	if limit > 5000 {
		limit = 5000
	}
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("terminal artifact store is not configured")
	}
	input := &snapshot.SnapshotInput{}
	input.SetTerminalSince(since.UTC())
	input.SetTerminalTurnLimit(limit)
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: snapshotTarget, Input: input, Providers: []locator.Provider{provider.Named("terminalartifactaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "internal" {
			return true, true, nil
		}
		return nil, false, nil
	})}})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*snapshot.SnapshotOutput)
	if !ok || output == nil {
		return nil, fmt.Errorf("terminal artifact snapshot returned %T", value)
	}
	result := make([]Candidate, 0, len(output.Data))
	seen := map[string]bool{}
	for _, row := range output.Data {
		if row == nil {
			continue
		}
		key := row.ArtifactKind + "\x00" + row.ArtifactId
		if seen[key] {
			continue
		}
		seen[key] = true
		status := normalized(row.TerminalStatus)
		reason := strings.TrimSpace(row.TerminalError)
		if reason == "" {
			reason = fmt.Sprintf("turn reached terminal status %s", status)
		}
		result = append(result, Candidate{Kind: Kind(row.ArtifactKind), ID: row.ArtifactId, ConversationID: row.ConversationId, TurnID: row.TurnId, Linkage: Linkage(row.LinkMode), ExpectedLink: row.ExpectedLink, ExpectedRun: row.ExpectedRun, TerminalStatus: status, Reason: reason})
	}
	return result, nil
}
func (s *Store) Cleanup(ctx context.Context, candidates []Candidate, completedAt time.Time) ([]Disposition, error) {
	if len(candidates) == 0 {
		return []Disposition{}, nil
	}
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("terminal artifact store is not configured")
	}
	input := &Input{Candidates: append([]Candidate(nil), candidates...), CompletedAt: completedAt.UTC()}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: cleanupTarget, Input: input})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*Output)
	if !ok || output == nil {
		return nil, fmt.Errorf("terminal artifact cleanup returned %T", value)
	}
	if len(output.Dispositions) != len(candidates) {
		return nil, fmt.Errorf("terminal artifact cleanup returned %d dispositions for %d candidates", len(output.Dispositions), len(candidates))
	}
	return output.Dispositions, nil
}
