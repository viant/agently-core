package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	identity "github.com/viant/agently-core/protocol/resource"
	"strings"
	"time"

	"github.com/google/uuid"
	iauth "github.com/viant/agently-core/internal/auth"
	userread "github.com/viant/agently-core/internal/datly/user/read"
	userwrite "github.com/viant/agently-core/internal/datly/user/write"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	xhandler "github.com/viant/xdatly/handler"
	"reflect"
)

type DatlyUserService struct {
	invoker             dexec.ComponentInvoker
	verifyReportDefault func(context.Context, WorkspaceReportDefault) error
}

func NewDatlyUserService(invoker dexec.ComponentInvoker) *DatlyUserService {
	if invoker == nil {
		return nil
	}
	return &DatlyUserService{invoker: invoker}
}

var userReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[userread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/user"},
}

var userWriterTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[userwrite.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/user"},
}

func (s *DatlyUserService) GetByUsername(ctx context.Context, username string) (*User, error) {
	if s == nil || s.invoker == nil {
		return nil, errPreferencesUnavailable
	}
	if strings.TrimSpace(username) == "" {
		return nil, nil
	}
	// The auth runtime has already resolved this identity to users.id. Do not
	// reinterpret it as a username: another account may use that string as its
	// display login, and resolving it again would also add an unnecessary read.
	if canonical := iauth.CanonicalUserID(ctx); canonical != "" && username == canonical {
		return s.lookupByID(ctx, canonical)
	}
	if user, err := s.lookupByUsername(ctx, username); err != nil || user != nil {
		return user, err
	}
	return s.lookupByID(ctx, username)
}

// GetByID resolves a canonical user by users.id, including its active status.
// It implements UserByIDLookup for delegated-credential active checks.
func (s *DatlyUserService) GetByID(ctx context.Context, id string) (*User, error) {
	if s == nil || s.invoker == nil || strings.TrimSpace(id) == "" {
		return nil, nil
	}
	return s.lookupByID(ctx, strings.TrimSpace(id))
}

func (s *DatlyUserService) GetBySubjectAndProvider(ctx context.Context, subject, provider string) (*User, error) {
	if s == nil || s.invoker == nil {
		return nil, errPreferencesUnavailable
	}
	if strings.TrimSpace(subject) == "" || strings.TrimSpace(provider) == "" {
		return nil, nil
	}
	input := &userread.UserInput{}
	input.SetSubject(strings.TrimSpace(subject))
	input.SetProvider(strings.TrimSpace(provider))
	return s.lookup(ctx, input)
}

func (s *DatlyUserService) Upsert(ctx context.Context, user *User) error {
	if s == nil || s.invoker == nil || user == nil {
		return nil
	}
	_, err := s.upsert(ctx, strings.TrimSpace(user.ID), strings.TrimSpace(user.Username), strings.TrimSpace(user.DisplayName), strings.TrimSpace(user.Email), firstNonEmpty(strings.TrimSpace(user.Provider), "local"), strings.TrimSpace(user.Subject), "", nil)
	return err
}

func (s *DatlyUserService) UpsertWithProvider(ctx context.Context, username, displayName, email, provider, subject string) (string, error) {
	return s.upsert(ctx, "", strings.TrimSpace(username), strings.TrimSpace(displayName), strings.TrimSpace(email), firstNonEmpty(strings.TrimSpace(provider), "oauth"), strings.TrimSpace(subject), "", nil)
}

func (s *DatlyUserService) UpdateHashIPByID(ctx context.Context, id, hash string) error {
	if s == nil || s.invoker == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	user := &userwrite.User{}
	user.SetId(strings.TrimSpace(id))
	if strings.TrimSpace(hash) != "" {
		user.SetHashIp(userTextPtr(strings.TrimSpace(hash)))
	}
	user.SetUpdatedAt(userNowPtr())
	return s.write(ctx, user)
}

