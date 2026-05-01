package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/agently-core/app/executor"
	execconfig "github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/app/server"
	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	agentfinder "github.com/viant/agently-core/protocol/agent/finder"
	agentloader "github.com/viant/agently-core/protocol/agent/loader"
	skillproto "github.com/viant/agently-core/protocol/skill"
	agentsvc "github.com/viant/agently-core/service/agent"
	skillsvc "github.com/viant/agently-core/service/skill"
	"github.com/viant/agently-core/workspace"
	wsconfig "github.com/viant/agently-core/workspace/config"
	"github.com/viant/agently-core/workspace/service/meta"
	scyauth "github.com/viant/scy/auth"
	"github.com/viant/scy/auth/authorizer"
)

type queryClient interface {
	Query(ctx context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error)
}

type exitCoder interface {
	error
	ExitCode() int
}

type cliExitError struct {
	code int
	err  error
}

func (e *cliExitError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *cliExitError) ExitCode() int {
	if e == nil || e.code == 0 {
		return 1
	}
	return e.code
}

var buildQueryClient = func(ctx context.Context, workspaceRoot string) (queryClient, error) {
	_, backend, _, err := server.BuildWorkspaceRuntime(ctx, server.RuntimeOptions{WorkspaceRoot: workspaceRoot})
	if err != nil {
		return nil, err
	}
	return backend, nil
}

var buildQueryRuntime = func(ctx context.Context, workspaceRoot string, defaults *execconfig.Defaults) (*executor.Runtime, error) {
	rt, _, _, err := server.BuildWorkspaceRuntime(ctx, server.RuntimeOptions{
		WorkspaceRoot: workspaceRoot,
		Defaults:      defaults,
	})
	return rt, err
}

