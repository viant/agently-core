package host

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
)

type authoredSourceConfig struct{ Namespace, Folder string }
type feedSourceConfig struct{ Tenant, Path string }
type bindingTestPolicy struct{}

func (*bindingTestPolicy) SelectRevision(context.Context, identity.ResourceRef, []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	return identity.ResourceDecision{}, identity.ErrResourceDenied
}

func sourceDirectory(kind, namespace, directory string) resources.ExtensionDirectory {
	return resources.ExtensionDirectory{Kind: kind, Namespace: namespace, Directory: directory, FormatVersion: 1, Load: func(ctx context.Context, reader *resources.ExtensionReader, file string) (json.RawMessage, error) {
		raw, err := reader.ReadFile(ctx, file)
		return json.RawMessage(raw), err
	}}
}

func TestBindSourcesComposesTwoConfigTypesAndRetainsRequestPolicy(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"authored", "feeds"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"one", "two"} {
			if err := os.WriteFile(filepath.Join(root, directory, name+".yaml"), []byte(`{"name":"`+name+`"}`), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, kind := range []string{"template", "toolfeed"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			actor := identity.VerifiedActor{Subject: "reader", Issuer: "fixture", IdentityRevision: "r1", ValidUntil: time.Now().Add(time.Minute)}
			policyValue := &bindingTestPolicy{}
			deny := false
			calls := 0
			seenURI := ""
			policy := func(got context.Context, a identity.VerifiedActor, uri identity.ResourceURI, operation string) (identity.ResourceRevisionPolicy, error) {
				calls++
				if got != ctx || a.Subject != actor.Subject || operation != "resource.get" {
					t.Error("request context/actor/operation changed")
				}
				seenURI = uri.String()
				if deny {
					return nil, errors.New("current host policy denied")
				}
				return policyValue, nil
			}
			snapshot := &identity.LocalResource{}
			var bindings []resources.LocalResourceBinding
			var err error
			if kind == "template" {
				bindings, err = BindSources(ctx, root, "host-owner", []authoredSourceConfig{{Namespace: "team", Folder: "authored"}}, func(source authoredSourceConfig) (resources.ExtensionDirectory, error) {
					return sourceDirectory("template", source.Namespace, source.Folder), nil
				}, policy, snapshot)
			} else {
				bindings, err = BindSources(ctx, root, "host-owner", []feedSourceConfig{{Tenant: "team", Path: "feeds"}}, func(source feedSourceConfig) (resources.ExtensionDirectory, error) {
					return sourceDirectory("toolfeed", source.Tenant, source.Path), nil
				}, policy, nil)
			}
			if err != nil || len(bindings) != 2 {
				t.Fatalf("startup inventory: bindings=%d, err=%v", len(bindings), err)
			}
			if calls != 0 {
				t.Fatal("startup invoked request policy")
			}
			for _, binding := range bindings {
				resolved, err := binding.Resolver(ctx, actor, "resource.get")
				if err != nil || resolved == nil || resolved.ProviderIdentity != "host-owner" || resolved.Policy != policyValue || seenURI != binding.URI {
					t.Fatalf("binding lost ownership/policy: %v %v", resolved, err)
				}
				if kind == "template" {
					if resolved.Source != snapshot {
						t.Fatal("captured source was not installed")
					}
				} else {
					uri, _ := identity.ParseResourceURI(binding.URI)
					candidates, err := resolved.Source.Candidates(ctx, uri)
					if err != nil || len(candidates) != 1 {
						t.Fatal("confined source lost", err)
					}
					raw, err := resolved.Source.ReadCandidate(ctx, uri, candidates[0])
					if err != nil || !json.Valid(raw) {
						t.Fatal("confined loader lost", err)
					}
				}
				resolved.ProviderIdentity = "caller-mutated"
				resolved.Source = nil
				again, err := binding.Resolver(ctx, actor, "resource.get")
				if err != nil || again == resolved || again.ProviderIdentity != "host-owner" || again.Source == nil {
					t.Fatal("resolved result mutation leaked into next request", err)
				}
			}
			if calls != 4 {
				t.Fatalf("policy not called for each request: %d", calls)
			}
			deny = true
			if resolved, err := bindings[0].Resolver(ctx, actor, "resource.get"); resolved != nil || !errors.Is(err, identity.ErrResourceDenied) {
				t.Fatal("current denial was cached or exposed", resolved, err)
			}
		})
	}
}

func TestBindSourcesEmptyAndStartupErrors(t *testing.T) {
	bindings, err := BindSources[authoredSourceConfig](nil, "", "", nil, nil, nil, nil)
	if bindings != nil || err != nil {
		t.Fatal("empty configuration must remain a no-op", bindings, err)
	}
	mappingFailure := errors.New("namespace requires trusted ownership projection")
	bindings, err = BindSources(context.Background(), t.TempDir(), "owner", []feedSourceConfig{{Tenant: "users"}}, func(feedSourceConfig) (resources.ExtensionDirectory, error) {
		return resources.ExtensionDirectory{}, mappingFailure
	}, nil, nil)
	if bindings != nil || !errors.Is(err, mappingFailure) {
		t.Fatal("directory error was not preserved", bindings, err)
	}
	mapper := func(source authoredSourceConfig) (resources.ExtensionDirectory, error) {
		return sourceDirectory("template", source.Namespace, source.Folder), nil
	}
	policy := func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) (identity.ResourceRevisionPolicy, error) {
		return &bindingTestPolicy{}, nil
	}
	bindings, err = BindSources(context.Background(), t.TempDir(), "owner", []authoredSourceConfig{{Namespace: "team", Folder: "missing"}}, mapper, policy, nil)
	if bindings != nil || !os.IsNotExist(err) {
		t.Fatal("missing configured inventory did not fail closed", bindings, err)
	}
	bindings, err = BindSources(context.Background(), t.TempDir(), "", []authoredSourceConfig{{Namespace: "team", Folder: "missing"}}, mapper, policy, nil)
	if bindings != nil || !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatal("missing owner accepted", bindings, err)
	}
}
