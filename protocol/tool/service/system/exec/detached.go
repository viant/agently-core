package exec

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/viant/agently-core/internal/textutil"
	asynccfg "github.com/viant/agently-core/protocol/async"
	svc "github.com/viant/agently-core/protocol/tool/service"
	sys "github.com/viant/agently-core/protocol/tool/service/system"
	"github.com/viant/mcp-protocol/extension"
)

type StartInput struct {
	SessionID    string            `json:"sessionId,omitempty" description:"Optional async session id. When empty, the runtime generates one."`
	Host         *sys.Host         `json:"host,omitempty" internal:"true" description:"Target host. Detached mode currently supports localhost only."`
	Workdir      string            `json:"workdir,omitempty" description:"Working directory for file operations. Example: /repo/path."`
	Env          map[string]string `json:"env,omitempty" description:"Environment variables (k=v) set before running."`
	Commands     []string          `json:"commands,omitempty" description:"Commands to run in order (no pipes)."`
	TimeoutMs    int               `json:"timeoutMs,omitempty" yaml:"timeoutMs,omitempty" description:"Reserved for higher-level async management; ignored by detached execution."`
	AbortOnError *bool             `json:"abortOnError,omitempty" description:"Stop on first non-zero status (default true)."`
}

type StartOutput struct {
	SessionID string `json:"sessionId,omitempty"`
	ProcessID string `json:"processId,omitempty"`
	Status    string `json:"status,omitempty"`
}

type StatusInput struct {
	SessionID string             `json:"sessionId,omitempty"`
	ProcessID string             `json:"processId,omitempty"`
	Stream    string             `json:"stream,omitempty" description:"Optional stream selector for ranged reads: stdout, stderr, or combined."`
	ByteRange *textutil.IntRange `json:"byteRange,omitempty" description:"Optional byte range [from,to) over the selected stream content."`
}

type StatusOutput struct {
	SessionID    string                  `json:"sessionId,omitempty"`
	ProcessID    string                  `json:"processId,omitempty"`
	Stream       string                  `json:"stream,omitempty"`
	Status       string                  `json:"status,omitempty"`
	ExitCode     *int                    `json:"exitCode,omitempty"`
	Stdout       string                  `json:"stdout,omitempty" description:"Full buffered stdout accumulated so far. Not truncated."`
	Stderr       string                  `json:"stderr,omitempty" description:"Full buffered stderr accumulated so far. Not truncated."`
	Content      string                  `json:"content,omitempty" description:"Selected stream content slice when stream/byteRange paging is used."`
	Offset       int                     `json:"offset,omitempty"`
	Limit        int                     `json:"limit,omitempty"`
	Size         int                     `json:"size,omitempty"`
	Continuation *extension.Continuation `json:"continuation,omitempty" description:"Native byte-range continuation for the selected stream content."`
}

type CancelInput struct {
	SessionID string `json:"sessionId,omitempty"`
	ProcessID string `json:"processId,omitempty"`
}

type CancelOutput struct {
	Status string `json:"status,omitempty"`
}

type processState struct {
	mu              sync.RWMutex
	id              string
	sessionID       string
	cmd             *osexec.Cmd
	status          string
	exitCode        *int
	stdoutPath      string
	stderrPath      string
	cancelRequested bool
	waitErr         string
}

func (s *Service) start(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*StartInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*StartOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	normalized := input.asExecuteInput()
	normalized.Init()
	if err := validateDetachedInput(normalized); err != nil {
		return err
	}
	script := buildDetachedScript(normalized)
	stdoutFile, err := os.CreateTemp("", "agently-exec-stdout-*.log")
	if err != nil {
		return fmt.Errorf("create stdout file: %w", err)
	}
	stderrFile, err := os.CreateTemp("", "agently-exec-stderr-*.log")
	if err != nil {
		_ = stdoutFile.Close()
		_ = os.Remove(stdoutFile.Name())
		return fmt.Errorf("create stderr file: %w", err)
	}
	cmd := osexec.Command("/bin/sh", "-c", script)
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
		_ = os.Remove(stdoutFile.Name())
		_ = os.Remove(stderrFile.Name())
		return fmt.Errorf("start detached command: %w", err)
	}
	_ = stdoutFile.Close()
	_ = stderrFile.Close()

	processID := strconv.Itoa(cmd.Process.Pid)
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		sessionID = processID
	}
	state := &processState{
		id:         processID,
		sessionID:  sessionID,
		cmd:        cmd,
		status:     "running",
		stdoutPath: stdoutFile.Name(),
		stderrPath: stderrFile.Name(),
	}
	s.mu.Lock()
	if s.processes == nil {
		s.processes = map[string]*processState{}
	}
	s.processes[processID] = state
	s.processes[sessionID] = state
	s.mu.Unlock()

	go s.waitDetachedProcess(state)

	output.SessionID = sessionID
	output.ProcessID = processID
	output.Status = "running"
	return nil
}

