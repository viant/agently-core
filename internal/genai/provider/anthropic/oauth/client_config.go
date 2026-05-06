package oauth

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// OAuthClientConfig represents the OAuth client identity used for Anthropic
// login and token refresh.
type OAuthClientConfig struct {
	ClientID     string
	ClientSecret string
	Issuer       string
	TokenURL     string
	APIKeyURL    string
}

func (c *OAuthClientConfig) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.ClientID = stringValue(raw, "client_id", "ClientID", "clientId")
	c.ClientSecret = stringValue(raw, "client_secret", "ClientSecret", "clientSecret")
	c.Issuer = stringValue(raw, "issuer", "Issuer", "issuer_url", "issuerURL")
	c.TokenURL = stringValue(raw, "token_url", "tokenURL", "TokenURL")
	c.APIKeyURL = stringValue(raw, "api_key_url", "apiKeyURL", "APIKeyURL")
	return nil
}

func (c *OAuthClientConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw map[string]any
	if err := value.Decode(&raw); err != nil {
		return err
	}
	c.ClientID = stringValue(raw, "client_id", "ClientID", "clientId")
	c.ClientSecret = stringValue(raw, "client_secret", "ClientSecret", "clientSecret")
	c.Issuer = stringValue(raw, "issuer", "Issuer", "issuer_url", "issuerURL")
	c.TokenURL = stringValue(raw, "token_url", "tokenURL", "TokenURL")
	c.APIKeyURL = stringValue(raw, "api_key_url", "apiKeyURL", "APIKeyURL")
	return nil
}

func (c *OAuthClientConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("oauth client config was nil")
	}
	if c.ClientID == "" {
		return fmt.Errorf("oauth client_id was empty")
	}
	return nil
}

func stringValue(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
