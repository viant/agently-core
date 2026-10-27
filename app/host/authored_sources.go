package host

import (
	"context"
	"fmt"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
)

// DirectorySource assigns an authored folder to a logical namespace. Publication
// does not grant identity, resource permissions, or revision selection.
type DirectorySource struct {
	Namespace string `json:"namespace"`
	Directory string `json:"directory"`
}

// AuthoredCompiler prepares the existing compilation in the shared snapshot's
// confined reader. Its lifetime ends after that startup compilation.
type AuthoredCompiler interface {
	Prepare(context.Context, *resources.ExtensionReader) error
	Release()
}

func AuthoredDirectories(templates, intents []DirectorySource, playbookSources ...[]DirectorySource) ([]resources.ExtensionDirectory, []AuthoredCompiler, error) {
	var dirs []resources.ExtensionDirectory
	var compilers []AuthoredCompiler
	for _, src := range templates {
		if src.Namespace == "users" {
			return nil, nil, fmt.Errorf("users templates require trusted ownership projection")
		}
		c := resources.NewAuthoredCompilation(src.Directory)
		compilers = append(compilers, c)
		dirs = append(dirs, c.TemplateDirectory(src.Namespace))
	}
	for _, src := range intents {
		if src.Namespace == "users" {
			return nil, nil, fmt.Errorf("users intents require trusted ownership projection")
		}
		c := resources.NewIntentCompilation(src.Directory, workspace.KindToolBundle)
		compilers = append(compilers, c)
		dirs = append(dirs, c.IntentDirectory(src.Namespace))
	}
	for _, group := range playbookSources {
		for _, src := range group {
			if src.Namespace == "users" {
				return nil, nil, fmt.Errorf("users playbooks require trusted ownership projection")
			}
			c := resources.NewIntentCompilation(src.Directory, workspace.KindToolBundle)
			compilers = append(compilers, c)
			dirs = append(dirs, c.PlaybookDirectory(src.Namespace))
		}
	}
	return dirs, compilers, nil
}

// BindAuthoredSources composes content with the managing provider's existing
// revision-policy callback. It never evaluates permissions during startup.
func BindAuthoredSources(ctx context.Context, root, owner string, templates, intents []DirectorySource, policy resources.LocalRevisionPolicyFactory, snapshot identity.ResourceSource, playbookSources ...[]DirectorySource) ([]resources.LocalResourceBinding, error) {
	playbookCount := 0
	for _, group := range playbookSources {
		playbookCount += len(group)
	}
	if len(templates)+len(intents)+playbookCount == 0 {
		return nil, nil
	}
	if owner == "" || policy == nil || snapshot == nil {
		return nil, identity.ErrResourceDenied
	}
	dirs, _, err := AuthoredDirectories(templates, intents, playbookSources...)
	if err != nil {
		return nil, err
	}
	return BindSources(ctx, root, owner, dirs, func(directory resources.ExtensionDirectory) (resources.ExtensionDirectory, error) {
		return directory, nil
	}, policy, snapshot)
}