func (s *Service) status(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*StatusInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*StatusOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	state, err := s.lookupProcess(firstNonEmpty(strings.TrimSpace(input.SessionID), strings.TrimSpace(input.ProcessID)))
	if err != nil {
		return err
	}
	status, exitCode := state.snapshot()
	stdout, stderr := state.readOutput()
	output.SessionID = state.sessionID
	output.ProcessID = state.id
	output.Status = status
	output.ExitCode = exitCode
	output.Stdout = stdout
	output.Stderr = stderr
	if strings.TrimSpace(input.Stream) != "" || input.ByteRange != nil {
		selected, streamName := selectedStatusStream(strings.TrimSpace(input.Stream), stdout, stderr)
		clipped, offset, end, err := textutil.ClipBytes([]byte(selected), input.ByteRange)
		if err != nil {
			return err
		}
		output.Stream = streamName
		output.Content = string(clipped)
		output.Offset = offset
		output.Limit = end - offset
		output.Size = len(selected)
		if end < len(selected) {
			remaining := len(selected) - end
			nextLength := output.Limit
			if nextLength <= 0 {
				nextLength = remaining
			}
			if nextLength > remaining {
				nextLength = remaining
			}
			output.Continuation = &extension.Continuation{
				HasMore:   true,
				Remaining: remaining,
				Returned:  output.Limit,
				NextRange: &extension.RangeHint{
					Bytes: &extension.ByteRange{
						Offset: end,
						Length: nextLength,
					},
				},
			}
		}
	}
	_ = ctx
	return nil
}

func (s *Service) cancel(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*CancelInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*CancelOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	state, err := s.lookupProcess(firstNonEmpty(strings.TrimSpace(input.SessionID), strings.TrimSpace(input.ProcessID)))
	if err != nil {
		return err
	}
	status, _ := state.snapshot()
	if status != "running" {
		output.Status = status
		return nil
	}
	state.markCancelRequested()
	if err := signalProcessGroup(state.cmd.Process.Pid, syscall.SIGTERM); err != nil && !isNoSuchProcess(err) {
		return fmt.Errorf("cancel process %s: %w", state.id, err)
	}
	go func() {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		<-timer.C
		status, _ := state.snapshot()
		if status != "running" {
			return
		}
		_ = signalProcessGroup(state.cmd.Process.Pid, syscall.SIGKILL)
	}()
	output.Status = "canceled"
	_ = ctx
	return nil
}

func (s *Service) waitDetachedProcess(state *processState) {
	err := state.cmd.Wait()
	exitCode := 0
	if state.cmd.ProcessState != nil {
		exitCode = state.cmd.ProcessState.ExitCode()
	}
	status := "completed"
	if state.wasCancelRequested() {
		status = "canceled"
	} else if err != nil || exitCode != 0 {
		status = "failed"
	}
	state.finish(status, exitCode, err)
}

