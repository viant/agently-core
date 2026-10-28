package executor

import (
	"context"
	"fmt"

	"github.com/viant/agently-core/internal/logx"
	identity "github.com/viant/agently-core/protocol/resource"
	auth "github.com/viant/agently-core/service/auth"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
	producer "github.com/viant/forge/backend/resourcehost/producer"
)

func (b *Builder) configureNativeHostDocuments(out *Runtime) error {
	if out == nil || out.NativeHostDocuments != nil {
		return nil
	}
	root := workspace.Root()
	if out.Store != nil {
		root = out.Store.Root()
	}
	snapshot, files, err := resources.CaptureHostDocuments(context.Background(), root)
	if err != nil {
		return err
	}
	reader := &resources.HostDocuments{Root: root, Files: files, Connection: "internal"}
	docs, err := resources.MCPResourceDocuments(out.LocalPrimitiveProvider)
	if err != nil {
		return err
	}
	if snapshot != nil {
		docs = append(docs, snapshot.NativeResourceDocuments()...)
	}
	attachNativeHostDocumentFailureDiagnostics(docs, files)
	if len(docs) > 0 {
		if out.LocalPrimitiveProvider != nil {
			if err = out.LocalPrimitiveProvider.ConfigureNativeResources(docs); err != nil {
				return err
			}
		} else {
			actor, verify := b.primitiveActor, b.primitiveVerifier
			if out.PrimitiveProviders != nil {
				actor, verify = out.PrimitiveProviders.Actor, out.PrimitiveProviders.Verify
			} else if actor == nil && out.ComponentAuthority != nil {
				actor, verify = resources.AuthorityActors(out.ComponentAuthority)
			}
			if actor == nil && verify == nil {
				// Raw host documents carry no business authority. A pinned datasource
				// composition may intentionally have no general primitive actor.
				actor, verify = resources.PublicReadActors()
				if out.AuthConfig != nil && out.AuthConfig.Enabled {
					baseActor, baseVerify := actor, verify
					actor = func(ctx context.Context) (identity.VerifiedActor, error) {
						if auth.EffectiveUserID(ctx) == "" {
							return identity.VerifiedActor{}, identity.ErrResourceDenied
						}
						return baseActor(ctx)
					}
					verify = func(ctx context.Context, held identity.VerifiedActor) error {
						if auth.EffectiveUserID(ctx) == "" {
							return identity.ErrResourceDenied
						}
						return baseVerify(ctx, held)
					}
				}
				if out.MCPManager == nil {
					return fmt.Errorf("native documents require a manager")
				}
				out.PrimitiveProviders = resources.NewGateway(out.MCPManager, actor, verify, b.primitiveGatewayIdentity)
				out.nativeDocumentOnlyGateway = true
			}
			if actor == nil || verify == nil {
				return fmt.Errorf("native documents require verified provider authority")
			}
			out.LocalPrimitiveProvider, err = resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: resources.InternalProviderIdentity, Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error {
				return identity.ErrResourceDenied
			}, NativeResources: docs})
			if err != nil {
				return err
			}
		}
		reader.Provider = out.LocalPrimitiveProvider.ProviderIdentity()
	}
	out.NativeHostDocuments = reader
	return nil
}

func attachNativeHostDocumentFailureDiagnostics(docs []producer.NativeResource, files map[string]string) {
	fileByURI := make(map[string]string, len(files))
	for file, uri := range files {
		fileByURI[uri] = file
	}
	for i := range docs {
		file, ok := fileByURI[docs[i].Metadata.Uri]
		if !ok {
			continue
		}
		docs[i].OnFailure = func(ctx context.Context, failure producer.NativeResourceFailure) {
			logx.OperationalWarnf("resource", "native.document.read.failed file=%q stage=%q category=%q", logx.SafeResourceIdentifier(file), failure.Stage, failure.Category)
		}
	}
}
