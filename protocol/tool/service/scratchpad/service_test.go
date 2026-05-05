package scratchpad

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	authctx "github.com/viant/agently-core/internal/auth"
)

func TestService_MemorizeListFetch_UserScoped(t *testing.T) {
	ctx := userCtx("alice@example.com")
	otherCtx := userCtx("bob@example.com")
	fs := afs.New()
	svc := New(
		WithAFS(fs),
		WithRootURI("mem://localhost/scratchpad_test/${userID}"),
		WithNow(func() time.Time {
			return time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
		}),
	)

	memorize, err := svc.Method("memorize")
	require.NoError(t, err)
	list, err := svc.Method("list")
	require.NoError(t, err)
	fetch, err := svc.Method("fetch")
	require.NoError(t, err)

	memOut := &MemorizeOutput{}
	err = memorize(ctx, &MemorizeInput{
		Key:         "patch-contract",
		Description: "Patch contract notes",
		Body:        "Paths are resolved under workdir.",
	}, memOut)
	require.NoError(t, err)
	assert.Equal(t, "ok", memOut.Status)
	assert.Equal(t, "patch-contract", memOut.Key)
	assert.Equal(t, "2026-05-04T12:00:00Z", memOut.UpdatedAt)

	listOut := &ListOutput{}
	err = list(ctx, &ListInput{}, listOut)
	require.NoError(t, err)
	require.Equal(t, "ok", listOut.Status)
	require.EqualValues(t, []Entry{{
		Key:         "patch-contract",
		Description: "Patch contract notes",
		UpdatedAt:   "2026-05-04T12:00:00Z",
	}}, listOut.Entries)

	fetchOut := &FetchOutput{}
	err = fetch(ctx, &FetchInput{Key: "patch-contract"}, fetchOut)
	require.NoError(t, err)
	assert.Equal(t, "ok", fetchOut.Status)
	assert.Equal(t, "Paths are resolved under workdir.", fetchOut.Body)

	otherListOut := &ListOutput{}
	err = list(otherCtx, &ListInput{}, otherListOut)
	require.NoError(t, err)
	assert.Equal(t, "ok", otherListOut.Status)
	assert.Empty(t, otherListOut.Entries)

	otherFetchOut := &FetchOutput{}
	err = fetch(otherCtx, &FetchInput{Key: "patch-contract"}, otherFetchOut)
	require.NoError(t, err)
	assert.Equal(t, "error", otherFetchOut.Status)
	assert.Contains(t, otherFetchOut.Error, "not found")
}

func TestService_MemorizeOverwritesExactKeyWithoutDuplicate(t *testing.T) {
	ctx := userCtx("alice@example.com")
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	svc := New(
		WithRootURI("mem://localhost/scratchpad_overwrite/${userID}"),
		WithNow(func() time.Time {
			now = now.Add(time.Minute)
			return now
		}),
	)
	memorize, err := svc.Method("memorize")
	require.NoError(t, err)
	list, err := svc.Method("list")
	require.NoError(t, err)
	fetch, err := svc.Method("fetch")
	require.NoError(t, err)

	first := &MemorizeOutput{}
	require.NoError(t, memorize(ctx, &MemorizeInput{
		Key:         "same-key",
		Description: "Original",
		Body:        "first body",
	}, first))
	second := &MemorizeOutput{}
	require.NoError(t, memorize(ctx, &MemorizeInput{
		Key:         "same-key",
		Description: "Updated",
		Body:        "second body",
	}, second))

	listOut := &ListOutput{}
	require.NoError(t, list(ctx, &ListInput{}, listOut))
	require.Equal(t, "ok", listOut.Status)
	require.EqualValues(t, []Entry{{
		Key:         "same-key",
		Description: "Updated",
		UpdatedAt:   second.UpdatedAt,
	}}, listOut.Entries)
	assert.NotEqual(t, first.UpdatedAt, second.UpdatedAt)

	fetchOut := &FetchOutput{}
	require.NoError(t, fetch(ctx, &FetchInput{Key: "same-key"}, fetchOut))
	assert.Equal(t, "second body", fetchOut.Body)
	assert.Equal(t, "Updated", fetchOut.Description)
	assert.Equal(t, second.UpdatedAt, fetchOut.UpdatedAt)
}