var authorizeQueryOOB = func(ctx context.Context, secretsURL, configURL string, scopes []string) (*scyauth.Token, error) {
	cmd := &authorizer.Command{
		AuthFlow:   "OOB",
		UsePKCE:    true,
		SecretsURL: strings.TrimSpace(secretsURL),
		Scopes:     scopes,
		OAuthConfig: authorizer.OAuthConfig{
			ConfigURL: strings.TrimSpace(configURL),
		},
	}
	oauthTok, err := authorizer.New().Authorize(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if oauthTok == nil {
		return nil, fmt.Errorf("oauth oob returned empty token")
	}
	st := &scyauth.Token{Token: *oauthTok}
	st.PopulateIDToken()
	return st, nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if coded, ok := err.(exitCoder); ok {
			os.Exit(coded.ExitCode())
		}
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agently skill <list|activate|add|diagnostics|show|validate> [options]")
	}
	switch strings.TrimSpace(args[0]) {
	case "skill":
		return runSkill(args[1:], stdout, stderr)
	case "query":
		return runQuery(args[1:], stdout)
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func runSkill(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agently skill <list|activate|add|diagnostics|show|validate> [options]")
	}
	switch strings.TrimSpace(args[0]) {
	case "list":
		return runSkillList(args[1:], stdout)
	case "activate":
		return runSkillActivate(args[1:], stdout)
	case "add":
		return runSkillAdd(args[1:], stdout, stderr)
	case "diagnostics":
		return runSkillDiagnostics(args[1:], stdout)
	case "show":
		return runSkillShow(args[1:], stdout)
	case "validate":
		return runSkillValidate(args[1:], stdout)
	default:
		return fmt.Errorf("unknown skill subcommand: %s", args[0])
	}
}

func runSkillList(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("skill list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	workspaceRoot := fs.String("workspace", "", "workspace root")
	agentID := fs.String("agent", "", "agent id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, agent, err := initSkillCLI(strings.TrimSpace(*workspaceRoot), strings.TrimSpace(*agentID), false)
	if err != nil {
		return err
	}
	var items []skillproto.Metadata
	if agent != nil {
		items, _ = svc.Visible(agent)
	} else {
		items = svc.ListAll()
	}
	for _, item := range items {
		if item.Name == "" {
			continue
		}
		if strings.TrimSpace(item.Description) == "" {
			fmt.Fprintln(stdout, item.Name)
			continue
		}
		fmt.Fprintf(stdout, "%s\t%s\n", item.Name, item.Description)
	}
	return nil
}

func runSkillActivate(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("skill activate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	workspaceRoot := fs.String("workspace", "", "workspace root")
	agentID := fs.String("agent", "", "agent id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("skill name is required")
	}
	name := strings.TrimSpace(rest[0])
	argText := strings.TrimSpace(strings.Join(rest[1:], " "))
	root, defaults, err := resolveWorkspace(strings.TrimSpace(*workspaceRoot))
	if err != nil {
		return err
	}
	rt, _, finder, err := server.BuildWorkspaceRuntime(context.Background(), server.RuntimeOptions{
		WorkspaceRoot: root,
		Defaults:      defaults,
	})
	if err != nil {
		return err
	}
	selectedAgent := strings.TrimSpace(*agentID)
	if selectedAgent == "" {
		selectedAgent = strings.TrimSpace(defaults.Agent)
	}
	if selectedAgent == "" {
		return fmt.Errorf("agent is required (set --agent or configure default.agent in workspace config)")
	}
	agent, err := finder.Find(context.Background(), selectedAgent)
	if err != nil {
		return err
	}
	svc := rt.Skills
	if svc == nil {
		return fmt.Errorf("skills runtime not available")
	}
	body, err := svc.Activate(agent, name, argText)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, body)
	return nil
}

func runSkillDiagnostics(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("skill diagnostics", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	workspaceRoot := fs.String("workspace", "", "workspace root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, _, err := initSkillCLI(strings.TrimSpace(*workspaceRoot), "", false)
	if err != nil {
		return err
	}
	for _, item := range svc.Diagnostics() {
		fmt.Fprintln(stdout, item)
	}
	return nil
}

func runSkillShow(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("skill show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	workspaceRoot := fs.String("workspace", "", "workspace root")
	agentID := fs.String("agent", "", "agent id")
	bodyOnly := fs.Bool("body", false, "print body only")
	pathOnly := fs.Bool("path", false, "print resolved SKILL.md path only")
	asJSON := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("skill show requires exactly one skill name")
	}
	name := strings.TrimSpace(fs.Arg(0))
	svc, agent, err := initSkillCLI(strings.TrimSpace(*workspaceRoot), strings.TrimSpace(*agentID), false)
	if err != nil {
		return err
	}
	item, err := svc.Resolve(agent, name)
	if err != nil {
		return err
	}
	if *pathOnly {
		fmt.Fprintln(stdout, item.Path)
		return nil
	}
	body := strings.TrimSpace(item.Body)
	references := listSkillFiles(item.Root, "references")
	assets := listSkillFiles(item.Root, "assets")
	scripts := listSkillFiles(item.Root, "scripts")
	if *bodyOnly {
		fmt.Fprintln(stdout, body)
		return nil
	}
	if *asJSON {
		return json.NewEncoder(stdout).Encode(map[string]interface{}{
			"name":           item.Frontmatter.Name,
			"description":    item.Frontmatter.Description,
			"license":        item.Frontmatter.License,
			"context":        item.Frontmatter.ContextMode(),
			"allowedTools":   item.Frontmatter.AllowedTools,
			"path":           item.Path,
			"root":           item.Root,
			"source":         item.Source,
			"body":           body,
			"references":     references,
			"assets":         assets,
			"scripts":        scripts,
			"rawFrontmatter": item.Frontmatter.Raw,
		})
	}
	if item.Frontmatter.Name != "" {
		fmt.Fprintf(stdout, "name:\t%s\n", item.Frontmatter.Name)
	}
	if strings.TrimSpace(item.Frontmatter.Description) != "" {
		fmt.Fprintf(stdout, "description:\t%s\n", item.Frontmatter.Description)
	}
	if strings.TrimSpace(item.Frontmatter.License) != "" {
		fmt.Fprintf(stdout, "license:\t%s\n", item.Frontmatter.License)
	}
	if mode := strings.TrimSpace(item.Frontmatter.ContextMode()); mode != "" && mode != "inline" {
		fmt.Fprintf(stdout, "context:\t%s\n", mode)
	}
	if strings.TrimSpace(item.Frontmatter.AllowedTools) != "" {
		fmt.Fprintf(stdout, "allowed-tools:\t%s\n", item.Frontmatter.AllowedTools)
	}
	fmt.Fprintf(stdout, "path:\t%s\n", item.Path)
	fmt.Fprintf(stdout, "source:\t%s\n", item.Source)
	if len(references) > 0 {
		fmt.Fprintf(stdout, "references:\t%s\n", strings.Join(references, ", "))
	}
	if len(assets) > 0 {
		fmt.Fprintf(stdout, "assets:\t%s\n", strings.Join(assets, ", "))
	}
	if len(scripts) > 0 {
		fmt.Fprintf(stdout, "scripts:\t%s\n", strings.Join(scripts, ", "))
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, body)
	return nil
}

// runSkillAdd installs a skill from a local source into the workspace's
// skill root or the user's home root. v1 supports local-path sources only;
// git-URL and curated-shorthand resolution come later. Validates the skill
// via the same skillproto.Parse path used by `skill validate`, requires
// explicit confirmation (or --yes), and writes a _SOURCE provenance file
// alongside the installed SKILL.md.
//
// Persistence path: filesystem write only — the existing fsnotify watcher
// (service/skill/watcher.go) detects the new directory and reloads the
// registry without restart. No install-side event emission.
func runSkillAdd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("skill add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	rootChoice := fs.String("root", "workspace", "install location: workspace | user")
	workspaceRoot := fs.String("workspace", "", "workspace root override (when --root=workspace)")
	nameOverride := fs.String("name", "", "override the skill directory name")
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: agently skill add <local-path> [--root user|workspace] [--name <name>] [--yes]")
	}
	source := strings.TrimSpace(fs.Arg(0))
	if source == "" {
		return fmt.Errorf("source path is required")
	}
	if strings.HasPrefix(source, "~/") {
		source = filepath.Join(mustUserHome(), source[2:])
	}
	srcInfo, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("source not found: %w", err)
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("source must be a skill directory containing SKILL.md, got file")
	}
	srcSkillMD := filepath.Join(source, "SKILL.md")
	srcData, err := os.ReadFile(srcSkillMD)
	if err != nil {
		return fmt.Errorf("source missing SKILL.md: %w", err)
	}

	// Validate via the canonical parser before touching the target tree.
	parsed, diags, err := skillproto.Parse(srcSkillMD, source, "workspace", string(srcData))
	if err != nil {
		return fmt.Errorf("invalid skill: %w", err)
	}
	if parsed == nil || strings.TrimSpace(parsed.Frontmatter.Name) == "" {
		return fmt.Errorf("invalid skill: missing frontmatter name")
	}
	for _, d := range diags {
		if strings.EqualFold(strings.TrimSpace(d.Level), "error") {
			return fmt.Errorf("skill failed validation: %s (%s)", d.Message, d.Path)
		}
	}

	skillName := strings.TrimSpace(parsed.Frontmatter.Name)
	if override := strings.TrimSpace(*nameOverride); override != "" {
		skillName = override
	}

	// Resolve install root.
	var targetRoot string
	switch strings.ToLower(strings.TrimSpace(*rootChoice)) {
	case "user":
		targetRoot = filepath.Join(mustUserHome(), ".agently", "skills")
	case "workspace", "":
		ws := strings.TrimSpace(*workspaceRoot)
		if ws == "" {
			resolved, _, err := resolveWorkspace("")
			if err != nil {
				return fmt.Errorf("resolve workspace: %w", err)
			}
			ws = resolved
		}
		targetRoot = filepath.Join(ws, "skills")
	default:
		return fmt.Errorf("--root must be 'workspace' or 'user', got %q", *rootChoice)
	}
	targetDir := filepath.Join(targetRoot, skillName)

	// Surface the trust-relevant frontmatter to the user before any write.
	fmt.Fprintf(stdout, "Installing skill %q from %s\n", skillName, source)
	fmt.Fprintf(stdout, "  Target:      %s\n", targetDir)
	fmt.Fprintf(stdout, "  Description: %s\n", strings.TrimSpace(parsed.Frontmatter.Description))
	if lic := strings.TrimSpace(parsed.Frontmatter.License); lic != "" {
		fmt.Fprintf(stdout, "  License:     %s\n", lic)
	}
	if at := strings.TrimSpace(parsed.Frontmatter.AllowedTools); at != "" {
		fmt.Fprintf(stdout, "  Allowed-tools: %s   *** review carefully ***\n", at)
	}
	if parsed.Frontmatter.PreprocessEnabled() {
		fmt.Fprintln(stdout, "  Preprocess:  ENABLED — body `!`-blocks will be expanded at activation time")
	}
	if mode := parsed.Frontmatter.ContextMode(); mode != "inline" {
		fmt.Fprintf(stdout, "  Context:     %s (Agently extension; runs in a child agent)\n", mode)
	}
	for _, d := range diags {
		fmt.Fprintf(stdout, "  diag (%s): %s\n", d.Level, d.Message)
	}
	if existing, err := os.Stat(targetDir); err == nil && existing != nil {
		fmt.Fprintf(stdout, "  Note: target directory already exists; install will overwrite.\n")
	}
	if !*yes {
		fmt.Fprint(stdout, "Proceed? [y/N]: ")
		var ans string
		fmt.Fscanln(os.Stdin, &ans)
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans != "y" && ans != "yes" {
			fmt.Fprintln(stdout, "aborted")
			return nil
		}
	}

	// Stage in temp dir, then atomic-ish move into place.
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir target root: %w", err)
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return fmt.Errorf("clean target dir: %w", err)
	}
	if err := copyDir(source, targetDir); err != nil {
		return fmt.Errorf("copy source: %w", err)
	}

	// Write _SOURCE provenance. Same shape used by portable test fixtures so
	// there is one provenance convention across vendored and installed skills.
	provenance := strings.Join([]string{
		"origin: " + source,
		"installed-by: agently skill add",
		"name: " + skillName,
		"workspace-root: " + filepath.Dir(targetDir),
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(targetDir, "_SOURCE"), []byte(provenance), 0o644); err != nil {
		fmt.Fprintf(stderr, "warning: install succeeded but _SOURCE write failed: %v\n", err)
	}

	fmt.Fprintf(stdout, "Installed %q to %s\n", skillName, targetDir)
	fmt.Fprintln(stdout, "The fsnotify watcher (when running) will reload the registry automatically.")
	return nil
}

// copyDir recursively copies a directory tree. Symlinks, special files,
// and permissions beyond mode bits are not preserved (skill folders are
// plain text + scripts; that suffices for v1).
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func runSkillValidate(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("skill validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	strict := fs.Bool("strict", false, "warn on extension fields")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("skill validate requires exactly one path")
	}
	target := strings.TrimSpace(fs.Arg(0))
	if strings.HasPrefix(target, "~/") {
		target = filepath.Join(mustUserHome(), target[2:])
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	skillDir := target
	skillPath := target
	if info.IsDir() {
		skillPath = filepath.Join(target, "SKILL.md")
	} else {
		skillDir = filepath.Dir(target)
	}
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return err
	}
	item, diags, err := skillproto.Parse(skillPath, skillDir, "workspace", string(data))
	if err != nil {
		fmt.Fprintf(stdout, "FAIL %s\n  - %v\n", skillDir, err)
		return fmt.Errorf("validation failed")
	}
	if item != nil && strings.TrimSpace(item.Frontmatter.Name) != "" && filepath.Base(skillDir) != strings.TrimSpace(item.Frontmatter.Name) {
		diags = append(diags, skillproto.Diagnostic{Level: "error", Message: "skill directory name must match frontmatter name", Path: skillPath})
	}
	if *strict && item != nil && len(item.Frontmatter.Raw) > 0 {
		keys := make([]string, 0, len(item.Frontmatter.Raw))
		for k := range item.Frontmatter.Raw {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(stdout, "WARN: unknown frontmatter field %q preserved as raw metadata\n", k)
		}
		return &cliExitError{code: 2, err: fmt.Errorf("strict validation produced warnings")}
	}
	var errs []skillproto.Diagnostic
	for _, d := range diags {
		if strings.EqualFold(strings.TrimSpace(d.Level), "error") {
			errs = append(errs, d)
		}
	}
	if len(errs) > 0 {
		fmt.Fprintf(stdout, "FAIL %s\n", skillDir)
		for _, d := range errs {
			fmt.Fprintf(stdout, "  - %s\n", d.Message)
		}
		return fmt.Errorf("validation failed")
	}
	fmt.Fprintf(stdout, "OK: %s\n", skillDir)
	return nil
}

func runQuery(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	workspaceRoot := fs.String("workspace", "", "workspace root")
	agentID := fs.String("agent", "", "agent id")
	modelID := fs.String("model", "", "model override")
	oobURL := fs.String("oob", "", "oauth OOB secrets URL")
	authURL := fs.String("auth", "", "oauth client config URL override")
	conversationID := fs.String("conversation", "", "conversation id")
	userID := fs.String("user", "cli", "user id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		return fmt.Errorf("query text is required")
	}
	root, defaults, err := resolveWorkspace(strings.TrimSpace(*workspaceRoot))
	if err != nil {
		return err
	}
	selectedAgent := strings.TrimSpace(*agentID)
	if selectedAgent == "" {
		selectedAgent = strings.TrimSpace(defaults.Agent)
	}
	if selectedAgent == "" {
		return fmt.Errorf("agent is required (set --agent or configure default.agent in workspace config)")
	}
	if strings.TrimSpace(*oobURL) != "" || strings.TrimSpace(*authURL) != "" {
		rt, err := buildQueryRuntime(context.Background(), root, defaults)
		if err != nil {
			return err
		}
		ctx := context.Background()
		if strings.TrimSpace(*oobURL) != "" {
			cfgURL := strings.TrimSpace(*authURL)
			if cfgURL == "" && rt.AuthConfig != nil && rt.AuthConfig.OAuth != nil && rt.AuthConfig.OAuth.Client != nil {
				cfgURL = strings.TrimSpace(rt.AuthConfig.OAuth.Client.ConfigURL)
			}
			if cfgURL == "" {
				return fmt.Errorf("oauth client configURL is required (set --auth or configure auth.oauth.client.configURL)")
			}
			scopes := []string{"openid"}
			if rt.AuthConfig != nil && rt.AuthConfig.OAuth != nil && rt.AuthConfig.OAuth.Client != nil && len(rt.AuthConfig.OAuth.Client.Scopes) > 0 {
				scopes = append([]string(nil), rt.AuthConfig.OAuth.Client.Scopes...)
			}
			tok, err := authorizeQueryOOB(ctx, strings.TrimSpace(*oobURL), cfgURL, scopes)
			if err != nil {
				return err
			}
			ctx = iauth.WithTokens(ctx, tok)
			if v := strings.TrimSpace(tok.AccessToken); v != "" {
				ctx = iauth.WithBearer(ctx, v)
			}
			if v := strings.TrimSpace(tok.IDToken); v != "" {
				ctx = iauth.WithIDToken(ctx, v)
			}
			ctx = iauth.WithProvider(ctx, "oauth")
			if uid := strings.TrimSpace(*userID); uid != "" {
				ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: uid})
				if rt.TokenProvider != nil {
					runtimeCfgURL := ""
					if rt.AuthConfig != nil && rt.AuthConfig.OAuth != nil && rt.AuthConfig.OAuth.Client != nil {
						runtimeCfgURL = strings.TrimSpace(rt.AuthConfig.OAuth.Client.ConfigURL)
					}
					if runtimeCfgURL != "" && runtimeCfgURL == cfgURL {
						_ = rt.TokenProvider.Store(ctx, token.Key{Subject: uid, Provider: "oauth"}, tok)
					}
				}
			}
		}
		input := &agentsvc.QueryInput{
			ConversationID: strings.TrimSpace(*conversationID),
			AgentID:        selectedAgent,
			UserId:         strings.TrimSpace(*userID),
			ModelOverride:  strings.TrimSpace(*modelID),
			Query:          prompt,
			DisplayQuery:   prompt,
		}
		out := &agentsvc.QueryOutput{}
		if err := rt.Agent.Query(ctx, input, out); err != nil {
			return err
		}
		if strings.TrimSpace(out.Content) != "" {
			fmt.Fprintln(stdout, out.Content)
		}
		return nil
	}
	client, err := buildQueryClient(context.Background(), root)
	if err != nil {
		return err
	}
	input := &agentsvc.QueryInput{
		ConversationID: strings.TrimSpace(*conversationID),
		AgentID:        selectedAgent,
		UserId:         strings.TrimSpace(*userID),
		ModelOverride:  strings.TrimSpace(*modelID),
		Query:          prompt,
		DisplayQuery:   prompt,
	}
	out, err := client.Query(context.Background(), input)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out.Content) != "" {
		fmt.Fprintln(stdout, out.Content)
	}
	return nil
}

