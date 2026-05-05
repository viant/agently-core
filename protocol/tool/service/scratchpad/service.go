package scratchpad

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/viant/afs"
	afsurl "github.com/viant/afs/url"
	authctx "github.com/viant/agently-core/internal/auth"
	svc "github.com/viant/agently-core/protocol/tool/service"
	"github.com/viant/agently-core/workspace"
)

const (
	Name                   = "scratchpad"
	EnvScratchpadURI       = "AGENTLY_SCRATCHPAD_URI"
	DefaultRootURITemplate = "mem://localhost/scratchpad/${userID}"
	appendSeparator        = "\n\n---\n\n"
)

var errScratchpadNoteNotFound = errors.New("scratchpad note not found")

type Service struct {
	fs           afs.Service
	rootTemplate string
	now          func() time.Time
}

type Option func(*Service)

func WithAFS(fs afs.Service) Option {
	return func(s *Service) {
		if fs != nil {
			s.fs = fs
		}
	}
}

func WithRootURI(template string) Option {
	return func(s *Service) {
		s.rootTemplate = strings.TrimSpace(template)
	}
}

func WithNow(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func New(opts ...Option) *Service {
	s := &Service{
		fs:           afs.New(),
		rootTemplate: DefaultRootURITemplate,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

func (s *Service) Name() string { return Name }

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{
			Name:        "memorize",
			Description: "Store or replace a user-scoped scratchpad note by exact key. Requires key, description, and body. Storage location is host-configured and is never returned.",
			Input:       reflect.TypeOf(&MemorizeInput{}),
			Output:      reflect.TypeOf(&MemorizeOutput{}),
		},
		{
			Name:        "append",
			Description: "Append body text to a user-scoped scratchpad note by exact key. Description is note-level metadata only: existing notes keep it unless provided; new notes default it to key when omitted.",
			Input:       reflect.TypeOf(&AppendInput{}),
			Output:      reflect.TypeOf(&AppendOutput{}),
		},
		{
			Name:        "list",
			Description: "List the current user's scratchpad note keys with descriptions. Does not return note bodies.",
			Input:       reflect.TypeOf(&ListInput{}),
			Output:      reflect.TypeOf(&ListOutput{}),
		},
		{
			Name:        "fetch",
			Description: "Fetch a user-scoped scratchpad note body by exact key.",
			Input:       reflect.TypeOf(&FetchInput{}),
			Output:      reflect.TypeOf(&FetchOutput{}),
		},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "memorize":
		return s.memorize, nil
	case "append":
		return s.append, nil
	case "list":
		return s.list, nil
	case "fetch":
		return s.fetch, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

func (s *Service) memorize(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*MemorizeInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*MemorizeOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	output.Status = "ok"
	if err := s.memorizeNote(ctx, input, output); err != nil {
		output.Status = "error"
		output.Error = err.Error()
	}
	return nil
}

func (s *Service) append(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*AppendInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*AppendOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	output.Status = "ok"
	if err := s.appendNote(ctx, input, output); err != nil {
		output.Status = "error"
		output.Error = err.Error()
	}
	return nil
}

func (s *Service) list(ctx context.Context, in, out interface{}) error {
	if _, ok := in.(*ListInput); !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ListOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	output.Status = "ok"
	if err := s.listNotes(ctx, output); err != nil {
		output.Status = "error"
		output.Error = err.Error()
	}
	return nil
}

func (s *Service) fetch(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*FetchInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*FetchOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	output.Status = "ok"
	if err := s.fetchNote(ctx, input, output); err != nil {
		output.Status = "error"
		output.Error = err.Error()
	}
	return nil
}

func (s *Service) memorizeNote(ctx context.Context, input *MemorizeInput, output *MemorizeOutput) error {
	key := strings.TrimSpace(input.Key)
	description := strings.TrimSpace(input.Description)
	body := strings.TrimSpace(input.Body)
	if key == "" {
		return fmt.Errorf("key is required")
	}
	if description == "" {
		return fmt.Errorf("description is required")
	}
	if body == "" {
		return fmt.Errorf("body is required")
	}
	root, userID, err := s.resolveRootURI(ctx)
	if err != nil {
		return err
	}
	if err = s.ensureRoot(ctx, root); err != nil {
		return fmt.Errorf("scratchpad storage unavailable")
	}
	updatedAt := s.now().UTC().Format(time.RFC3339Nano)
	note := noteFile{
		Key:         key,
		Description: description,
		Body:        input.Body,
		UserID:      userID,
		UpdatedAt:   updatedAt,
	}
	if err = s.writeNote(ctx, root, key, note); err != nil {
		return err
	}
	output.Key = key
	output.Description = description
	output.UpdatedAt = updatedAt
	return nil
}

func (s *Service) appendNote(ctx context.Context, input *AppendInput, output *AppendOutput) error {
	key := strings.TrimSpace(input.Key)
	body := strings.TrimSpace(input.Body)
	if key == "" {
		return fmt.Errorf("key is required")
	}
	if body == "" {
		return fmt.Errorf("body is required")
	}
	root, userID, err := s.resolveRootURI(ctx)
	if err != nil {
		return err
	}
	if err = s.ensureRoot(ctx, root); err != nil {
		return fmt.Errorf("scratchpad storage unavailable")
	}
	target := noteURL(root, key)
	exists, err := s.fs.Exists(ctx, target)
	if err != nil {
		return fmt.Errorf("read scratchpad note failed")
	}
	description := strings.TrimSpace(input.Description)
	updatedAt := s.now().UTC().Format(time.RFC3339Nano)
	var note noteFile
	if exists {
		existing, err := s.readNoteURL(ctx, target)
		if err != nil {
			return err
		}
		if existing.Key != key {
			return fmt.Errorf("scratchpad note identity mismatch for key %q", key)
		}
		note = *existing
		note.Body = appendNoteBody(note.Body, input.Body)
		if description != "" {
			note.Description = description
		}
		if strings.TrimSpace(note.Description) == "" {
			note.Description = key
		}
	} else {
		output.Created = true
		if description == "" {
			description = key
		}
		note = noteFile{
			Key:         key,
			Description: description,
			Body:        input.Body,
			UserID:      userID,
		}
	}
	note.UserID = userID
	note.UpdatedAt = updatedAt
	if err = s.writeNote(ctx, root, key, note); err != nil {
		return err
	}
	output.Key = note.Key
	output.Description = note.Description
	output.UpdatedAt = note.UpdatedAt
	return nil
}

func (s *Service) listNotes(ctx context.Context, output *ListOutput) error {
	root, _, err := s.resolveRootURI(ctx)
	if err != nil {
		return err
	}
	if ok, err := s.fs.Exists(ctx, root); err != nil {
		return fmt.Errorf("list scratchpad notes failed")
	} else if !ok {
		output.Entries = []Entry{}
		return nil
	}
	objects, err := s.fs.List(ctx, root)
	if err != nil {
		if os.IsNotExist(err) {
			output.Entries = []Entry{}
			return nil
		}
		return fmt.Errorf("list scratchpad notes failed")
	}
	var entries []Entry
	for _, object := range objects {
		if object == nil || object.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(object.Name()), ".json") {
			continue
		}
		note, err := s.readNoteURL(ctx, object.URL())
		if err != nil {
			return err
		}
		entries = append(entries, Entry{
			Key:         note.Key,
			Description: note.Description,
			UpdatedAt:   note.UpdatedAt,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	output.Entries = entries
	return nil
}

func (s *Service) fetchNote(ctx context.Context, input *FetchInput, output *FetchOutput) error {
	key := strings.TrimSpace(input.Key)
	if key == "" {
		return fmt.Errorf("key is required")
	}
	root, _, err := s.resolveRootURI(ctx)
	if err != nil {
		return err
	}
	target := noteURL(root, key)
	exists, err := s.fs.Exists(ctx, target)
	if err != nil {
		return fmt.Errorf("read scratchpad note failed")
	}
	if !exists {
		return fmt.Errorf("scratchpad note %q not found", key)
	}
	note, err := s.readNoteURL(ctx, target)
	if err != nil {
		if errors.Is(err, errScratchpadNoteNotFound) {
			return fmt.Errorf("scratchpad note %q not found", key)
		}
		return err
	}
	if note.Key != key {
		return fmt.Errorf("scratchpad note identity mismatch for key %q", key)
	}
	output.Key = note.Key
	output.Description = note.Description
	output.Body = note.Body
	output.UpdatedAt = note.UpdatedAt
	return nil
}

func (s *Service) writeNote(ctx context.Context, root, key string, note noteFile) error {
	data, err := json.MarshalIndent(note, "", "  ")
	if err != nil {
		return err
	}
	target := noteURL(root, key)
	if err = s.fs.Upload(ctx, target, 0o644, strings.NewReader(string(data)+"\n")); err != nil {
		return fmt.Errorf("write scratchpad note %q failed", key)
	}
	return nil
}

func appendNoteBody(existing, addition string) string {
	if strings.TrimSpace(existing) == "" {
		return addition
	}
	return strings.TrimRight(existing, "\n") + appendSeparator + strings.TrimLeft(addition, "\n")
}

func (s *Service) readNoteURL(ctx context.Context, uri string) (*noteFile, error) {
	data, err := s.fs.DownloadWithURL(ctx, uri)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errScratchpadNoteNotFound
		}
		return nil, fmt.Errorf("read scratchpad note failed")
	}
	var note noteFile
	if err = json.Unmarshal(data, &note); err != nil {
		return nil, fmt.Errorf("decode scratchpad note failed: %w", err)
	}
	if strings.TrimSpace(note.Key) == "" {
		return nil, fmt.Errorf("scratchpad note is missing key")
	}
	return &note, nil
}

func (s *Service) ensureRoot(ctx context.Context, root string) error {
	if ok, err := s.fs.Exists(ctx, root); err != nil {
		return err
	} else if ok {
		return nil
	}
	return s.fs.Create(ctx, root, 0o755, true)
}

func (s *Service) resolveRootURI(ctx context.Context) (string, string, error) {
	template := strings.TrimSpace(os.Getenv(EnvScratchpadURI))
	if template == "" {
		template = strings.TrimSpace(s.rootTemplate)
	}
	return ResolveRootURI(ctx, template)
}

func ResolveRootURI(ctx context.Context, template string) (string, string, error) {
	userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if userID == "" {
		return "", "", fmt.Errorf("scratchpad requires an effective user id")
	}
	if strings.TrimSpace(template) == "" {
		template = DefaultRootURITemplate
	}
	if !strings.Contains(template, "${userID}") && !strings.Contains(template, "${user}") {
		return "", "", fmt.Errorf("%s template must include ${userID} or ${user}", EnvScratchpadURI)
	}
	safeUserID := sanitizePathComponent(userID)
	if safeUserID == "" {
		return "", "", fmt.Errorf("effective user id cannot be converted to a scratchpad path segment")
	}
	out := strings.ReplaceAll(template, "${userID}", safeUserID)
	out = strings.ReplaceAll(out, "${user}", safeUserID)
	out = expandHostPathMacros(out)
	out = strings.ReplaceAll(out, "${userID}", safeUserID)
	out = strings.ReplaceAll(out, "${user}", safeUserID)
	return normalizeRootURI(out), userID, nil
}

func expandHostPathMacros(value string) string {
	out := strings.TrimSpace(value)
	if strings.Contains(out, "${workspaceRoot}") {
		out = strings.ReplaceAll(out, "${workspaceRoot}", workspace.Root())
	}
	if strings.Contains(out, "${runtimeRoot}") {
		out = strings.ReplaceAll(out, "${runtimeRoot}", workspace.RuntimeRoot())
	}
	if strings.Contains(out, "${home}") {
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			out = strings.ReplaceAll(out, "${home}", home)
		}
	}
	if strings.HasPrefix(out, "~/") || out == "~" {
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			if out == "~" {
				out = home
			} else {
				out = filepath.Join(home, strings.TrimPrefix(out, "~/"))
			}
		}
	}
	return strings.TrimSpace(out)
}

func noteURL(root, key string) string {
	sum := sha256.Sum256([]byte(key))
	return afsurl.Join(root, hex.EncodeToString(sum[:])+".json")
}

var unsafePathComponent = regexp.MustCompile(`[^A-Za-z0-9_.@-]+`)

func sanitizePathComponent(value string) string {
	value = strings.TrimSpace(value)
	value = unsafePathComponent.ReplaceAllString(value, "_")
	value = strings.Trim(value, "._-")
	return value
}

func normalizeRootURI(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return root
	}
	if strings.Contains(root, "://") {
		return strings.TrimRight(root, "/")
	}
	if filepath.IsAbs(root) || isWindowsAbsPath(root) {
		return filepath.Clean(root)
	}
	return filepath.Clean(filepath.Join(workspace.Root(), root))
}

func isWindowsAbsPath(v string) bool {
	if len(v) < 2 {
		return false
	}
	if v[1] != ':' {
		return false
	}
	return (v[0] >= 'a' && v[0] <= 'z') || (v[0] >= 'A' && v[0] <= 'Z')
}