func TestService_AppendCreatesAndAppendsBodyOnly(t *testing.T) {
	ctx := userCtx("alice@example.com")
	otherCtx := userCtx("bob@example.com")
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	svc := New(
		WithRootURI("mem://localhost/scratchpad_append/${userID}"),
		WithNow(func() time.Time {
			now = now.Add(time.Minute)
			return now
		}),
	)
	appendNote, err := svc.Method("append")
	require.NoError(t, err)
	list, err := svc.Method("list")
	require.NoError(t, err)
	fetch, err := svc.Method("fetch")
	require.NoError(t, err)

	first := &AppendOutput{}
	require.NoError(t, appendNote(ctx, &AppendInput{
		Key:  "daily-log",
		Body: "first entry",
	}, first))
	require.Equal(t, "ok", first.Status)
	assert.True(t, first.Created)
	assert.Equal(t, "daily-log", first.Description)

	second := &AppendOutput{}
	require.NoError(t, appendNote(ctx, &AppendInput{
		Key:  "daily-log",
		Body: "\nsecond entry",
	}, second))
	require.Equal(t, "ok", second.Status)
	assert.False(t, second.Created)
	assert.Equal(t, "daily-log", second.Description)
	assert.NotEqual(t, first.UpdatedAt, second.UpdatedAt)

	fetchOut := &FetchOutput{}
	require.NoError(t, fetch(ctx, &FetchInput{Key: "daily-log"}, fetchOut))
	require.Equal(t, "ok", fetchOut.Status)
	assert.Equal(t, "first entry\n\n---\n\nsecond entry", fetchOut.Body)
	assert.Equal(t, "daily-log", fetchOut.Description)
	assert.Equal(t, second.UpdatedAt, fetchOut.UpdatedAt)

	listOut := &ListOutput{}
	require.NoError(t, list(ctx, &ListInput{}, listOut))
	require.Equal(t, "ok", listOut.Status)
	require.EqualValues(t, []Entry{{
		Key:         "daily-log",
		Description: "daily-log",
		UpdatedAt:   second.UpdatedAt,
	}}, listOut.Entries)

	otherFetchOut := &FetchOutput{}
	require.NoError(t, fetch(otherCtx, &FetchInput{Key: "daily-log"}, otherFetchOut))
	assert.Equal(t, "error", otherFetchOut.Status)
	assert.Contains(t, otherFetchOut.Error, "not found")
}

func TestService_AppendPreservesOrUpdatesDescriptionMetadata(t *testing.T) {
	ctx := userCtx("alice@example.com")
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	svc := New(
		WithRootURI("mem://localhost/scratchpad_append_description/${userID}"),
		WithNow(func() time.Time {
			now = now.Add(time.Minute)
			return now
		}),
	)
	memorize, err := svc.Method("memorize")
	require.NoError(t, err)
	appendNote, err := svc.Method("append")
	require.NoError(t, err)
	fetch, err := svc.Method("fetch")
	require.NoError(t, err)

	memOut := &MemorizeOutput{}
	require.NoError(t, memorize(ctx, &MemorizeInput{
		Key:         "decisions",
		Description: "Original description",
		Body:        "first",
	}, memOut))

	withoutDescription := &AppendOutput{}
	require.NoError(t, appendNote(ctx, &AppendInput{
		Key:  "decisions",
		Body: "second",
	}, withoutDescription))
	require.Equal(t, "ok", withoutDescription.Status)
	assert.Equal(t, "Original description", withoutDescription.Description)

	withDescription := &AppendOutput{}
	require.NoError(t, appendNote(ctx, &AppendInput{
		Key:         "decisions",
		Body:        "third",
		Description: "Updated description",
	}, withDescription))
	require.Equal(t, "ok", withDescription.Status)
	assert.Equal(t, "Updated description", withDescription.Description)

	fetchOut := &FetchOutput{}
	require.NoError(t, fetch(ctx, &FetchInput{Key: "decisions"}, fetchOut))
	require.Equal(t, "ok", fetchOut.Status)
	assert.Equal(t, "Updated description", fetchOut.Description)
	assert.Equal(t, "first\n\n---\n\nsecond\n\n---\n\nthird", fetchOut.Body)
	assert.Equal(t, withDescription.UpdatedAt, fetchOut.UpdatedAt)
}

