package sdk

import (
	"context"
	"encoding/json"
)

func listTemplates(c Client, ctx context.Context, input *ListTemplatesInput) (*ListTemplatesOutput, error) {
	args := map[string]interface{}{}
	raw, err := c.ExecuteTool(ctx, "template:list", args)
	if err != nil {
		return nil, err
	}
	out := &ListTemplatesOutput{}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return nil, err
	}
	if out.Items == nil {
		out.Items = []TemplateListItem{}
	}
	return out, nil
}

func getTemplate(c Client, ctx context.Context, input *GetTemplateInput) (*GetTemplateOutput, error) {
	args := map[string]interface{}{}
	if input != nil {
		if input.Name != "" {
			args["name"] = input.Name
		}
		if input.IncludeDocument != nil {
			args["includeDocument"] = *input.IncludeDocument
		}
	}
	raw, err := c.ExecuteTool(ctx, "template:get", args)
	if err != nil {
		return nil, err
	}
	out := &GetTemplateOutput{}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return nil, err
	}
	return out, nil
}
