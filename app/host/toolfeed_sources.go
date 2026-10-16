package host

import (
	"context"
	"fmt"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
)

// BindToolfeedSources uses the same generic publisher and captured source as
// other authored kinds. Policy remains a managing-provider callback.
func BindToolfeedSources(ctx context.Context, root, owner string, sources []DirectorySource, policy resources.LocalRevisionPolicyFactory, snapshot identity.ResourceSource) ([]resources.LocalResourceBinding, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	if owner == "" || policy == nil {
		return nil, fmt.Errorf("toolfeed sources require explicit provider and authorization")
	}
	return BindSources(ctx, root, owner, sources, func(source DirectorySource) (resources.ExtensionDirectory, error) {
		if source.Namespace == "users" {
			return resources.ExtensionDirectory{}, fmt.Errorf("toolfeed users namespace requires trusted ownership projection")
		}
		return resources.ToolfeedDirectory(source.Namespace, source.Directory), nil
	}, policy, snapshot)
}
