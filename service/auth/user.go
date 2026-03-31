package auth

import "context"

// User represents a registered user.
type User struct {
	ID          string                 `json:"id,omitempty"`
	Username    string                 `json:"username"`
	Email       string                 `json:"email,omitempty"`
	DisplayName string                 `json:"displayName,omitempty"`
	Provider    string                 `json:"provider,omitempty"`
	Subject     string                 `json:"subject,omitempty"`
	Preferences map[string]interface{} `json:"preferences,omitempty"`
}

// UserService abstracts user CRUD. Implementations may use Datly, SQL, or an
// in-memory store.
type UserService interface {
	GetByUsername(ctx context.Context, username string) (*User, error)
	GetBySubjectAndProvider(ctx context.Context, subject, provider string) (*User, error)
	Upsert(ctx context.Context, user *User) error
	UpsertWithProvider(ctx context.Context, username, displayName, email, provider, subject string) (string, error)
	UpdateHashIPByID(ctx context.Context, id, hash string) error
	UpdatePreferences(ctx context.Context, username string, patch *PreferencesPatch) error
}
