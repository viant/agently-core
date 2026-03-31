package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	userread "github.com/viant/agently-core/pkg/agently/user"
	userwrite "github.com/viant/agently-core/pkg/agently/user/write"
	"github.com/viant/datly"
	"github.com/viant/datly/repository/contract"
)

type DatlyUserService struct {
	dao *datly.Service
}

func NewDatlyUserService(dao *datly.Service) *DatlyUserService {
	if dao == nil {
		return nil
	}
	return &DatlyUserService{dao: dao}
}

func (s *DatlyUserService) GetByUsername(ctx context.Context, username string) (*User, error) {
	if s == nil || s.dao == nil || strings.TrimSpace(username) == "" {
		return nil, nil
	}
	if user, err := s.lookupByUsername(ctx, username); err != nil || user != nil {
		return user, err
	}
	return s.lookupByID(ctx, username)
}

func (s *DatlyUserService) GetBySubjectAndProvider(ctx context.Context, subject, provider string) (*User, error) {
	if s == nil || s.dao == nil || strings.TrimSpace(subject) == "" || strings.TrimSpace(provider) == "" {
		return nil, nil
	}
	conn, err := s.dao.Resource().Connector("agently")
	if err != nil {
		return nil, err
	}
	db, err := conn.DB()
	if err != nil {
		return nil, err
	}
	const query = `SELECT id, username, display_name, email, provider, subject, settings
FROM users
WHERE subject = ? AND provider = ?
LIMIT 1`
	row := db.QueryRowContext(ctx, query, strings.TrimSpace(subject), strings.TrimSpace(provider))
	var (
		user       User
		display    sql.NullString
		emailVal   sql.NullString
		subjectVal sql.NullString
		settings   sql.NullString
	)
	if err := row.Scan(&user.ID, &user.Username, &display, &emailVal, &user.Provider, &subjectVal, &settings); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	user.DisplayName = strings.TrimSpace(firstNonEmpty(display.String, user.Username))
	user.Email = strings.TrimSpace(emailVal.String)
	user.Subject = strings.TrimSpace(subjectVal.String)
	if strings.TrimSpace(settings.String) != "" {
		user.Preferences = map[string]interface{}{}
		_ = json.Unmarshal([]byte(strings.TrimSpace(settings.String)), &user.Preferences)
	}
	return &user, nil
}

func (s *DatlyUserService) Upsert(ctx context.Context, user *User) error {
	if s == nil || s.dao == nil || user == nil {
		return nil
	}
	_, err := s.upsert(ctx, strings.TrimSpace(user.ID), strings.TrimSpace(user.Username), strings.TrimSpace(user.DisplayName), strings.TrimSpace(user.Email), firstNonEmpty(strings.TrimSpace(user.Provider), "local"), strings.TrimSpace(user.Subject), "", nil)
	return err
}

func (s *DatlyUserService) UpsertWithProvider(ctx context.Context, username, displayName, email, provider, subject string) (string, error) {
	return s.upsert(ctx, "", strings.TrimSpace(username), strings.TrimSpace(displayName), strings.TrimSpace(email), firstNonEmpty(strings.TrimSpace(provider), "oauth"), strings.TrimSpace(subject), "", nil)
}

func (s *DatlyUserService) UpdateHashIPByID(ctx context.Context, id, hash string) error {
	if s == nil || s.dao == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	user := &userwrite.User{}
	user.SetId(strings.TrimSpace(id))
	if strings.TrimSpace(hash) != "" {
		user.SetHashIP(strings.TrimSpace(hash))
	}
	user.SetUpdatedAt(time.Now().UTC())
	return s.write(ctx, user)
}

func (s *DatlyUserService) UpdatePreferences(ctx context.Context, username string, patch *PreferencesPatch) error {
	if s == nil || s.dao == nil || strings.TrimSpace(username) == "" || patch == nil {
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
		user.SetDisplayName(strings.TrimSpace(*patch.DisplayName))
	}
	if patch.Timezone != nil && strings.TrimSpace(*patch.Timezone) != "" {
		user.SetTimezone(strings.TrimSpace(*patch.Timezone))
	}
	if patch.DefaultAgentRef != nil {
		user.SetDefaultAgentRef(strings.TrimSpace(*patch.DefaultAgentRef))
	}
	if patch.DefaultModelRef != nil {
		user.SetDefaultModelRef(strings.TrimSpace(*patch.DefaultModelRef))
	}
	if patch.DefaultEmbedderRef != nil {
		user.SetDefaultEmbedderRef(strings.TrimSpace(*patch.DefaultEmbedderRef))
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
		user.SetSettings(string(data))
	}
	user.SetUpdatedAt(time.Now().UTC())
	return s.write(ctx, user)
}

func (s *DatlyUserService) upsert(ctx context.Context, explicitID, username, displayName, email, provider, subject, timezone string, settings map[string]any) (string, error) {
	if s == nil || s.dao == nil || strings.TrimSpace(username) == "" {
		return "", nil
	}
	id := strings.TrimSpace(explicitID)
	existing, err := s.lookupByUsername(ctx, username)
	if err != nil {
		return "", err
	}
	if existing != nil && strings.TrimSpace(existing.ID) != "" {
		id = existing.ID
	}
	if id == "" {
		id = uuid.NewString()
	}

	user := &userwrite.User{}
	user.SetId(id)
	user.SetUsername(username)
	if strings.TrimSpace(displayName) != "" {
		user.SetDisplayName(strings.TrimSpace(displayName))
	}
	if strings.TrimSpace(email) != "" {
		user.SetEmail(strings.TrimSpace(email))
	}
	user.SetProvider(firstNonEmpty(strings.TrimSpace(provider), "oauth"))
	if strings.TrimSpace(subject) != "" {
		user.SetSubject(strings.TrimSpace(subject))
	}
	user.SetTimezone(firstNonEmpty(strings.TrimSpace(timezone), "UTC"))
	if len(settings) > 0 {
		data, err := json.Marshal(settings)
		if err != nil {
			return "", err
		}
		user.SetSettings(string(data))
	}
	if err := s.write(ctx, user); err != nil {
		return "", err
	}
	return id, nil
}

func (s *DatlyUserService) write(ctx context.Context, user *userwrite.User) error {
	in := &userwrite.Input{Users: []*userwrite.User{user}}
	out := &userwrite.Output{}
	_, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath("PATCH", userwrite.PathURI)),
		datly.WithInput(in),
		datly.WithOutput(out),
	)
	return err
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
	out := &userread.UserOutput{}
	if _, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath("GET", userread.UserPathURI)),
		datly.WithInput(in),
		datly.WithOutput(out),
	); err != nil {
		return nil, err
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
