package scratchpad

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/viant/afs"
	afsscratchpad "github.com/viant/afs/scratchpad"
	authctx "github.com/viant/agently-core/internal/auth"
	svc "github.com/viant/agently-core/protocol/tool/service"
	"github.com/viant/agently-core/workspace"
)

const (
	Name                   = "scratchpad"
	EnvScratchpadURI       = "AGENTLY_SCRATCHPAD_URI"
	DefaultRootURITemplate = afsscratchpad.DefaultRootURITemplate
)

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
	result, err := s.client(ctx).Memorize(ctx, input)
	if err != nil {
		output.Status = "error"
		output.Error = err.Error()
		return nil
	}
	*output = *result
	output.Status = "ok"
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
	result, err := s.client(ctx).Append(ctx, input)
	if err != nil {
		output.Status = "error"
		output.Error = err.Error()
		return nil
	}
	*output = *result
	output.Status = "ok"
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
	result, err := s.client(ctx).List(ctx)
	if err != nil {
		output.Status = "error"
		output.Error = err.Error()
		return nil
	}
	*output = *result
	output.Status = "ok"
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
	result, err := s.client(ctx).Fetch(ctx, input.Key)
	if err != nil {
		output.Status = "error"
		output.Error = err.Error()
		return nil
	}
	*output = *result
	output.Status = "ok"
	return nil
}

func (s *Service) client(ctx context.Context) *afsscratchpad.Service {
	template := s.effectiveRootTemplate()
	options := []afsscratchpad.Option{
		afsscratchpad.WithAFS(s.fs),
		afsscratchpad.WithRootURI(template),
		afsscratchpad.WithUserID(authctx.EffectiveUserID(ctx)),
		afsscratchpad.WithNow(s.now),
	}
	basePath, macros := workspaceBindingsForTemplate(template)
	if basePath != "" {
		options = append(options, afsscratchpad.WithBasePath(basePath))
	}
	if len(macros) > 0 {
		options = append(options, afsscratchpad.WithMacros(macros))
	}
	return afsscratchpad.New(options...)
}

func (s *Service) resolveRootURI(ctx context.Context) (string, string, error) {
	return ResolveRootURI(ctx, s.effectiveRootTemplate())
}

func (s *Service) effectiveRootTemplate() string {
	template := strings.TrimSpace(os.Getenv(EnvScratchpadURI))
	if template == "" {
		template = strings.TrimSpace(s.rootTemplate)
	}
	return template
}

func ResolveRootURI(ctx context.Context, template string) (string, string, error) {
	userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if strings.TrimSpace(template) == "" {
		template = DefaultRootURITemplate
	}
	basePath, macros := workspaceBindingsForTemplate(template)
	root, resolvedUserID, err := afsscratchpad.ResolveRootURI(template, userID, basePath, macros)
	if err != nil {
		if strings.Contains(err.Error(), "root template must include") {
			return "", "", fmt.Errorf("%s template must include ${userID} or ${user}", EnvScratchpadURI)
		}
		return "", "", err
	}
	return root, resolvedUserID, nil
}

func workspaceBindingsForTemplate(template string) (string, map[string]string) {
	template = strings.TrimSpace(template)
	if template == "" {
		template = DefaultRootURITemplate
	}
	macros := map[string]string{}
	var workspaceRoot string
	if needsWorkspaceRoot(template) {
		workspaceRoot = workspace.Root()
	}
	if needsWorkspaceBasePath(template) {
		if workspaceRoot == "" {
			workspaceRoot = workspace.Root()
		}
		returnBasePath := workspaceRoot
		if strings.Contains(template, "${workspaceRoot}") {
			macros["workspaceRoot"] = workspaceRoot
		}
		if strings.Contains(template, "${runtimeRoot}") {
			macros["runtimeRoot"] = workspace.RuntimeRoot()
		}
		if len(macros) == 0 {
			return returnBasePath, nil
		}
		return returnBasePath, macros
	}
	if strings.Contains(template, "${workspaceRoot}") {
		macros["workspaceRoot"] = workspaceRoot
	}
	if strings.Contains(template, "${runtimeRoot}") {
		macros["runtimeRoot"] = workspace.RuntimeRoot()
	}
	if len(macros) == 0 {
		return "", nil
	}
	return "", macros
}

func needsWorkspaceRoot(template string) bool {
	return needsWorkspaceBasePath(template) || strings.Contains(template, "${workspaceRoot}")
}

func needsWorkspaceBasePath(template string) bool {
	template = strings.TrimSpace(template)
	if template == "" {
		template = DefaultRootURITemplate
	}
	if strings.Contains(template, "://") || filepath.IsAbs(template) || isWindowsAbsPath(template) {
		return false
	}
	if strings.HasPrefix(template, "${home}") || strings.HasPrefix(template, "~/") || template == "~" {
		return false
	}
	if strings.HasPrefix(template, "${workspaceRoot}") || strings.HasPrefix(template, "${runtimeRoot}") {
		return false
	}
	return true
}

func noteURL(root, key string) string {
	return afsscratchpad.NoteURL(root, key)
}

func appendNoteBody(existing, addition string) string {
	return afsscratchpad.AppendNoteBody(existing, addition)
}

func sanitizePathComponent(value string) string {
	return afsscratchpad.SanitizePathComponent(value)
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