func (s *DatlyUserService) UpdatePreferences(ctx context.Context, username string, patch *PreferencesPatch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.invoker == nil {
		return errPreferencesUnavailable
	}
	if patch == nil || strings.TrimSpace(username) == "" {
		return &WorkspacePreferenceError{Cause: fmt.Errorf("a user and preference update are required")}
	}
	patch.committedWorkspace = nil
	patch.committedDefaultChange = nil
	var workspaceResult *WorkspacePreferences
	var defaultChange *WorkspaceDefaultChange
	if patch.Workspace != nil && patch.WorkspaceAction != nil {
		return fmt.Errorf("workspace replacement and action are mutually exclusive")
	}
	if err := patch.Workspace.validate(); err != nil {
		return err
	}
	if err := patch.StarterTasks.validate(); err != nil {
		return err
	}
	existing, err := preferenceUser(ctx, s, username)
	if err != nil {
		return err
	}
	if existing == nil || strings.TrimSpace(existing.ID) == "" {
		return fmt.Errorf("user not found")
	}
	if patch.ExpectedOwnerID != nil && *patch.ExpectedOwnerID != existing.ID {
		return &preferencesChangedError{cause: identity.ErrResourceStale}
	}
	if existing.settingsRaw != nil && strings.TrimSpace(*existing.settingsRaw) != "" {
		var document map[string]json.RawMessage
		if err := json.Unmarshal([]byte(*existing.settingsRaw), &document); err != nil || document == nil {
			return fmt.Errorf("stored preferences are not a valid object; no changes were saved")
		}
	}
	user := &userwrite.User{}
	user.SetId(existing.ID)
	if patch.DisplayName != nil {
		user.SetDisplayName(userTextPtr(strings.TrimSpace(*patch.DisplayName)))
	}
	if patch.Timezone != nil && strings.TrimSpace(*patch.Timezone) != "" {
		user.SetTimezone(strings.TrimSpace(*patch.Timezone))
	}
	if patch.DefaultAgentRef != nil {
		user.SetDefaultAgentRef(userTextPtr(strings.TrimSpace(*patch.DefaultAgentRef)))
	}
	if patch.DefaultModelRef != nil {
		user.SetDefaultModelRef(userTextPtr(strings.TrimSpace(*patch.DefaultModelRef)))
	}
	if patch.DefaultEmbedderRef != nil {
		user.SetDefaultEmbedderRef(userTextPtr(strings.TrimSpace(*patch.DefaultEmbedderRef)))
	}
	if len(patch.AgentPrefs) > 0 || patch.StarterTasks != nil || patch.Workspace != nil || patch.WorkspaceAction != nil {
		settings := map[string]any{}
		if existing.Preferences != nil {
			for key, value := range existing.Preferences {
				settings[key] = value
			}
		}
		if len(patch.AgentPrefs) > 0 {
			settings["agentPrefs"] = patch.AgentPrefs
		}
		if patch.StarterTasks != nil {
			settings["starterTasks"] = patch.StarterTasks
		}
		if patch.Workspace != nil {
			settings["workspace"] = patch.Workspace
		}
		if patch.WorkspaceAction != nil {
			if patch.WorkspaceAction.Action == "set_default" || patch.WorkspaceAction.Action == "clear_default" {
				defaultChange = &WorkspaceDefaultChange{}
			}
			updated, err := applyWorkspaceMutationWithChange(settings["workspace"], patch.WorkspaceAction, defaultChange)
			if err != nil {
				return err
			}
			settings["workspace"] = updated
			workspaceResult = updated
		}
		if patch.Workspace != nil {
			if err := s.verifyChangedReportDefaults(ctx, existing.Preferences["workspace"], patch.Workspace); err != nil {
				return err
			}
		} else if defaultChange != nil && defaultChange.Current != nil &&
			(defaultChange.Previous == nil || *defaultChange.Previous != *defaultChange.Current) {
			if err := s.verifyNewReportDefault(ctx, *defaultChange.Current); err != nil {
				return err
			}
		}
		// Rotate an opaque revision inside the existing settings document. The
		// MySQL TEXT column uses case-insensitive collation; a new revision makes
		// every preference write distinct even for case-only edits.
		settings["_preferencesRevision"] = uuid.NewString()
		data, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		if len(data) > 65535 {
			return &WorkspacePreferenceError{Cause: fmt.Errorf("These settings are too large to save. Shorten personal task prompts or remove unused workspace shortcuts, then try again.")}
		}
		user.SetSettings(userTextPtr(string(data)))
	}
	user.SetUpdatedAt(userNowPtr())
	if err := s.writePreferences(ctx, user, existing.settingsRaw); err != nil {
		return err
	}
	patch.committedWorkspace = workspaceResult
	patch.committedDefaultChange = defaultChange
	return nil
}

