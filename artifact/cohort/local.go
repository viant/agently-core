package cohort

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/protocol/primitive/observe"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/forge/backend/artifactcohort"
	scyauth "github.com/viant/scy/auth"
	"gopkg.in/yaml.v3"
)

// LocalConfig is an explicit operator-only activation file.
// Omitted credentials select anonymous published metadata. ExpectedTenant and
// ExpectedAccount then supply the trusted cache and interactive admission scope.
// Optional authenticated compatibility uses secret REFERENCES, never tokens.
type LocalConfig struct {
	Namespace        string   `yaml:"namespace"`
	ConnectionName   string   `yaml:"connectionName"`
	ProviderIdentity string   `yaml:"providerIdentity"`
	ExpectedEndpoint string   `yaml:"expectedEndpoint"`
	Origin           string   `yaml:"origin"`
	OAuthClient      string   `yaml:"oauthClient"`
	UserSecret       string   `yaml:"userSecret"`
	Key              string   `yaml:"key"`
	Scopes           []string `yaml:"scopes"`
	ExpectedSubject  string   `yaml:"expectedSubject"`
	ExpectedTenant   string   `yaml:"expectedTenant"`
	ExpectedAccount  string   `yaml:"expectedAccount"`
	Cohort           string   `yaml:"cohort"`
}

