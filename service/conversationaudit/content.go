package conversationaudit

import (
	"context"
	"sort"

	projection "github.com/viant/agently-core/internal/datly/conversationaudit/read"
)

// readPageContent reauthorizes a bounded set of IDs in one reader invocation.
// It does not load the lookahead row or call Detail once per turn.
func (s *Service) readPageContent(ctx context.Context, input *projection.Input, selected []*projection.Row) ([]*projection.Row, error) {
	i := *input
	i.Detail, i.Limit = true, len(selected)
	i.TurnIDs = make([]string, len(selected))
	i.Has = &projection.InputHas{TurnIDs: true}
	for n, row := range selected {
		i.TurnIDs[n] = row.Id
	}
	rows, err := s.reader.Read(ctx, &i)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*projection.Row, len(rows))
	for _, row := range rows {
		if row == nil || byID[row.Id] != nil {
			return nil, ErrNotFound
		}
		byID[row.Id] = row
	}
	result := make([]*projection.Row, len(selected))
	for n, original := range selected {
		row := byID[original.Id]
		if row == nil || row.ConversationId != original.ConversationId || row.OwnerID != original.OwnerID {
			return nil, ErrNotFound
		}
		result[n] = row
	}
	return result, nil
}

func savedModels(row *projection.Row) []string {
	unique := make(map[string]bool)
	for _, message := range row.Message {
		if message.ModelCall != nil {
			call := message.ModelCall
			unique[call.Provider+"/"+call.Model] = true
		}
	}
	models := make([]string, 0, len(unique))
	for model := range unique {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}
