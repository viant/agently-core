package dbconfig

import (
	"context"
	"strings"

	"github.com/viant/scy"
)

// ExpandDSN resolves AGENTLY_DB_SECRETS and expands the DSN text using
// scy.Secret.Expand so secrets such as username/password can be interpolated.
func ExpandDSN(ctx context.Context, dsn string, secretsRef string) (string, *scy.Resource, error) {
	dsn = strings.TrimSpace(dsn)
	secretsRef = strings.TrimSpace(secretsRef)
	if secretsRef == "" {
		return dsn, nil, nil
	}
	resource := scy.NewResource("", secretsRef, "")
	secret, err := scy.New().Load(ctx, resource)
	if err != nil {
		return "", nil, err
	}
	if secret == nil || secret.Resource == nil {
		return dsn, nil, nil
	}
	return secret.Expand(dsn), secret.Resource, nil
}
