package auth

import (
	"context"
	"encoding/json"
	"fmt"
	iauth "github.com/viant/agently-core/internal/auth"
	svc "github.com/viant/agently-core/protocol/tool/service"
	"reflect"
)

type WorkspaceTools struct {
	runtime              *Runtime
	SetBrowserVisibility func(context.Context, *WorkspaceMutation) (*WorkspaceMutationReceipt, error)
}
type WorkspaceReadRequest struct{}

// WorkspaceDefaultChange records the mapping before and after a committed edit.
type WorkspaceDefaultChange struct {
	Previous *WorkspaceReportDefault `json:"previous,omitempty"`
	Current  *WorkspaceReportDefault `json:"current,omitempty"`
}

// WorkspaceMutationReceipt acknowledges one committed presentation edit without
// sending the user's complete menu back into every model iteration.
type WorkspaceMutationReceipt struct {
	DefaultChange *WorkspaceDefaultChange `json:"defaultChange,omitempty"`
	Storage       string                  `json:"storage,omitempty"`
	Hidden        *bool                   `json:"hidden,omitempty"`
	Title         string                  `json:"title,omitempty"`
	Presentation  string                  `json:"presentation,omitempty"`
	Status        string                  `json:"status"`
	Action        string                  `json:"action"`
	GroupID       string                  `json:"groupId,omitempty"`
	ItemID        string                  `json:"itemId,omitempty"`
	Enabled       *bool                   `json:"enabled,omitempty"`
}