func (s *Service) lookupProcess(processID string) (*processState, error) {
	if processID == "" {
		return nil, fmt.Errorf("processId is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := s.processes[processID]
	if state == nil {
		return nil, svc.NewMethodNotFoundError("process not found: " + processID)
	}
	return state, nil
}

func (i *StartInput) asExecuteInput() *Input {
	if i == nil {
		return &Input{}
	}
	return &Input{
		SessionID:    i.SessionID,
		Host:         i.Host,
		Workdir:      i.Workdir,
		Env:          i.Env,
		Commands:     append([]string(nil), i.Commands...),
		TimeoutMs:    i.TimeoutMs,
		AbortOnError: i.AbortOnError,
	}
}

func validateDetachedInput(input *Input) error {
	if input == nil {
		return fmt.Errorf("input is required")
	}
	if len(input.Commands) == 0 {
		return fmt.Errorf("at least one command is required")
	}
	if input.Host == nil {
		input.Init()
	}
	hostURL := strings.TrimSpace(input.Host.URL)
	if hostURL == "" {
		hostURL = "bash://localhost/"
	}
	if !strings.Contains(hostURL, "localhost") {
		return fmt.Errorf("detached execution currently supports localhost only")
	}
	if input.Workdir == "." {
		return fmt.Errorf("absolute path in workdir is required")
	}
	if input.Workdir == "" {
		if cmd := input.HasFSCommand(); cmd != "" {
			return fmt.Errorf("workdir is required for %s", cmd)
		}
	}
	return nil
}

func buildDetachedScript(input *Input) string {
	var builder strings.Builder
	builder.WriteString("#!/bin/sh\n")
	builder.WriteString("set +e\n")
	if strings.TrimSpace(input.Workdir) != "" {
		builder.WriteString("cd ")
		builder.WriteString(shellQuote(strings.TrimSpace(input.Workdir)))
		builder.WriteString(" || exit 1\n")
	}
	if len(input.Env) > 0 {
		keys := make([]string, 0, len(input.Env))
		for key := range input.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			builder.WriteString("export ")
			builder.WriteString(key)
			builder.WriteString("=")
			builder.WriteString(shellQuote(input.Env[key]))
			builder.WriteString("\n")
		}
	}
	abortOnError := true
	if input.AbortOnError != nil {
		abortOnError = *input.AbortOnError
	}
	builder.WriteString("last_status=0\n")
	for _, command := range input.Commands {
		builder.WriteString(command)
		builder.WriteString("\n")
		builder.WriteString("status=$?\n")
		builder.WriteString("if [ \"$status\" -ne 0 ]; then\n")
		builder.WriteString("  last_status=$status\n")
		if abortOnError {
			builder.WriteString("  exit $last_status\n")
		}
		builder.WriteString("fi\n")
	}
	builder.WriteString("exit $last_status\n")
	return builder.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func selectedStatusStream(stream, stdout, stderr string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(stream)) {
	case "stderr":
		return stderr, "stderr"
	case "combined":
		switch {
		case stdout == "":
			return stderr, "combined"
		case stderr == "":
			return stdout, "combined"
		default:
			return stdout + "\n" + stderr, "combined"
		}
	default:
		return stdout, "stdout"
	}
}

func (s *Service) AsyncConfig(toolName string) *asynccfg.Config {
	for _, cfg := range s.AsyncConfigs() {
		if cfg == nil {
			continue
		}
		if strings.TrimSpace(cfg.Run.Tool) == strings.TrimSpace(toolName) ||
			strings.TrimSpace(cfg.Status.Tool) == strings.TrimSpace(toolName) ||
			(cfg.Cancel != nil && strings.TrimSpace(cfg.Cancel.Tool) == strings.TrimSpace(toolName)) {
			return cfg
		}
	}
	return nil
}

func (s *Service) AsyncConfigs() []*asynccfg.Config {
	return []*asynccfg.Config{
		{
			WaitForResponse: true,
			TimeoutMs:       int((10 * time.Minute) / time.Millisecond),
			PollIntervalMs:  int((2 * time.Second) / time.Millisecond),
			Run: asynccfg.RunConfig{
				Tool:            "system/exec:start",
				OperationIDPath: "sessionId",
				Selector:        &asynccfg.Selector{StatusPath: "status"},
			},
			Status: asynccfg.StatusConfig{
				Tool:           "system/exec:status",
				OperationIDArg: "sessionId",
				Selector: asynccfg.Selector{
					StatusPath: "status",
					DataPath:   "stdout",
					ErrorPath:  "stderr",
				},
			},
			Cancel: &asynccfg.CancelConfig{
				Tool:           "system/exec:cancel",
				OperationIDArg: "sessionId",
			},
		},
	}
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func (p *processState) snapshot() (string, *int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var exitCode *int
	if p.exitCode != nil {
		copy := *p.exitCode
		exitCode = &copy
	}
	return p.status, exitCode
}

func (p *processState) markCancelRequested() {
	p.mu.Lock()
	p.cancelRequested = true
	p.mu.Unlock()
}

func (p *processState) wasCancelRequested() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cancelRequested
}

func (p *processState) finish(status string, exitCode int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = status
	p.exitCode = &exitCode
	if err != nil {
		p.waitErr = err.Error()
	}
}

func (p *processState) readOutput() (string, string) {
	p.mu.RLock()
	stdoutPath := p.stdoutPath
	stderrPath := p.stderrPath
	waitErr := p.waitErr
	p.mu.RUnlock()
	stdoutBytes, _ := os.ReadFile(stdoutPath)
	stderrBytes, _ := os.ReadFile(stderrPath)
	stderr := strings.TrimSpace(string(stderrBytes))
	if stderr == "" && waitErr != "" {
		stderr = waitErr
	}
	return strings.TrimSpace(string(stdoutBytes)), stderr
}

func signalProcessGroup(pid int, signal syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	if err := syscall.Kill(-pid, signal); err == nil {
		return nil
	} else if isNoSuchProcess(err) {
		return err
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(signal)
}

func isNoSuchProcess(err error) bool {
	return err != nil && (strings.Contains(strings.ToLower(err.Error()), "no such process") || err == syscall.ESRCH)
}
