package skill

import (
	"context"
	"fmt"
	svc "github.com/viant/agently-core/protocol/tool/service"
	"strings"
)

type GetInput struct {
	Name   string `json:"name,omitempty"`
	URI    string `json:"uri,omitempty"`
	Server string `json:"server,omitempty"`
	Path   string `json:"path,omitempty"`
}
type GetOutput struct {
	Files          []string `json:"files,omitempty"`
	Blob           string   `json:"blob,omitempty"`
	MimeType       string   `json:"mimeType,omitempty"`
	RequestedTools string   `json:"requestedTools,omitempty"`
	Name           string   `json:"name"`
	URI            string   `json:"uri,omitempty"`
	Body           string   `json:"body"`
	AllowedTools   string   `json:"allowedTools,omitempty"`
}

func (s *Service) get(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*GetInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*GetOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	agent, err := s.currentAgent(ctx)
	if err != nil {
		return err
	}
	name, err := normalizeSkillInput(input.Name, input.URI, input.Server)
	if err != nil {
		return err
	}
	item, err := s.GetVisible(ctx, agent, name)
	if err != nil {
		return err
	}
	*output = GetOutput{Name: item.Frontmatter.Name, URI: item.CatalogURI, Body: item.Body, AllowedTools: item.Frontmatter.AllowedTools, RequestedTools: item.RequestedTools}
	if item.Manifest != nil {
		for _, f := range item.Manifest.Resources.Files {
			output.Files = append(output.Files, strings.TrimPrefix(f.Uri, strings.TrimSuffix(item.RemoteURI, "SKILL.md")))
		}
	}
	if input.Path != "" {
		if item.Manifest == nil {
			return fmt.Errorf("supporting-file get currently requires an MCP skill")
		}
		text, blob, mime, err := s.readSupporting(ctx, item, input.Path)
		if err != nil {
			return err
		}
		output.Body = text
		output.Blob = blob
		output.MimeType = mime
	}
	return nil
}