func TestService_AppendUsesExactKeyIdentityNotPath(t *testing.T) {
	ctx := userCtx("alice@example.com")
	fs := afs.New()
	rootTemplate := "mem://localhost/scratchpad_append_key_identity/${userID}"
	svc := New(WithAFS(fs), WithRootURI(rootTemplate))
	appendNote, err := svc.Method("append")
	require.NoError(t, err)
	fetch, err := svc.Method("fetch")
	require.NoError(t, err)

	key := "../nested/../../append.md"
	out := &AppendOutput{}
	require.NoError(t, appendNote(ctx, &AppendInput{
		Key:  key,
		Body: "stored by exact key",
	}, out))
	assert.Equal(t, key, out.Key)
	assert.Equal(t, key, out.Description)

	root, _, err := svc.resolveRootURI(ctx)
	require.NoError(t, err)
	objects, err := fs.List(ctx, root)
	require.NoError(t, err)
	var noteNames []string
	for _, object := range objects {
		if object != nil && !object.IsDir() && strings.HasSuffix(object.Name(), ".json") {
			noteNames = append(noteNames, object.Name())
		}
	}
	require.Len(t, noteNames, 1)
	assert.NotContains(t, noteNames[0], "append")
	assert.NotContains(t, noteNames[0], "..")

	fetchOut := &FetchOutput{}
	require.NoError(t, fetch(ctx, &FetchInput{Key: key}, fetchOut))
	assert.Equal(t, "ok", fetchOut.Status)
	assert.Equal(t, key, fetchOut.Key)
	assert.Equal(t, "stored by exact key", fetchOut.Body)
}

func TestService_KeyIsExactIdentityNotPath(t *testing.T) {
	ctx := userCtx("alice@example.com")
	fs := afs.New()
	rootTemplate := "mem://localhost/scratchpad_key_identity/${userID}"
	svc := New(WithAFS(fs), WithRootURI(rootTemplate))
	memorize, err := svc.Method("memorize")
	require.NoError(t, err)
	fetch, err := svc.Method("fetch")
	require.NoError(t, err)

	key := "../nested/../../escape.md"
	out := &MemorizeOutput{}
	require.NoError(t, memorize(ctx, &MemorizeInput{
		Key:         key,
		Description: "Path-shaped key",
		Body:        "stored by exact key",
	}, out))
	assert.Equal(t, key, out.Key)

	root, _, err := svc.resolveRootURI(ctx)
	require.NoError(t, err)
	objects, err := fs.List(ctx, root)
	require.NoError(t, err)
	var noteNames []string
	for _, object := range objects {
		if object != nil && !object.IsDir() && strings.HasSuffix(object.Name(), ".json") {
			noteNames = append(noteNames, object.Name())
		}
	}
	require.Len(t, noteNames, 1)
	assert.NotContains(t, noteNames[0], "escape")
	assert.NotContains(t, noteNames[0], "..")

	fetchOut := &FetchOutput{}
	require.NoError(t, fetch(ctx, &FetchInput{Key: key}, fetchOut))
	assert.Equal(t, "ok", fetchOut.Status)
	assert.Equal(t, key, fetchOut.Key)
	assert.Equal(t, "stored by exact key", fetchOut.Body)
}

func TestService_ListSortsAndFiltersStoredObjects(t *testing.T) {
	ctx := userCtx("alice@example.com")
	fs := afs.New()
	svc := New(WithAFS(fs), WithRootURI("mem://localhost/scratchpad_list/${userID}"))
	memorize, err := svc.Method("memorize")
	require.NoError(t, err)
	list, err := svc.Method("list")
	require.NoError(t, err)

	for _, input := range []MemorizeInput{
		{Key: "zeta", Description: "Z note", Body: "z"},
		{Key: "alpha", Description: "A note", Body: "a"},
		{Key: "middle", Description: "M note", Body: "m"},
	} {
		out := &MemorizeOutput{}
		require.NoError(t, memorize(ctx, &input, out))
		require.Equal(t, "ok", out.Status)
	}
	root, _, err := svc.resolveRootURI(ctx)
	require.NoError(t, err)
	require.NoError(t, fs.Upload(ctx, root+"/ignored.txt", 0o644, strings.NewReader("ignore")))
	require.NoError(t, fs.Create(ctx, root+"/ignored_dir", 0o755, true))

	out := &ListOutput{}
	require.NoError(t, list(ctx, &ListInput{}, out))

	require.Equal(t, "ok", out.Status)
	require.Len(t, out.Entries, 3)
	assert.Equal(t, "alpha", out.Entries[0].Key)
	assert.Equal(t, "middle", out.Entries[1].Key)
	assert.Equal(t, "zeta", out.Entries[2].Key)
}

