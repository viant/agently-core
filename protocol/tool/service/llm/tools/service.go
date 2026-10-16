package tools

import (
	"context"
	"fmt"
	"reflect"

	"github.com/viant/agently-core/protocol/primitive"
	svc "github.com/viant/agently-core/protocol/tool/service"
	"github.com/viant/agently-core/runtime/toolselection"
)

const Name = "llm/tools"

type Service struct{}

func New() *Service           { return &Service{} }
func (*Service) Name() string { return Name }
func (*Service) Methods() svc.Signatures {
	return svc.Signatures{
		{Name: "list", Description: "Find authorized tool names and descriptions using optional namespace, nameTerms, descriptionTerms, or keywords. Matching is literal case-insensitive contains: terms within each group are OR; groups are AND. Every tool on the returned page is selected automatically and its schema is available at the next model step; no separate activation is needed. Use limit and nextCursor with unchanged filters. Legacy query and offset remain supported. List never returns schemas or executes tools.", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
		{Name: "activate", Description: "Expose schemas for exact authorized tool names on the next model iteration in this turn. Does not execute tools or grant approval.", Input: reflect.TypeOf(&ActivateInput{}), Output: reflect.TypeOf(&ActivateOutput{})},
	}
}
func (s *Service) Method(name string) (svc.Executable, error) {
	switch name {
	case "list":
		return s.list, nil
	case "activate":
		return s.activate, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

type ListInput struct {
	primitive.CatalogQuery
	Query  string `json:"query,omitempty"`
	Offset int    `json:"offset,omitempty"`
	// Keep Limit explicit for existing Go callers' keyed struct literals.
	Limit int `json:"limit,omitempty"`
}
type ListOutput struct {
	Tools      []toolselection.Candidate `json:"tools"`
	HasMore    bool                      `json:"hasMore"`
	NextOffset int                       `json:"nextOffset,omitempty"`
	NextCursor string                    `json:"nextCursor,omitempty"`
	Complete   bool                      `json:"complete"`
	Total      int                       `json:"total"`
}
type ActivateInput struct {
	Names []string `json:"names"`
}
type ActivateOutput struct {
	Names []string `json:"names"`
}

func (*Service) list(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ListInput)
	if !ok || input == nil {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ListOutput)
	if !ok || output == nil {
		return svc.NewInvalidOutputError(out)
	}
	state, ok := toolselection.FromContext(ctx)
	if !ok {
		return fmt.Errorf("tool selection is unavailable for this turn")
	}
	if len(input.Query) > 256 {
		return fmt.Errorf("invalid tool discovery bounds")
	}
	query := input.CatalogQuery
	if input.Limit != 0 {
		query.Limit = input.Limit
	}
	if input.Query != "" {
		query.Keywords = append(append([]string(nil), query.Keywords...), input.Query)
	}
	page, err := state.ListPage(ctx, query, input.Offset)
	if err != nil {
		return err
	}
	if len(page.Items) > 0 {
		names := make([]string, 0, len(page.Items))
		for _, item := range page.Items {
			names = append(names, item.Name)
		}
		if err := state.Activate(names); err != nil {
			return err
		}
	}
	*output = ListOutput{Tools: page.Items, HasMore: !page.Complete, NextCursor: page.NextCursor, Complete: page.Complete, Total: page.Total}
	if !page.Complete && query.Cursor == "" {
		output.NextOffset = input.Offset + len(page.Items)
	}
	return nil
}
func (*Service) activate(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ActivateInput)
	if !ok || input == nil {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ActivateOutput)
	if !ok || output == nil {
		return svc.NewInvalidOutputError(out)
	}
	state, ok := toolselection.FromContext(ctx)
	if !ok {
		return fmt.Errorf("tool selection is unavailable for this turn")
	}
	if err := state.Activate(input.Names); err != nil {
		return err
	}
	output.Names = append([]string(nil), input.Names...)
	return nil
}