// Accept the original one-provider object or an ordered list of independent
// provider registrations. Every entry retains the same endpoint/identity pins.
func loadArtifactCohortLocalConfigs(path string) ([]LocalConfig, error) {
	if !filepath.IsAbs(path) {
		return nil, artifactcohort.ErrInvalid
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err = yaml.Unmarshal(raw, &document); err != nil || len(document.Content) != 1 {
		return nil, artifactcohort.ErrInvalid
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	var configs []LocalConfig
	if document.Content[0].Kind == yaml.SequenceNode {
		err = decoder.Decode(&configs)
	} else {
		var config LocalConfig
		err = decoder.Decode(&config)
		configs = []LocalConfig{config}
	}
	if err != nil {
		return nil, err
	}
	if len(configs) == 0 || decoder.Decode(new(any)) != io.EOF {
		return nil, artifactcohort.ErrInvalid
	}
	providers, connections := map[string]bool{}, map[string]bool{}
	for i := range configs {
		config := &configs[i]
		for _, value := range []string{config.ConnectionName, config.ProviderIdentity, config.ExpectedEndpoint} {
			if strings.TrimSpace(value) == "" {
				return nil, artifactcohort.ErrInvalid
			}
		}
		authenticated, err := localCredentialMode(*config)
		if err != nil || providers[config.ProviderIdentity] || connections[config.ConnectionName] {
			return nil, artifactcohort.ErrInvalid
		}
		providers[config.ProviderIdentity], connections[config.ConnectionName] = true, true
		if authenticated && len(config.Scopes) == 0 {
			config.Scopes = []string{"openid"}
		}
	}
	return configs, nil
}

func localCredentialMode(config LocalConfig) (bool, error) {
	authenticated := config.OAuthClient != "" || config.UserSecret != "" || config.Key != ""
	if authenticated {
		if !filepath.IsAbs(config.OAuthClient) || !filepath.IsAbs(config.UserSecret) || config.Key != "blowfish://default" {
			return false, artifactcohort.ErrInvalid
		}
		return true, nil
	}
	if strings.TrimSpace(config.ExpectedTenant) == "" || strings.TrimSpace(config.ExpectedAccount) == "" || config.ExpectedSubject != "" || len(config.Scopes) != 0 {
		return false, artifactcohort.ErrInvalid
	}
	return false, nil
}

func loadArtifactCohortLocalConfig(path string) (LocalConfig, error) {
	configs, err := loadArtifactCohortLocalConfigs(path)
	if err != nil {
		return LocalConfig{}, err
	}
	if len(configs) != 1 {
		return LocalConfig{}, artifactcohort.ErrInvalid
	}
	return configs[0], nil
}

func PrepareLocalBindings(ctx context.Context, rt *executor.Runtime, path string) ([]Binding, error) {
	configs, err := loadArtifactCohortLocalConfigs(path)
	if err != nil {
		return nil, err
	}
	bindings := make([]Binding, 0, len(configs))
	for _, config := range configs {
		binding, err := prepareLocalArtifactCohortConfig(ctx, rt, config)
		if err != nil {
			for _, prepared := range bindings {
				if prepared.Close != nil {
					prepared.Close()
				}
			}
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

type localScyHostToken struct {
	mu      sync.Mutex
	config  LocalConfig
	value   string
	expires time.Time
}

func (s *localScyHostToken) Token(ctx context.Context) (string, error) {
	if s == nil || ctx == nil || ctx.Err() != nil {
		return "", artifactcohort.ErrDenied
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.value != "" && time.Until(s.expires) > time.Minute {
		return s.value, nil
	}
	commandCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "scy", "authorize", "-a", "OOB", "-p", "-c", s.config.OAuthClient, "-e", s.config.UserSecret, "-s", strings.Join(s.config.Scopes, " "), "--tokenType=id", "-k", s.config.Key)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("configured local scy identity unavailable")
	}
	token := strings.TrimSpace(output.String())
	exp, err := unverifiedTokenExpiry(token)
	if err != nil || !exp.After(time.Now().Add(time.Minute)) {
		return "", artifactcohort.ErrDenied
	}
	s.value, s.expires = token, exp
	return token, nil
}

// This unverified expiry is ONLY a refresh hint. The configured identity
// provider and the producer independently verify the token before any grant.
func unverifiedTokenExpiry(value string) (time.Time, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return time.Time{}, artifactcohort.ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, artifactcohort.ErrInvalid
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Exp <= 0 {
		return time.Time{}, artifactcohort.ErrInvalid
	}
	return time.Unix(claims.Exp, 0), nil
}

// prepareLocalArtifactCohort constructs the configured metadata connection.
// Optional authenticated bootstrap verifies its configured identity through
// the existing principal provider; anonymous bootstrap resolves no principal.
func prepareLocalArtifactCohort(ctx context.Context, rt *executor.Runtime, path string) (Binding, error) {
	config, err := loadArtifactCohortLocalConfig(path)
	if err != nil {
		return Binding{}, err
	}
	return prepareLocalArtifactCohortConfig(ctx, rt, config)
}

func prepareLocalArtifactCohortConfig(ctx context.Context, rt *executor.Runtime, config LocalConfig) (Binding, error) {
	if rt == nil || rt.VerifiedPrincipalResolver == nil {
		return Binding{}, artifactcohort.ErrDenied
	}
	authenticated, err := localCredentialMode(config)
	if err != nil {
		return Binding{}, err
	}
	tenant, account := config.ExpectedTenant, config.ExpectedAccount
	var hostToken HostToken
	if authenticated {
		tokens := &localScyHostToken{config: config}
		hostToken = tokens.Token
		tokenTiming := observe.Start(ctx, "startup.cohort.token", observe.Labels{})
		value, err := tokens.Token(ctx)
		tokenTiming.End(observe.OutcomeFor(err), observe.CacheUnknown)
		if err != nil {
			return Binding{}, err
		}
		verifiedCtx := svcauth.InjectTokens(ctx, &scyauth.Token{IDToken: value})
		principalTiming := observe.Start(ctx, "startup.cohort.principal", observe.Labels{})
		principal, err := rt.VerifiedPrincipalResolver.ResolvePrincipal(verifiedCtx)
		principalTiming.End(observe.OutcomeFor(err), observe.CacheUnknown)
		if err != nil || principal.Facts.Subject == "" || config.ExpectedSubject != "" && principal.Facts.Subject != config.ExpectedSubject || principal.Facts.Issuer == "" || principal.Facts.Tenant == "" || principal.AccountID == "" || !principal.Facts.ValidUntil.After(time.Now()) || tenant != "" && tenant != principal.Facts.Tenant || account != "" && account != principal.AccountID {
			return Binding{}, artifactcohort.ErrDenied
		}
		tenant, account = principal.Facts.Tenant, principal.AccountID
	}
	connectionTiming := observe.Start(ctx, "startup.cohort.connection", observe.Labels{})
	client, notifications, err := NewHostClient(ctx, rt, config.ConnectionName, config.ProviderIdentity, config.ExpectedEndpoint, config.Origin, hostToken)
	connectionTiming.End(observe.OutcomeFor(err), observe.CacheBypass)
	if err != nil {
		return Binding{}, err
	}
	cacheTiming := observe.Start(ctx, "startup.cohort.cache", observe.Labels{})
	binding, err := PrepareBinding(ctx, rt, Registration{ConnectionName: config.ConnectionName, ProviderIdentity: config.ProviderIdentity, Namespace: config.Namespace, Tenant: tenant, Account: account, Cohort: config.Cohort, Client: client, Notifications: notifications})
	cacheTiming.End(observe.OutcomeFor(err), observe.CacheUnknown)
	if err != nil {
		switch closer := client.(type) {
		case interface{ Close() }:
			closer.Close()
		case interface{ Close() error }:
			_ = closer.Close()
		}
		notifications.Close()
		return Binding{}, err
	}
	known, ok := binding.Source.(interface{ KnownHash() string })
	if !ok {
		binding.Close()
		return Binding{}, artifactcohort.ErrInvalid
	}
	binding.PollInterval = 2 * time.Second
	start := binding.Start
	closeBinding := binding.Close
	var stopNotifications func()
	binding.Start = func(ctx context.Context) error {
		if start != nil {
			if err := start(ctx); err != nil {
				return err
			}
		}
		var err error
		stopNotifications, err = StartNotifications(ctx, config.ExpectedEndpoint, config.Origin, binding.Namespaces[0], hostToken, known.KnownHash, notifications)
		return err
	}
	binding.Close = func() {
		if stopNotifications != nil {
			stopNotifications()
		}
		if closeBinding != nil {
			closeBinding()
		}
	}
	return binding, nil
}