func initSkillCLI(workspaceRoot, agentID string, requireAgent bool) (*skillsvc.Service, *agentmdl.Agent, error) {
	root, defaults, err := resolveWorkspace(workspaceRoot)
	if err != nil {
		return nil, nil, err
	}
	metaSvc := meta.New(afs.New(), root)
	loader := agentloader.New(agentloader.WithMetaService(metaSvc))
	finder := agentfinder.New(agentfinder.WithLoader(loader))
	selectedAgent := strings.TrimSpace(agentID)
	if selectedAgent == "" && requireAgent {
		selectedAgent = strings.TrimSpace(defaults.Agent)
	}
	if selectedAgent == "" && requireAgent {
		return nil, nil, fmt.Errorf("agent is required (set --agent or configure default.agent in workspace config)")
	}
	svc := skillsvc.New(defaults, nil, finder)
	if err := svc.Load(context.Background()); err != nil {
		return nil, nil, err
	}
	if !requireAgent && selectedAgent == "" {
		return svc, nil, nil
	}
	agent, err := finder.Find(context.Background(), selectedAgent)
	if err != nil {
		return nil, nil, err
	}
	return svc, agent, nil
}

func resolveWorkspace(workspaceRoot string) (string, *execconfig.Defaults, error) {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		root = filepath.Join(mustUserHome(), ".agently")
	}
	workspace.SetRoot(root)
	cfg, err := wsconfig.Load(root)
	if err != nil {
		return "", nil, err
	}
	defaults := cfg.DefaultsWithFallback(&execconfig.Defaults{
		Model:    "openai_gpt-5.2",
		Embedder: "openai_text",
		Agent:    "chatter",
	})
	return root, defaults, nil
}

func mustUserHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func listSkillFiles(root, subdir string) []string {
	root = strings.TrimSpace(root)
	subdir = strings.TrimSpace(subdir)
	if root == "" || subdir == "" {
		return nil
	}
	base := filepath.Join(root, subdir)
	info, err := os.Stat(base)
	if err != nil || !info.IsDir() {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out
}
