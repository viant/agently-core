package resource

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
)

// CatalogQuery and CatalogPage use the same local query contract for every kind.
type CatalogQuery = primitive.CatalogQuery

type NamespacePage = primitive.CatalogPage[DiscoveredNamespace]

func (g *Gateway) catalog(ctx context.Context, kind, category string, load func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error), sourceLease ...func() time.Time) (*primitive.Catalog[json.RawMessage], identity.VerifiedActor, error) {
	actor, err := g.actor(ctx)
	if err != nil {
		return nil, actor, err
	}
	names, err := g.Manager.Names(ctx)
	if err != nil {
		return nil, actor, err
	}
	sort.Strings(names)
	scope, cacheable, err := g.Manager.CatalogScope(ctx, "", names)
	if err != nil {
		return nil, actor, err
	}
	until := g.now().Add(30 * time.Second)
	if actor.ValidUntil.Before(until) {
		until = actor.ValidUntil
	}
	if !cacheable {
		until = g.now()
	}
	generation := g.catalogGeneration(names)
	key := fmt.Sprintf("primitive:%p:%s:%s:%s:%x", g, actorKey(actor, "", 0), scope, kind, generation)
	partition := g.Manager.CatalogPartition(ctx, catalogPrincipalScope(actor), category)
	index, err := g.Manager.VisibleCatalogInPartition(ctx, key, partition, until, load, sourceLease...)
	if err != nil {
		return nil, actor, err
	}
	current, _, err := g.Manager.CatalogScope(ctx, "", names)
	if err != nil {
		return nil, actor, err
	}
	if current != scope || generation != g.catalogGeneration(names) {
		return nil, actor, primitive.ErrCatalogCursor
	}
	if err = g.final(ctx, actor); err != nil {
		return nil, actor, err
	}
	return index, actor, nil
}

// ListPage queries one cached visible metadata snapshot. Legacy List and all
// Get/data execution paths remain fresh and do not consume this snapshot.
func ListPage[T any](ctx context.Context, g *Gateway, kind string, query CatalogQuery, hydrateDefinitions ...bool) (*primitive.CatalogPage[Located[T]], error) {
	if !identity.ValidResourceKind(kind) {
		return nil, identity.ErrResource
	}
	hydrated := len(hydrateDefinitions) > 0 && hydrateDefinitions[0]
	catalogKind := kind
	if hydrated {
		catalogKind += ":definitions"
	}
	var definitionLease time.Time
	index, actor, err := g.catalog(ctx, catalogKind, kind, func(ctx context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
		rows, err := g.List(ctx, kind, "")
		if err != nil {
			return nil, err
		}
		if hydrated && len(rows) > primitive.MaxCatalogPageSize {
			return nil, identity.ErrResource
		}
		items := make([]primitive.CatalogItem[json.RawMessage], 0, len(rows))
		for _, row := range rows {
			if hydrated {
				fresh, e := Get[json.RawMessage](ctx, g, row.Connection, identity.ResourceRef{URI: row.Resource.URI, Revision: row.Resource.Revision}, nil)
				if errors.Is(e, identity.ErrResourceDenied) && !resourceIdentityRejected(e) {
					continue
				}
				if e != nil {
					return nil, e
				}
				if row.Resource.ContentFingerprint != "" && row.Resource.ContentFingerprint != fresh.Resource.ContentFingerprint {
					return nil, identity.ErrResourceStale
				}
				if fresh.ResolvedResource == nil {
					return nil, identity.ErrResourceDenied
				}
				if definitionLease.IsZero() || fresh.ResolvedResource.ValidUntil.Before(definitionLease) {
					definitionLease = fresh.ResolvedResource.ValidUntil
				}
				row.ResolvedResource = fresh.ResolvedResource
				row.Resource = *fresh.Resource
				row.Resource.Definition = fresh.Definition
				row.Resource.DefinitionBytes = nil // One exact definition carrier in the private snapshot.
			}
			raw, err := json.Marshal(row)
			if err != nil {
				return nil, err
			}
			items = append(items, primitive.CatalogItem[json.RawMessage]{Name: row.Resource.Name, Description: row.Resource.Description, Namespace: row.Resource.Namespace, URI: row.Resource.URI, Provider: row.Connection.ProviderIdentity, OwnerID: row.Resource.OwnerID, Value: raw})
		}
		return items, nil
	}, func() time.Time { return definitionLease })
	if err != nil {
		return nil, err
	}
	page, err := index.Page(ctx, query)
	if err != nil {
		return nil, err
	}
	result := &primitive.CatalogPage[Located[T]]{NextCursor: page.NextCursor, Complete: page.Complete, Total: page.Total, Items: make([]Located[T], 0, len(page.Items))}
	for _, raw := range page.Items {
		var located LocatedResource
		if err = json.Unmarshal(raw, &located); err != nil {
			return nil, err
		}
		definition, err := decodeDefinition[T](located.Resource.Definition)
		if err != nil {
			return nil, err
		}
		result.Items = append(result.Items, Located[T]{LocatedResource: located, Definition: definition})
	}
	if err = g.final(ctx, actor); err != nil {
		return nil, err
	}
	if err = g.Manager.CheckVisibleCatalog(ctx, index); err != nil {
		return nil, err
	}
	return result, nil
}
func (g *Gateway) ListPage(ctx context.Context, kind string, query CatalogQuery) (*primitive.CatalogPage[Located[json.RawMessage]], error) {
	return ListPage[json.RawMessage](ctx, g, kind, query)
}

