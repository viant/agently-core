// Package metadata exposes Forge's neutral metadata projection over the normal
// tool/MCP adapter. Definition admission stays with the existing host catalog.
package metadata

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
	svc "github.com/viant/agently-core/protocol/tool/service"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	bridge "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/forge/backend/uimetadata"
)

const Name = "ui/metadata"

// DefinitionReader is deliberately limited to metadata. No UI command, OPEN,
// business datasource or writer interface is reachable from this service.
type DefinitionReader interface {
	WindowDefinitionsList(context.Context, *bridge.WindowDefinitionListInput) (*bridge.WindowDefinitionListOutput, error)
	WindowDefinitionGet(context.Context, *bridge.WindowDefinitionGetInput) (*bridge.WindowDefinitionGetOutput, error)
	MetadataScope() bridge.MetadataReadScope
	UsesWindowResourceResolution() bool
}
type Service struct{ reader DefinitionReader }

func New(reader DefinitionReader) *Service { return &Service{reader: reader} }
func (*Service) Name() string              { return Name }

type ListInput struct {
	Query  string `json:"query,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}
type Window struct {
	ID          string `json:"id"`
	Title       string `json:"title,omitempty"`
	ResourceURI string `json:"resourceUri,omitempty"`
}
type ListOutput struct {
	Windows    []Window `json:"windows"`
	HasMore    bool     `json:"hasMore"`
	NextOffset int      `json:"nextOffset,omitempty"`
}
type GetInput struct {
	ID       string                `json:"id,omitempty"`
	Resource *identity.ResourceRef `json:"resource,omitempty"`
	Target   *uimetadata.Target    `json:"target,omitempty"`
	Path     string                `json:"path,omitempty"`
	NodeID   string                `json:"nodeId,omitempty"`
	Cursor   string                `json:"cursor,omitempty"`
	MaxNodes int                   `json:"maxNodes,omitempty"`
}
type GetOutput struct {
	Resource *identity.ResourceRef `json:"resource,omitempty"`
	Target   *uimetadata.Target    `json:"target,omitempty"`
	Metadata *uimetadata.Page      `json:"metadata,omitempty"`
}

func (*Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "list", Description: "Metadata only: discover authorized unopened Forge windows without opening them or fetching business data. Returns a bounded page; use get for tabs/forms/controls. Does not report current UI state or executable authority.", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
		{Name: "get", Description: "Metadata only: describe the authorized selected Forge window definition, including nested tabs, forms, control field types/constraints, declared actions and unloaded option sources. No OPEN, business reads or actions execute. Narrow with a returned path or unambiguous nodeId and follow nextCursor. For every static option or enum choice, follow the control options.path and its paginated option nodes; the inline items are only a bounded preview. Conditional availability is unknown; use ui/context:get for current Read state and registered UI controls for Operate.", Input: reflect.TypeOf(&GetInput{}), Output: reflect.TypeOf(&GetOutput{})},
	}
}
func (s *Service) Method(name string) (svc.Executable, error) {
	switch name {
	case "list":
		return s.list, nil
	case "get":
		return s.get, nil
	}
	return nil, svc.NewMethodNotFoundError(name)
}
func (s *Service) begin(ctx context.Context) (context.Context, func() error, error) {
	if s == nil || s.reader == nil || !s.reader.UsesWindowResourceResolution() {
		return nil, nil, errors.New("authorized window metadata catalog is not configured")
	}
	if ctx == nil || strings.TrimSpace(requestctx.ConversationIDFromContext(ctx)) == "" {
		return nil, nil, errors.New("current conversation is required for UI metadata")
	}
	return bridge.BeginMetadataReadScope(ctx, s.reader.MetadataScope())
}
func (s *Service) list(ctx context.Context, in, out any) error {
	input, ok := in.(*ListInput)
	if !ok || input == nil {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ListOutput)
	if !ok || output == nil {
		return svc.NewInvalidOutputError(out)
	}
	*output = ListOutput{}
	if input.Limit < 0 || input.Limit > 100 || input.Offset < 0 || input.Offset > 100000 || len(input.Query) > 240 {
		return errors.New("invalid metadata list bounds")
	}
	limit := input.Limit
	if limit == 0 {
		limit = 25
	}
	readCtx, finish, err := s.begin(ctx)
	if err != nil {
		return err
	}
	result, readErr := s.reader.WindowDefinitionsList(readCtx, &bridge.WindowDefinitionListInput{Query: input.Query, Limit: limit, Offset: input.Offset})
	if err = bridge.FinishMetadataReadScope(finish, readErr); err != nil {
		return err
	}
	projected := ListOutput{Windows: []Window{}}
	if result != nil {
		projected.HasMore = result.HasMore
		for index, item := range result.Windows {
			if index >= limit {
				projected.HasMore = true
				break
			}
			projected.Windows = append(projected.Windows, Window{ID: bounded(item.WindowID), Title: bounded(item.Title), ResourceURI: bounded(item.ResourceURI)})
		}
		if projected.HasMore {
			projected.NextOffset = input.Offset + limit
		}
	}
	*output = projected
	return nil
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func (s *Service) get(ctx context.Context, in, out any) error {
	input, ok := in.(*GetInput)
	if !ok || input == nil {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*GetOutput)
	if !ok || output == nil {
		return svc.NewInvalidOutputError(out)
	}
	*output = GetOutput{}
	key := input.ID
	if input.Resource != nil {
		uri, err := identity.ParseResourceURI(input.Resource.URI)
		if err != nil || uri.Kind != "window" {
			return errors.New("invalid window resource")
		}
		if key == "" {
			key = input.Resource.URI
		}
	}
	if key == "" {
		return errors.New("window id or resource is required")
	}
	if strings.Contains(key, "://") {
		uri, err := identity.ParseResourceURI(key)
		if err != nil || uri.Kind != "window" {
			return errors.New("invalid window identity")
		}
	} else if !safeID.MatchString(key) || strings.Contains(key, "..") {
		return errors.New("invalid window identity")
	}
	target := input.Target.WindowTarget()
	if target != nil {
		normalized, targetErr := target.Normalize()
		if targetErr != nil {
			return errors.New("invalid metadata target")
		}
		target = &normalized
	}
	readCtx, finish, err := s.begin(ctx)
	if err != nil {
		return err
	}
	definition, readErr := s.reader.WindowDefinitionGet(readCtx, &bridge.WindowDefinitionGetInput{WindowID: key, Resource: input.Resource, Target: target})
	var projected GetOutput
	if readErr == nil {
		if definition == nil || definition.Definition == nil || definition.Definition.Resource == nil {
			readErr = errors.New("authorized selected definition is unavailable")
		} else {
			selected := definition.Definition
			projected.Metadata, readErr = uimetadata.Project(selected, uimetadata.Query{Path: input.Path, ID: input.NodeID, Cursor: input.Cursor, MaxNodes: input.MaxNodes})
			if readErr == nil {
				pin := selected.Resource
				projected.Resource = &identity.ResourceRef{URI: pin.URI, Revision: pin.Revision}
				projected.Target = uimetadata.PublicTarget(selected.ResourceTarget)
			}
		}
	}
	if err = bridge.FinishMetadataReadScope(finish, readErr); err != nil {
		return err
	}
	*output = projected
	return nil
}
func bounded(value string) string {
	runes := []rune(value)
	if len(runes) > 240 {
		return string(runes[:240]) + "…"
	}
	return value
}