func NewWorkspaceTools(runtime *Runtime) *WorkspaceTools { return &WorkspaceTools{runtime: runtime} }
func (*WorkspaceTools) Name() string                     { return "my_workspace" }
func (*WorkspaceTools) Methods() svc.Signatures {
	return svc.Signatures{
		{Name: "get", Description: "Read the current user's personal My workspace groups, exact window/report pins and navigation visibility. Presentation preferences only; this does not discover tools or grant access.", Input: reflect.TypeOf(&WorkspaceReadRequest{}), Output: reflect.TypeOf(&WorkspacePreferences{})},
		{Name: "update", Description: "Apply one requested My workspace navigation edit: pin an existing window or report by exact provider and URI, unpin, create/rename/remove/reorder groups, move/rename items, show/hide My workspace, set_presentation to menu/tiles/both, set_title, rename_application, rename_menu, or show/hide an application/menu in the active browser only. Browser visibility requires a signed-in browser attached to this conversation; it never writes server visibility preferences. Labels and visibility are personal presentation only. Hiding only changes navigation; LLM tools and data permissions remain available. Pinning never clones or runs a report. Use stable item/group IDs; do not invent resource URIs. Explicit set_default/clear_default controls the preferred personal copy for unqualified conversational report requests; exact URIs, menu pins and open windows never use this substitution. First pin enables an unset menu preference; explicit disable is preserved.", Input: reflect.TypeOf(&WorkspaceMutation{}), Output: reflect.TypeOf(&WorkspaceMutationReceipt{})},
	}
}
func (s *WorkspaceTools) Method(name string) (svc.Executable, error) {
	switch name {
	case "get":
		return func(ctx context.Context, in, out interface{}) error {
			if _, ok := in.(*WorkspaceReadRequest); !ok {
				return svc.NewInvalidInputError(in)
			}
			output, ok := out.(*WorkspacePreferences)
			if !ok {
				return svc.NewInvalidOutputError(out)
			}
			prefs, err := s.runtime.workspacePreferences(ctx)
			if err != nil {
				return err
			}
			*output = *prefs
			return nil
		}, nil
	case "update":
		return func(ctx context.Context, in, out interface{}) error {
			mutation, ok := in.(*WorkspaceMutation)
			if !ok {
				return svc.NewInvalidInputError(in)
			}
			output, ok := out.(*WorkspaceMutationReceipt)
			if !ok {
				return svc.NewInvalidOutputError(out)
			}
			user := iauth.User(ctx)
			if user == nil || s.runtime == nil || s.runtime.ext == nil || s.runtime.ext.users == nil {
				return fmt.Errorf("authenticated user preferences are unavailable")
			}
			if mutation.Action == "set_application_visibility" || mutation.Action == "set_menu_visibility" {
				if _, err := applyWorkspaceMutation(nil, mutation); err != nil {
					return err
				}
				if s.SetBrowserVisibility == nil {
					return fmt.Errorf("browser workspace visibility is unavailable; open the workspace and retry")
				}
				receipt, err := s.SetBrowserVisibility(ctx, mutation)
				if err != nil {
					return err
				}
				if receipt == nil || receipt.Status != "saved" || receipt.Storage != "browser" || receipt.Action != mutation.Action || receipt.Hidden == nil || *receipt.Hidden != *mutation.Hidden {
					return fmt.Errorf("browser did not confirm the visibility change")
				}
				*output = *receipt
				return nil
			}
			patch := &PreferencesPatch{WorkspaceAction: mutation}
			if err := s.runtime.ext.users.UpdatePreferences(ctx, firstNonEmpty(iauth.CanonicalUserID(ctx), user.Subject), patch); err != nil {
				return err
			}
			prefs := patch.committedWorkspace
			if prefs == nil {
				// A custom UserService may only implement the public write contract.
				var err error
				prefs, err = s.runtime.workspacePreferences(ctx)
				if err != nil {
					return fmt.Errorf("workspace edit accepted; preference readback failed: %w", err)
				}
			}
			*output = workspaceMutationReceipt(mutation, prefs, patch.committedDefaultChange)
			return nil
		}, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

func workspaceMutationReceipt(mutation *WorkspaceMutation, prefs *WorkspacePreferences, change *WorkspaceDefaultChange) WorkspaceMutationReceipt {
	receipt := WorkspaceMutationReceipt{Status: "saved", Action: mutation.Action, GroupID: mutation.GroupID, ItemID: mutation.ItemID,
		Enabled: prefs.Enabled, Presentation: prefs.Presentation, Title: prefs.Title, DefaultChange: change}
	if change != nil && (change.Previous == nil && change.Current == nil ||
		change.Previous != nil && change.Current != nil && *change.Previous == *change.Current) {
		receipt.Status = "unchanged"
	}
	if mutation.Action == "pin" && mutation.Pin != nil {
		groupID := mutation.GroupID
		if groupID == "" {
			groupID = "default"
		}
		for _, group := range prefs.Groups {
			if group.ID != groupID {
				continue
			}
			for _, item := range group.Items {
				if samePin(item, *mutation.Pin) {
					receipt.GroupID, receipt.ItemID = group.ID, item.ID
					return receipt
				}
			}
			break
		}
	}
	return receipt
}

func (r *Runtime) workspacePreferences(ctx context.Context) (*WorkspacePreferences, error) {
	user := iauth.User(ctx)
	if user == nil || r == nil || r.ext == nil || r.ext.users == nil {
		return nil, fmt.Errorf("authenticated user preferences are unavailable")
	}
	preferences, err := readPreferences(ctx, r.ext.users, firstNonEmpty(iauth.CanonicalUserID(ctx), user.Subject))
	if err != nil {
		return nil, err
	}
	result := &WorkspacePreferences{}
	if value := preferences["workspace"]; value != nil {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, result); err != nil {
			return nil, err
		}
	}
	return result, result.validate()
}

// WorkspacePreferences returns only the current authenticated user's document.
func (r *Runtime) WorkspacePreferences(ctx context.Context) (*WorkspacePreferences, error) {
	return r.workspacePreferences(ctx)
}

// SetWorkspaceDefaultVerifier binds validation to the preference writer shared
// by HTTP and tools, not to a single transport entry point.
func (r *Runtime) SetWorkspaceDefaultVerifier(verify func(context.Context, WorkspaceReportDefault) error) error {
	if r == nil || r.ext == nil || r.ext.users == nil {
		return fmt.Errorf("user preference writer unavailable")
	}
	writer, ok := r.ext.users.(interface {
		SetWorkspaceDefaultVerifier(func(context.Context, WorkspaceReportDefault) error)
	})
	if !ok {
		return fmt.Errorf("user preference writer does not support report default verification")
	}
	writer.SetWorkspaceDefaultVerifier(verify)
	return nil
}