func (s *DatlyUserService) upsert(ctx context.Context, explicitID, username, displayName, email, provider, subject, timezone string, settings map[string]any) (string, error) {
	if s == nil || s.invoker == nil || strings.TrimSpace(username) == "" {
		return "", nil
	}
	id := strings.TrimSpace(explicitID)
	normalizedProvider := firstNonEmpty(strings.TrimSpace(provider), "oauth")
	var existingBySubject *User
	if id == "" && strings.TrimSpace(subject) != "" {
		var err error
		existingBySubject, err = s.GetBySubjectAndProvider(ctx, strings.TrimSpace(subject), normalizedProvider)
		if err != nil {
			return "", err
		}
		if existingBySubject != nil && strings.TrimSpace(existingBySubject.ID) != "" {
			id = existingBySubject.ID
			if subjectIdentityReusable(existingBySubject, strings.TrimSpace(email), normalizedProvider, strings.TrimSpace(subject), strings.TrimSpace(timezone), settings) {
				return id, nil
			}
			if userMatchesDesired(existingBySubject, strings.TrimSpace(username), strings.TrimSpace(displayName), strings.TrimSpace(email), normalizedProvider, strings.TrimSpace(subject), strings.TrimSpace(timezone), settings) {
				return id, nil
			}
		}
	}
	if id == "" {
		existing, err := s.lookupByUsername(ctx, username)
		if err != nil {
			return "", err
		}
		if existing != nil && strings.TrimSpace(existing.ID) != "" {
			id = existing.ID
		}
	}
	if id == "" {
		id = uuid.NewString()
	}

	user := &userwrite.User{}
	user.SetId(id)
	user.SetUsername(username)
	if strings.TrimSpace(displayName) != "" {
		user.SetDisplayName(userTextPtr(strings.TrimSpace(displayName)))
	}
	if strings.TrimSpace(email) != "" {
		user.SetEmail(userTextPtr(strings.TrimSpace(email)))
	}
	user.SetProvider(normalizedProvider)
	if strings.TrimSpace(subject) != "" {
		user.SetSubject(userTextPtr(strings.TrimSpace(subject)))
	}
	user.SetTimezone(firstNonEmpty(strings.TrimSpace(timezone), "UTC"))
	if len(settings) > 0 {
		data, err := json.Marshal(settings)
		if err != nil {
			return "", err
		}
		user.SetSettings(userTextPtr(string(data)))
	}
	if err := s.write(ctx, user); err != nil {
		return "", err
	}
	return id, nil
}

func userMatchesDesired(existing *User, username, displayName, email, provider, subject, timezone string, settings map[string]any) bool {
	if existing == nil {
		return false
	}
	if strings.TrimSpace(existing.Username) != strings.TrimSpace(username) {
		return false
	}
	wantDisplay := strings.TrimSpace(firstNonEmpty(displayName, username))
	if strings.TrimSpace(existing.DisplayName) != wantDisplay {
		return false
	}
	if strings.TrimSpace(existing.Email) != strings.TrimSpace(email) {
		return false
	}
	if strings.TrimSpace(existing.Provider) != strings.TrimSpace(provider) {
		return false
	}
	if strings.TrimSpace(existing.Subject) != strings.TrimSpace(subject) {
		return false
	}
	if strings.TrimSpace(timezone) != "" && !strings.EqualFold(strings.TrimSpace(timezone), "UTC") {
		return false
	}
	if len(settings) > 0 {
		return false
	}
	return true
}

func subjectIdentityReusable(existing *User, email, provider, subject, timezone string, settings map[string]any) bool {
	if existing == nil {
		return false
	}
	if strings.TrimSpace(existing.Provider) != strings.TrimSpace(provider) {
		return false
	}
	if strings.TrimSpace(existing.Subject) != strings.TrimSpace(subject) {
		return false
	}
	if strings.TrimSpace(email) != "" && strings.TrimSpace(existing.Email) != strings.TrimSpace(email) {
		return false
	}
	if strings.TrimSpace(timezone) != "" && !strings.EqualFold(strings.TrimSpace(timezone), "UTC") {
		return false
	}
	if len(settings) > 0 {
		return false
	}
	return true
}

func (s *DatlyUserService) write(ctx context.Context, user *userwrite.User) error {
	in := &userwrite.Input{}
	in.SetUsers([]*userwrite.User{user})
	value, err := s.invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: userWriterTarget, Input: in})
	if err != nil {
		return err
	}
	if _, ok := value.(*userwrite.Output); !ok {
		return fmt.Errorf("user writer returned %T", value)
	}
	return nil
}