func TestService_FetchRejectsStoredIdentityMismatch(t *testing.T) {
	ctx := userCtx("alice@example.com")
	fs := afs.New()
	svc := New(WithAFS(fs), WithRootURI("mem://localhost/scratchpad_mismatch/${userID}"))
	fetch, err := svc.Method("fetch")
	require.NoError(t, err)

	root, _, err := svc.resolveRootURI(ctx)
	require.NoError(t, err)
	require.NoError(t, fs.Create(ctx, root, 0o755, true))
	raw, err := json.Marshal(noteFile{
		Key:         "other",
		Description: "Wrong note",
		Body:        "wrong body",
		UserID:      "alice@example.com",
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	})
	require.NoError(t, err)
	require.NoError(t, fs.Upload(ctx, noteURL(root, "wanted"), 0o644, strings.NewReader(string(raw))))

	out := &FetchOutput{}
	require.NoError(t, fetch(ctx, &FetchInput{Key: "wanted"}, out))

	assert.Equal(t, "error", out.Status)
	assert.Contains(t, out.Error, "identity mismatch")
	assertNoStoragePathLeak(t, out.Error)
}

func TestService_ValidationErrors(t *testing.T) {
	ctx := userCtx("alice@example.com")
	svc := New(WithRootURI("mem://localhost/scratchpad_validation/${userID}"))
	memorize, err := svc.Method("memorize")
	require.NoError(t, err)
	appendNote, err := svc.Method("append")
	require.NoError(t, err)
	fetch, err := svc.Method("fetch")
	require.NoError(t, err)

	tests := []struct {
		name string
		in   MemorizeInput
		want string
	}{
		{name: "missing key", in: MemorizeInput{Description: "d", Body: "b"}, want: "key is required"},
		{name: "blank key", in: MemorizeInput{Key: " \t ", Description: "d", Body: "b"}, want: "key is required"},
		{name: "missing description", in: MemorizeInput{Key: "k", Body: "b"}, want: "description is required"},
		{name: "blank description", in: MemorizeInput{Key: "k", Description: " \t ", Body: "b"}, want: "description is required"},
		{name: "missing body", in: MemorizeInput{Key: "k", Description: "d"}, want: "body is required"},
		{name: "blank body", in: MemorizeInput{Key: "k", Description: "d", Body: " \n\t "}, want: "body is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &MemorizeOutput{}
			require.NoError(t, memorize(ctx, &tt.in, out))
			assert.Equal(t, "error", out.Status)
			assert.Contains(t, out.Error, tt.want)
			assertNoStoragePathLeak(t, out.Error)
		})
	}

	fetchOut := &FetchOutput{}
	require.NoError(t, fetch(ctx, &FetchInput{Key: " \n\t "}, fetchOut))
	assert.Equal(t, "error", fetchOut.Status)
	assert.Contains(t, fetchOut.Error, "key is required")
	assertNoStoragePathLeak(t, fetchOut.Error)

	appendTests := []struct {
		name string
		in   AppendInput
		want string
	}{
		{name: "missing key", in: AppendInput{Body: "b"}, want: "key is required"},
		{name: "blank key", in: AppendInput{Key: " \t ", Body: "b"}, want: "key is required"},
		{name: "missing body", in: AppendInput{Key: "k"}, want: "body is required"},
		{name: "blank body", in: AppendInput{Key: "k", Body: " \n\t "}, want: "body is required"},
	}
	for _, tt := range appendTests {
		t.Run("append "+tt.name, func(t *testing.T) {
			out := &AppendOutput{}
			require.NoError(t, appendNote(ctx, &tt.in, out))
			assert.Equal(t, "error", out.Status)
			assert.Contains(t, out.Error, tt.want)
			assertNoStoragePathLeak(t, out.Error)
		})
	}
}

