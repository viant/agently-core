package expose

import (
	"context"
	"github.com/viant/jsonrpc"
	schema "github.com/viant/mcp-protocol/schema"
)

func (h *ToolHandler) ListSkills(ctx context.Context, req *jsonrpc.TypedRequest[*schema.ListSkillsRequest]) (*schema.ListSkillsResult, *jsonrpc.Error) {
	p, ok := h.exec.(SkillProvider)
	if !ok {
		return nil, jsonrpc.NewMethodNotFound("skills/list unavailable", nil)
	}
	var cursor *string
	if req != nil && req.Request != nil {
		cursor = req.Request.Params.Cursor
	}
	out, err := p.ListSkills(ctx, cursor)
	if err != nil {
		return nil, jsonrpc.NewInvalidParamsError("skills unavailable", nil)
	}
	return out, nil
}
func (h *ToolHandler) GetSkill(ctx context.Context, req *jsonrpc.TypedRequest[*schema.GetSkillRequest]) (*schema.GetSkillResult, *jsonrpc.Error) {
	p, ok := h.exec.(SkillProvider)
	if !ok {
		return nil, jsonrpc.NewMethodNotFound("skills/get unavailable", nil)
	}
	if req == nil || req.Request == nil || req.Request.Params.Uri == "" {
		return nil, jsonrpc.NewInvalidParamsError("skill URI required", nil)
	}
	out, err := p.GetSkill(ctx, req.Request.Params.Uri)
	if err != nil {
		return nil, jsonrpc.NewInvalidParamsError("skill unavailable", nil)
	}
	return out, nil
}
