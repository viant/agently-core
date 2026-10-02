package data

import (
	"context"
	"encoding/json"
	"fmt"

	authctx "github.com/viant/agently-core/internal/auth"
	read "github.com/viant/agently-core/internal/datly/runsteps/read"
	store "github.com/viant/agently-core/internal/store/runsteps"
	runmodel "github.com/viant/agently-core/model/run"
	"github.com/viant/xdatly/state"
)

func nativeRunStepsPageInput(input *runmodel.RunStepsInput) *read.RunStepsInput {
	query := &read.RunStepsInput{}
	if input == nil || input.Has == nil {
		return query
	}
	h := input.Has
	if h.RunID {
		query.SetRunID(input.RunID)
	}
	if h.Iteration {
		query.SetIteration(input.Iteration)
	}
	if h.StepTypes {
		query.SetStepTypes(input.StepTypes)
	}
	if h.CursorBefore {
		query.SetCursorBefore(input.CursorBefore)
	}
	if h.CursorAfter {
		query.SetCursorAfter(input.CursorAfter)
	}
	return query
}

func (s *datlyService) queryRunStepsNative(ctx context.Context, input *runmodel.RunStepsInput, limit int, opts *options) ([]*runmodel.RunStepsView, error) {
	selectors := state.Selectors{&state.NamedSelector{Name: "RunSteps", Selector: state.Selector{Limit: limit + 1}}}
	if opts != nil {
		selectors = append(selectors, nativeSelectors(opts.selectors)...)
	}
	component := &store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
	rows, err := component.ListTrusted(ctx, nativeRunStepsPageInput(input), selectors)
	if err != nil {
		return nil, err
	}
	result := make([]*runmodel.RunStepsView, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, fmt.Errorf("encode native run step: %w", err)
		}
		var mapped runmodel.RunStepsView
		if err := json.Unmarshal(encoded, &mapped); err != nil {
			return nil, fmt.Errorf("decode run step contract: %w", err)
		}
		result = append(result, &mapped)
	}
	return result, nil
}