func TestService_MethodAndTypeValidation(t *testing.T) {
	svc := New()

	_, err := svc.Method("unknown")
	require.Error(t, err)

	memorize, err := svc.Method("MEMORIZE")
	require.NoError(t, err)
	appendNote, err := svc.Method("APPEND")
	require.NoError(t, err)
	list, err := svc.Method("LIST")
	require.NoError(t, err)
	fetch, err := svc.Method("FETCH")
	require.NoError(t, err)

	require.Error(t, memorize(context.Background(), &ListInput{}, &MemorizeOutput{}))
	require.Error(t, memorize(context.Background(), &MemorizeInput{}, &ListOutput{}))
	require.Error(t, appendNote(context.Background(), &ListInput{}, &AppendOutput{}))
	require.Error(t, appendNote(context.Background(), &AppendInput{}, &ListOutput{}))
	require.Error(t, list(context.Background(), &FetchInput{}, &ListOutput{}))
	require.Error(t, list(context.Background(), &ListInput{}, &FetchOutput{}))
	require.Error(t, fetch(context.Background(), &ListInput{}, &FetchOutput{}))
	require.Error(t, fetch(context.Background(), &FetchInput{}, &ListOutput{}))
}

func TestResolveRootURI_DefaultExpandsSanitizedUserID(t *testing.T) {
	ctx := userCtx("team/user@example.com")

	root, userID, err := ResolveRootURI(ctx, "")

	require.NoError(t, err)
	assert.Equal(t, "team/user@example.com", userID)
	assert.Equal(t, "mem://localhost/scratchpad/team_user@example.com", root)
}

func TestResolveRootURI_DefaultDoesNotBootstrapWorkspace(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })
	tempDir := t.TempDir()
	require.NoError(t, os.Chdir(tempDir))
	t.Setenv("AGENTLY_WORKSPACE", "")

	root, _, err := ResolveRootURI(userCtx("alice"), "")

	require.NoError(t, err)
	assert.Equal(t, "mem://localhost/scratchpad/alice", root)
	_, err = os.Stat(filepath.Join(tempDir, ".agently"))
	assert.True(t, os.IsNotExist(err), "default mem scratchpad must not create a workspace")
}

func TestResolveRootURI_RequiresUserBoundTemplate(t *testing.T) {
	ctx := userCtx("alice")

	_, _, err := ResolveRootURI(ctx, "mem://localhost/scratchpad")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must include ${userID} or ${user}")
}

func TestResolveRootURI_RequiresUsableUserSegment(t *testing.T) {
	ctx := userCtx("///")

	_, _, err := ResolveRootURI(ctx, "mem://localhost/scratchpad/${userID}")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "effective user id cannot be converted")
	assertNoStoragePathLeak(t, err.Error())
}

func TestResolveRootURI_UsesEmailFallbackAndUserAlias(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Email: "team/user@example.com"})

	root, userID, err := ResolveRootURI(ctx, "mem://localhost/scratchpad/${user}")

	require.NoError(t, err)
	assert.Equal(t, "team/user@example.com", userID)
	assert.Equal(t, "mem://localhost/scratchpad/team_user@example.com", root)
}

func TestResolveRootURI_ExpandsHomeTemplateWithoutWorkspaceBootstrap(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })
	tempDir := t.TempDir()
	require.NoError(t, os.Chdir(tempDir))
	t.Setenv("AGENTLY_WORKSPACE", "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	root, _, err := ResolveRootURI(userCtx("alice"), "${home}/scratchpad/${userID}")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "scratchpad", "alice"), root)
	_, err = os.Stat(filepath.Join(tempDir, ".agently"))
	assert.True(t, os.IsNotExist(err), "home-only scratchpad template must not create a workspace")
}

func TestService_RequiresEffectiveUserID(t *testing.T) {
	svc := New(WithRootURI("mem://localhost/scratchpad_test/${userID}"))
	list, err := svc.Method("list")
	require.NoError(t, err)
	appendNote, err := svc.Method("append")
	require.NoError(t, err)

	out := &ListOutput{}
	err = list(context.Background(), &ListInput{}, out)

	require.NoError(t, err)
	assert.Equal(t, "error", out.Status)
	assert.Contains(t, out.Error, "effective user id")

	appendOut := &AppendOutput{}
	err = appendNote(context.Background(), &AppendInput{Key: "k", Body: "b"}, appendOut)

	require.NoError(t, err)
	assert.Equal(t, "error", appendOut.Status)
	assert.Contains(t, appendOut.Error, "effective user id")
}

