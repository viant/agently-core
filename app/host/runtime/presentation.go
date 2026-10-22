package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/app/host"
	"github.com/viant/agently-core/service/auth"
	wscfg "github.com/viant/agently-core/workspace/config"
)

// configurePresentation binds operator-owned user-info configuration only to
// display facts. It does not register an ACL store, gates or tool/data callbacks.
func configurePresentation(options Options, root string, config *wscfg.Root) (Options, error) {
	if config == nil {
		return options, nil
	}
	ui, _ := config.Raw["ui"].(map[string]interface{})
	raw, present := ui["presentation"]
	if !present {
		return options, nil
	}
	section, ok := raw.(map[string]interface{})
	if !ok {
		return options, fmt.Errorf("ui.presentation must be an object")
	}
	name, ok := section["identityFile"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return options, fmt.Errorf("ui.presentation.identityFile is required")
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(root, name)
	}
	identity, err := os.ReadFile(name)
	if err != nil {
		return options, fmt.Errorf("presentation identity configuration: %w", err)
	}
	factory := options.AuthorizationProviderFactory
	if factory == nil {
		factory = host.HTTPProviders
	}
	previous := options.ConfigureBuilder
	options.ConfigureBuilder = func(ctx context.Context, builder *executor.Builder) error {
		providers, err := factory(ctx, identity, nil)
		if err != nil {
			return fmt.Errorf("presentation identity provider: %w", err)
		}
		if providers.Identity == nil {
			return fmt.Errorf("presentation identity provider is unavailable")
		}
		builder.WithPresentationPrincipalResolver(auth.IDTokenIdentityProvider{Source: providers.Identity})
		if previous != nil {
			return previous(ctx, builder)
		}
		return nil
	}
	return options, nil
}