// NamespacePage describes explicitly published provider metadata and declared
// supported operations; it never invents capabilities or fetches definitions.
func (g *Gateway) NamespacePage(ctx context.Context, query CatalogQuery) (*NamespacePage, error) {
	index, actor, err := g.catalog(ctx, "namespace", "namespace", func(ctx context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
		namespaces, err := g.Discover(ctx)
		if err != nil {
			return nil, err
		}
		items := make([]primitive.CatalogItem[json.RawMessage], 0, len(namespaces))
		for _, namespace := range namespaces {
			raw, err := json.Marshal(namespace)
			if err != nil {
				return nil, err
			}
			items = append(items, primitive.CatalogItem[json.RawMessage]{Name: namespace.Namespace, Description: namespace.Description, Namespace: namespace.Namespace, URI: namespace.Namespace, Provider: namespace.Connection.ProviderIdentity, Value: raw})
		}
		return items, nil
	})
	if err != nil {
		return nil, err
	}
	page, err := index.Page(ctx, query)
	if err != nil {
		return nil, err
	}
	result := &NamespacePage{NextCursor: page.NextCursor, Complete: page.Complete, Total: page.Total, Items: make([]DiscoveredNamespace, 0, len(page.Items))}
	for _, raw := range page.Items {
		var item DiscoveredNamespace
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		result.Items = append(result.Items, item)
	}
	if err = g.final(ctx, actor); err != nil {
		return nil, err
	}
	if err = g.Manager.CheckVisibleCatalog(ctx, index); err != nil {
		return nil, err
	}
	return result, nil
}

func (g *Gateway) catalogGeneration(names []string) [32]byte {
	generations := make([]uint64, len(names))
	g.mu.Lock()
	for i, name := range names {
		generations[i] = g.generations[name]
	}
	g.mu.Unlock()
	raw, _ := json.Marshal(generations)
	return sha256.Sum256(raw)
}

// Full catalog keys retain account/revision/credential isolation. Mutation tags
// identify the stable user realm so successful writes also expire that user's
// older account/revision/session variants without touching other principals.
func catalogPrincipalScope(actor identity.VerifiedActor) string {
	raw, _ := json.Marshal([]any{actor.PublicRead, actor.Issuer, actor.Subject, actor.TenantID})
	return string(raw)
}