func TestService_EnvScratchpadURIOverridesServiceTemplate(t *testing.T) {
	t.Setenv(EnvScratchpadURI, "mem://localhost/env_scratchpad/${userID}")
	ctx := userCtx("env-user")
	svc := New(WithRootURI("mem://localhost/service_scratchpad/${userID}"))

	root, userID, err := svc.resolveRootURI(ctx)

	require.NoError(t, err)
	assert.Equal(t, "env-user", userID)
	assert.Equal(t, "mem://localhost/env_scratchpad/env-user", root)
}

func TestService_EnvScratchpadURIStillRequiresUserBoundTemplate(t *testing.T) {
	t.Setenv(EnvScratchpadURI, "mem://localhost/not_user_bound")
	ctx := userCtx("env-user")
	svc := New(WithRootURI("mem://localhost/service_scratchpad/${userID}"))
	list, err := svc.Method("list")
	require.NoError(t, err)

	out := &ListOutput{}
	require.NoError(t, list(ctx, &ListInput{}, out))

	assert.Equal(t, "error", out.Status)
	assert.Contains(t, out.Error, "must include ${userID} or ${user}")
	assertNoStoragePathLeak(t, out.Error)
}

func TestService_ErrorsDoNotExposeResolvedStoragePath(t *testing.T) {
	ctx := userCtx("alice@example.com")
	rootTemplate := "file://localhost" + strings.TrimRight(t.TempDir(), "/") + "/scratchpad/${userID}"
	svc := New(WithRootURI(rootTemplate))
	fetch, err := svc.Method("fetch")
	require.NoError(t, err)
	list, err := svc.Method("list")
	require.NoError(t, err)

	fetchOut := &FetchOutput{}
	err = fetch(ctx, &FetchInput{Key: "missing"}, fetchOut)
	require.NoError(t, err)
	assert.Equal(t, "error", fetchOut.Status)
	assert.NotContains(t, fetchOut.Error, "file://")
	assert.NotContains(t, fetchOut.Error, "/")

	root, _, err := svc.resolveRootURI(ctx)
	require.NoError(t, err)
	require.NoError(t, svc.fs.Create(ctx, root, 0o755, true))
	require.NoError(t, svc.fs.Upload(ctx, root+"/bad.json", 0o644, strings.NewReader("{")))

	listOut := &ListOutput{}
	err = list(ctx, &ListInput{}, listOut)
	require.NoError(t, err)
	assert.Equal(t, "error", listOut.Status)
	assert.NotContains(t, listOut.Error, root)
	assert.NotContains(t, listOut.Error, "file://")
}

func TestService_WriteFailureDoesNotExposeResolvedStoragePath(t *testing.T) {
	ctx := userCtx("alice@example.com")
	rootTemplate := filepath.Join(t.TempDir(), "scratchpad", "${userID}")
	svc := New(WithRootURI(rootTemplate))
	memorize, err := svc.Method("memorize")
	require.NoError(t, err)
	appendNote, err := svc.Method("append")
	require.NoError(t, err)
	root, _, err := svc.resolveRootURI(ctx)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(root), 0o755))
	require.NoError(t, os.WriteFile(root, []byte("not a directory"), 0o644))

	out := &MemorizeOutput{}
	require.NoError(t, memorize(ctx, &MemorizeInput{
		Key:         "k",
		Description: "d",
		Body:        "b",
	}, out))

	assert.Equal(t, "error", out.Status)
	assertNoStoragePathLeak(t, out.Error)

	appendOut := &AppendOutput{}
	require.NoError(t, appendNote(ctx, &AppendInput{
		Key:  "k",
		Body: "b",
	}, appendOut))

	assert.Equal(t, "error", appendOut.Status)
	assertNoStoragePathLeak(t, appendOut.Error)
}

func assertNoStoragePathLeak(t *testing.T, value string) {
	t.Helper()
	assert.NotContains(t, value, "file://")
	assert.NotContains(t, value, "mem://")
	assert.NotContains(t, value, "/var/")
	assert.NotContains(t, value, "/tmp/")
	assert.NotContains(t, value, "/Users/")
}

func userCtx(userID string) context.Context {
	return authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: userID})
}
