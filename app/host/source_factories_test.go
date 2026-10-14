package host

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
)

func TestToolfeedSourceFactoriesRetainConfinement(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "feeds", "nested"), 0700))
	file := filepath.Join(root, "feeds", "nested", "terminal.yaml")
	raw := []byte("id: legacy-terminal\ntitle: Terminal\nmatch:\n  service: system/exec\n  method: execute\n")
	require.NoError(t, os.WriteFile(file, raw, 0600))
	policy := func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) (identity.ResourceRevisionPolicy, error) {
		return &bindingTestPolicy{}, nil
	}
	for _, source := range []DirectorySource{{Directory: "feeds"}, {Namespace: "team"}, {Namespace: "../team", Directory: "feeds"}, {Namespace: "users", Directory: "feeds"}, {Namespace: "team", Directory: "../feeds"}, {Namespace: "team", Directory: root}, {Namespace: "team", Directory: "missing"}} {
		_, err := BindToolfeedSources(ctx, root, "internal", []DirectorySource{source}, policy, nil)
		require.Error(t, err)
	}
	sources := []DirectorySource{{Namespace: "team", Directory: "feeds"}}
	_, err := BindToolfeedSources(ctx, "", "internal", sources, policy, nil)
	require.Error(t, err)
	bindings, err := BindToolfeedSources(ctx, root, "internal", sources, policy, nil)
	require.NoError(t, err)
	require.Len(t, bindings, 1)
	actor := identity.VerifiedActor{Subject: "alice", Issuer: "fixture", IdentityRevision: "one", ValidUntil: time.Now().Add(time.Minute)}
	resolver, err := bindings[0].Resolver(ctx, actor, "resource.get")
	require.NoError(t, err)
	uri, err := identity.ParseResourceURI(bindings[0].URI)
	require.NoError(t, err)
	candidates, err := resolver.Source.Candidates(ctx, uri)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	definition, err := resolver.Source.ReadCandidate(ctx, uri, candidates[0])
	require.NoError(t, err)
	var value map[string]any
	require.NoError(t, json.Unmarshal(definition, &value))
	require.Equal(t, "legacy-terminal", value["id"])
	match := value["match"].(map[string]any)
	require.Equal(t, "system/exec", match["service"])
	require.Equal(t, "execute", match["method"])
	retained, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, raw, retained)
	require.NoError(t, os.Symlink(file, filepath.Join(root, "feeds", "alias.yaml")))
	_, err = BindToolfeedSources(ctx, root, "internal", sources, policy, nil)
	require.Error(t, err)
}

func TestAuthoredSourceFactoriesUseExistingCompilersAndOwningPolicy(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for directory, raw := range map[string]string{"templates": "id: original-template\ninstructions: Original template body\n", "intents": "id: original-intent\nmodel: OriginalModel\nmessages: [{role: system, text: Original profile body}]\n"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, directory), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, directory, "one.yaml"), []byte(raw), 0600))
	}
	templates, intents := []DirectorySource{{Namespace: "team", Directory: "templates"}}, []DirectorySource{{Namespace: "team", Directory: "intents"}}
	directories, compilers, err := AuthoredDirectories(templates, intents)
	require.NoError(t, err)
	require.Len(t, directories, 2)
	require.Len(t, compilers, 2)
	for _, c := range compilers {
		defer c.Release()
	}
	require.Equal(t, "template", directories[0].Kind)
	require.Equal(t, "intent", directories[1].Kind)
	_, _, err = AuthoredDirectories([]DirectorySource{{Namespace: "users", Directory: "templates"}}, nil)
	require.Error(t, err)
	_, _, err = AuthoredDirectories(nil, []DirectorySource{{Namespace: "users", Directory: "intents"}})
	require.Error(t, err)
	snapshot := &identity.LocalResource{}
	actor := identity.VerifiedActor{Subject: "alice", Issuer: "fixture", IdentityRevision: "one", ValidUntil: time.Now().Add(time.Minute)}
	calls := 0
	denied := false
	owningDenial := errors.New("managing source denied")
	policyValue := &bindingTestPolicy{}
	policy := func(got context.Context, a identity.VerifiedActor, uri identity.ResourceURI, operation string) (identity.ResourceRevisionPolicy, error) {
		calls++
		require.Equal(t, ctx, got)
		require.Equal(t, actor, a)
		require.Equal(t, "team", uri.Namespace)
		require.Equal(t, "resource.get", operation)
		if denied {
			return nil, owningDenial
		}
		return policyValue, nil
	}
	bindings, err := BindAuthoredSources(ctx, root, "owner", templates, intents, policy, snapshot)
	require.NoError(t, err)
	require.Len(t, bindings, 2)
	require.Zero(t, calls, "startup must not evaluate owning permissions")
	for _, binding := range bindings {
		resolved, err := binding.Resolver(ctx, actor, "resource.get")
		require.NoError(t, err)
		require.Equal(t, "owner", resolved.ProviderIdentity)
		require.Same(t, snapshot, resolved.Source)
		require.Same(t, policyValue, resolved.Policy)
	}
	denied = true
	_, err = bindings[0].Resolver(ctx, actor, "resource.get")
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	require.Equal(t, 3, calls, "the current owning callback must still reject the read")
	_, err = BindAuthoredSources(ctx, root, "owner", templates, intents, policy, nil)
	require.Error(t, err)
}