// writePreferences uses the generated writer's native mutation predicates. The
// original nullable settings value is retained exactly; JSON normalization must
// not change the expected database value.
func (s *DatlyUserService) writePreferences(ctx context.Context, user *userwrite.User, expected *string) error {
	in := &userwrite.Input{}
	in.SetUsers([]*userwrite.User{user})
	in.SetCompareSettings(true)
	if expected == nil {
		in.SetExpectedSettingsNull(true)
	} else {
		in.SetExpectedSettings(*expected)
	}
	value, err := s.invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: userWriterTarget, Input: in})
	if err != nil {
		var conflict *xhandler.Conflict
		if errors.As(err, &conflict) {
			return &preferencesChangedError{cause: err}
		}
		return err
	}
	if _, ok := value.(*userwrite.Output); !ok {
		return fmt.Errorf("user writer returned %T", value)
	}
	return nil
}

func (s *DatlyUserService) lookupByID(ctx context.Context, id string) (*User, error) {
	in := &userread.UserInput{Has: &userread.UserInputHas{Id: true}}
	in.Id = strings.TrimSpace(id)
	return s.lookup(ctx, in)
}

func (s *DatlyUserService) lookupByUsername(ctx context.Context, username string) (*User, error) {
	in := &userread.UserInput{Has: &userread.UserInputHas{Username: true}}
	in.Username = strings.TrimSpace(username)
	return s.lookup(ctx, in)
}

func (s *DatlyUserService) lookup(ctx context.Context, in *userread.UserInput) (*User, error) {
	value, err := s.invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: userReaderTarget, Input: in})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*userread.UserOutput)
	if !ok || out == nil {
		return nil, fmt.Errorf("user reader returned %T", value)
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	for _, item := range out.Data {
		if item == nil {
			continue
		}
		preferences := map[string]interface{}{}
		if item.Settings != nil && strings.TrimSpace(*item.Settings) != "" {
			decoder := json.NewDecoder(bytes.NewReader([]byte(*item.Settings)))
			decoder.UseNumber()
			_ = decoder.Decode(&preferences)
		}
		return &User{
			ID:          strings.TrimSpace(item.Id),
			Username:    strings.TrimSpace(item.Username),
			Email:       strings.TrimSpace(stringValue(item.Email)),
			DisplayName: strings.TrimSpace(firstNonEmpty(stringValue(item.DisplayName), item.Username)),
			Provider:    strings.TrimSpace(item.Provider),
			Subject:     strings.TrimSpace(stringValue(item.Subject)),
			Preferences: preferences,
			settingsRaw: item.Settings,
			Disabled:    item.Disabled != 0,
		}, nil
	}
	return nil, nil
}

func stringValue(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return strings.TrimSpace(*ptr)
}

func userTextPtr(value string) *string { return &value }
func userNowPtr() *time.Time           { now := time.Now().UTC(); return &now }

// SetWorkspaceDefaultVerifier is configured once before serving requests.
func (s *DatlyUserService) SetWorkspaceDefaultVerifier(verify func(context.Context, WorkspaceReportDefault) error) {
	s.verifyReportDefault = verify
}
func (s *DatlyUserService) verifyNewReportDefault(ctx context.Context, entry WorkspaceReportDefault) error {
	if s.verifyReportDefault == nil {
		return fmt.Errorf("personal report default verification is unavailable")
	}
	if err := s.verifyReportDefault(ctx, entry); err != nil {
		return fmt.Errorf("personal report default verification: %w", err)
	}
	return nil
}

func (s *DatlyUserService) verifyChangedReportDefaults(ctx context.Context, before any, next *WorkspacePreferences) error {
	if len(next.Defaults) == 0 {
		return nil
	}
	var previous struct {
		Defaults []WorkspaceReportDefault `json:"defaults"`
	}
	raw, err := json.Marshal(before)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &previous); err != nil {
		return err
	}
	existing := make(map[WorkspaceReportDefault]bool, len(previous.Defaults))
	for _, entry := range previous.Defaults {
		existing[entry] = true
	}
	for _, entry := range next.Defaults {
		if existing[entry] {
			continue
		}
		if err := s.verifyNewReportDefault(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}
