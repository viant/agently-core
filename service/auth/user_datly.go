package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	userread "github.com/viant/agently-core/internal/datly/user/read"
	userwrite "github.com/viant/agently-core/internal/datly/user/write"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"reflect"
)

type DatlyUserService struct {
	invoker dexec.ComponentInvoker
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
	if s == nil || s.invoker == nil || strings.TrimSpace(username) == "" {
		return nil, nil
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
	if s == nil || s.invoker == nil || strings.TrimSpace(subject) == "" || strings.TrimSpace(provider) == "" {
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
	if s == nil || s.invoker == nil || strings.TrimSpace(username) == "" || patch == nil {
		return nil
	}
	existing, err := s.GetByUsername(ctx, username)
	if err != nil {
		return err
	}
	if existing == nil || strings.TrimSpace(existing.ID) == "" {
		return fmt.Errorf("user not found")
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
	if len(patch.AgentPrefs) > 0 {
		settings := map[string]any{}
		if existing.Preferences != nil {
			for key, value := range existing.Preferences {
				settings[key] = value
			}
		}
		settings["agentPrefs"] = patch.AgentPrefs
		data, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		user.SetSettings(userTextPtr(string(data)))
	}
	user.SetUpdatedAt(userNowPtr())
	return s.write(ctx, user)
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
			_ = json.Unmarshal([]byte(strings.TrimSpace(*item.Settings)), &preferences)
		}
		return &User{
			ID:          strings.TrimSpace(item.Id),
			Username:    strings.TrimSpace(item.Username),
			Email:       strings.TrimSpace(stringValue(item.Email)),
			DisplayName: strings.TrimSpace(firstNonEmpty(stringValue(item.DisplayName), item.Username)),
			Provider:    strings.TrimSpace(item.Provider),
			Subject:     strings.TrimSpace(stringValue(item.Subject)),
			Preferences: preferences,
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
