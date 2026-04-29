package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	skillproto "github.com/viant/agently-core/protocol/skill"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

var ExecFn func(ctx context.Context, name string, args map[string]interface{}) (string, error)

var (
	reInline = regexp.MustCompile("`!`([^`\n]+)`")
	reFenced = regexp.MustCompile("(?s)```!(?:[a-zA-Z0-9_-]*)?\\n(.*?)\\n```")
)

type placeholder struct {
	start int
	end   int
	cmd   string
}

type preprocessStats struct {
	CommandsRun   int `json:"commandsRun,omitempty"`
	Denied        int `json:"denied,omitempty"`
	TimedOut      int `json:"timedOut,omitempty"`
	BytesExpanded int `json:"bytesExpanded,omitempty"`
}

func expandVars(cmd string, args string, skillDir string, conversationID string) string {
	fields := strings.Fields(strings.TrimSpace(args))
	replacerArgs := []string{
		"$ARGUMENTS", args,
		"${SKILL_DIR}", skillDir,
		"${AGENTLY_SESSION_ID}", conversationID,
	}
	for i := 1; i <= 9; i++ {
		val := ""
		if i-1 < len(fields) {
			val = fields[i-1]
		}
		replacerArgs = append(replacerArgs, fmt.Sprintf("$%d", i), val)
	}
	return strings.NewReplacer(replacerArgs...).Replace(cmd)
}

func extractPlaceholders(raw []byte) []placeholder {
	var out []placeholder
	for _, m := range reInline.FindAllSubmatchIndex(raw, -1) {
		out = append(out, placeholder{start: m[0], end: m[1], cmd: string(raw[m[2]:m[3]])})
	}
	for _, m := range reFenced.FindAllSubmatchIndex(raw, -1) {
		out = append(out, placeholder{start: m[0], end: m[1], cmd: string(raw[m[2]:m[3]])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

func parseExecOutput(serialized string) (stdout string, status int) {
	var out struct {
		Stdout   string `json:"stdout"`
		Status   int    `json:"status"`
		Commands []struct {
			Output string `json:"output"`
			Status int    `json:"status"`
		} `json:"commands"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(serialized)), &out)
	if strings.TrimSpace(out.Stdout) != "" {
		return out.Stdout, out.Status
	}
	if len(out.Commands) > 0 {
		return out.Commands[0].Output, out.Commands[0].Status
	}
	return "", out.Status
}

func preprocessBody(ctx context.Context, raw string, sk *skillproto.Skill, args string, conversationID string) (string, []string, preprocessStats) {
	if ExecFn == nil || sk == nil {
		return raw, []string{"preprocess: disabled (no executor injected at runtime bootstrap)"}, preprocessStats{}
	}
	places := extractPlaceholders([]byte(raw))
	if len(places) == 0 {
		return raw, nil, preprocessStats{}
	}
	const totalBudget = 30 * time.Second
	const byteCap = 16 * 1024
	deadline := time.Now().Add(totalBudget)
	constraints := BuildConstraints([]*skillproto.Skill{sk})

	var result strings.Builder
	var diags []string
	stats := preprocessStats{}
	last := 0
	for _, p := range places {
		result.WriteString(raw[last:p.start])
		last = p.end
		if time.Now().After(deadline) {
			stats.TimedOut++
			result.WriteString("<!-- preprocess: budget exceeded -->")
			continue
		}
		cmd := expandVars(strings.TrimSpace(p.cmd), args, sk.Root, conversationID)
		execArgs := map[string]interface{}{"commands": []string{cmd}}
		execCtx := WithConstraints(ctx, constraints)
		if err := ValidateExecution(execCtx, "system/exec:execute", execArgs); err != nil {
			stats.Denied++
			diags = append(diags, "preprocess-denied: "+cmd)
			result.WriteString("<!-- preprocess: denied by allowed-tools -->")
			continue
		}
		timeoutSec := sk.Frontmatter.PreprocessTimeoutValue()
		if timeoutSec <= 0 {
			timeoutSec = 10
		}
		abortTrue := true
		perCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		serialized, err := ExecFn(perCtx, "system/exec:execute", map[string]interface{}{
			"commands": []string{cmd},
			"workdir":  sk.Root,
			"env": map[string]string{
				"ARGUMENTS":          args,
				"SKILL_DIR":          sk.Root,
				"AGENTLY_SESSION_ID": conversationID,
			},
			"timeoutMs":    timeoutSec * 1000,
			"abortOnError": &abortTrue,
		})
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				stats.TimedOut++
			}
			diags = append(diags, "preprocess-error: "+err.Error())
			result.WriteString(fmt.Sprintf("<!-- preprocess: error: %v -->", err))
			continue
		}
		stats.CommandsRun++
		stdout, status := parseExecOutput(serialized)
		stats.BytesExpanded += len(stdout)
		if len(stdout) > byteCap {
			stdout = stdout[:byteCap] + "\n<!-- preprocess: output truncated -->"
		}
		result.WriteString(stdout)
		if status != 0 {
			result.WriteString(fmt.Sprintf("\n# (preprocess: exit code %d)", status))
		}
	}
	result.WriteString(raw[last:])
	return result.String(), diags, stats
}

func preprocessConversationID(ctx context.Context) string {
	if v := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx)); v != "" {
		return v
	}
	return ""
}
