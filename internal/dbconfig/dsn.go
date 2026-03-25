package dbconfig

import (
	"context"
	"net/url"
	"strings"

	"github.com/viant/scy"
	"github.com/viant/scy/cred"
)

// ExpandDSN resolves AGENTLY_DB_SECRETS and interpolates supported placeholders
// in the DSN. It returns the expanded DSN and decoded basic credentials.
//
// Supported placeholders:
// - ${username}, {{username}}, ${user}, {{user}}
// - ${password}, {{password}}
// - ${email}, {{email}}
// - ${username_urlencoded}, {{username_urlencoded}}, ${user_urlencoded}, {{user_urlencoded}}
// - ${password_urlencoded}, {{password_urlencoded}}
// - ${email_urlencoded}, {{email_urlencoded}}
func ExpandDSN(ctx context.Context, dsn string, secretsRef string) (string, *scy.Resource, error) {
	dsn = strings.TrimSpace(dsn)
	secretsRef = strings.TrimSpace(secretsRef)
	if secretsRef == "" {
		return dsn, nil, nil
	}
	basic := &cred.Basic{}
	resource := scy.EncodedResource(secretsRef).Decode(ctx, basic)
	if resource == nil {
		return dsn, nil, nil
	}
	return expandBasicPlaceholders(dsn, basic), resource, nil
}

func expandBasicPlaceholders(dsn string, basic *cred.Basic) string {
	if strings.TrimSpace(dsn) == "" || basic == nil {
		return dsn
	}
	replacements := map[string]string{
		"${username}":             basic.Username,
		"{{username}}":            basic.Username,
		"${user}":                 basic.Username,
		"{{user}}":                basic.Username,
		"${password}":             basic.Password,
		"{{password}}":            basic.Password,
		"${email}":                basic.Email,
		"{{email}}":               basic.Email,
		"${username_urlencoded}":  url.QueryEscape(basic.Username),
		"{{username_urlencoded}}": url.QueryEscape(basic.Username),
		"${user_urlencoded}":      url.QueryEscape(basic.Username),
		"{{user_urlencoded}}":     url.QueryEscape(basic.Username),
		"${password_urlencoded}":  url.QueryEscape(basic.Password),
		"{{password_urlencoded}}": url.QueryEscape(basic.Password),
		"${email_urlencoded}":     url.QueryEscape(basic.Email),
		"{{email_urlencoded}}":    url.QueryEscape(basic.Email),
	}
	result := dsn
	for placeholder, value := range replacements {
		if strings.Contains(result, placeholder) {
			result = strings.ReplaceAll(result, placeholder, value)
		}
	}
	return result
}
