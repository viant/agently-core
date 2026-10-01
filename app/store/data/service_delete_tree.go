package data

import (
	"context"
	"github.com/viant/agently-core/internal/sqlitewrite"
	"github.com/viant/agently-core/internal/store/conversationtree"
	"strings"
	"time"
)

func (s *datlyService) DeleteConversationTree(ctx context.Context, ids ...string) (retErr error) {
	ids = normalizeDeleteIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	ctx, diagnostics := beginConversationDeleteDiagnostics(ctx, ids)
	if diagnostics != nil {
		defer func() {
			diagnostics.finish(retErr)
		}()
	}
	gateStarted := conversationDeleteDiagPhaseStart(ctx, "write_gate_wait")
	gateAcquired := false
	_, retErr = sqlitewrite.Do(ctx, s.writeGate, func() (struct{}, error) {
		gateAcquired = true
		conversationDeleteDiagPhaseDone(ctx, "write_gate_wait", gateStarted, nil, "")
		now := time.Now().UTC()
		started := conversationDeleteDiagPhaseStart(ctx, "managed_delete")
		err := mapScheduleDeleteError(conversationtree.InvokeDelete(ctx, s.native, &conversationtree.DeleteInput{RootIDs: ids, Now: &now}))
		conversationDeleteDiagPhaseDone(ctx, "managed_delete", started, err, "")
		return struct{}{}, err
	})
	if !gateAcquired {
		conversationDeleteDiagPhaseDone(ctx, "write_gate_wait", gateStarted, retErr, "")
	}
	return retErr
}

func normalizeDeleteIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func normalizeStatus(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
