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
		return fmt.Errorf("usage: agently skill <list|activate|diagnostics|show|validate> [options]")
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
		return fmt.Errorf("usage: agently skill <list|activate|diagnostics|show|validate> [options]")
	}
	switch strings.TrimSpace(args[0]) {
	case "list":
		return runSkillList(args[1:], stdout)
	case "activate":
		return runSkillActivate(args[1:], stdout)
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
