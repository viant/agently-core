package user

import (
	"context"

	"slices"

	iauth "github.com/viant/agently-core/internal/auth"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/reporting"
	resourcehost "github.com/viant/forge/backend/resourcehost"
)

type catalogRowsKey struct{}
type catalogRows struct {
	source                     *Reports
	owner, effective, provider string
	byOwner                    map[string]map[string]reporting.SharedArtifactRead
}

func (b *catalogRows) get(ctx context.Context, id string) (*reporting.SharedArtifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	row, ok := b.byOwner[iauth.EffectiveUserID(ctx)][id]
	if !ok {
		return nil, reporting.ErrNotFound
	}
	return row.Artifact, row.Err
}

func (c *Reports) PrepareCatalogPage(ctx context.Context, selected []resourcehost.LocatedResource) (context.Context, func() error, error) {
	store, ok := c.store.(interface {
		ReadSharedArtifactBatch(context.Context, []string) (map[string]reporting.SharedArtifactRead, func() error, error)
	})
	if !ok {
		return ctx, nil, nil
	}
	var canonicalIDs, legacyIDs []string
	for _, row := range selected {
		if row.Connection.ProviderIdentity != c.provider || row.Resource.Namespace != "users" {
			continue
		}
		uri, err := identity.ParseResourceURI(row.Resource.URI)
		if err != nil {
			return nil, nil, err
		}
		_, path, err := c.ownURI(ctx, uri)
		if err != nil {
			return nil, nil, err
		}
		canonicalIDs = append(canonicalIDs, userReportStorageID(uri))
		legacyIDs = append(legacyIDs, path)
	}
	if len(canonicalIDs) == 0 {
		return ctx, nil, nil
	}
	storage, err := canonicalStorageContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	owner, effective := iauth.EffectiveUserID(storage), iauth.EffectiveUserID(ctx)
	batch := &catalogRows{source: c, owner: owner, effective: effective, provider: iauth.Provider(ctx), byOwner: make(map[string]map[string]reporting.SharedArtifactRead)}
	if owner == effective {
		canonicalIDs = append(canonicalIDs, legacyIDs...)
	}
	rows, checkCanonical, err := store.ReadSharedArtifactBatch(storage, canonicalIDs)
	if err != nil {
		return nil, nil, err
	}
	if checkCanonical == nil {
		return ctx, nil, nil
	}
	batch.byOwner[owner] = rows
	finish := checkCanonical
	if owner != effective {
		// The single-record reader checks a legacy ID only when its canonical
		// record is absent. A corrupt or denied canonical record stays decisive.
		missing := make([]string, 0)
		for i, id := range canonicalIDs {
			if _, found := rows[id]; !found {
				missing = append(missing, legacyIDs[i])
			}
		}
		if len(missing) > 0 {
			slices.Sort(missing)
			legacy, checkLegacy, err := store.ReadSharedArtifactBatch(ctx, slices.Compact(missing))
			if err != nil {
				return nil, nil, err
			}
			batch.byOwner[effective] = legacy
			finish = func() error {
				if err := checkCanonical(); err != nil {
					return err
				}
				return checkLegacy()
			}
		}
	}
	scoped, cancel := context.WithCancel(context.WithValue(ctx, catalogRowsKey{}, batch))
	return scoped, func() error {
		defer cancel()
		if err := ctx.Err(); err != nil {
			return err
		}
		return finish()
	}, nil
}
