//go:build windows

package exec

import (
	"context"
	"fmt"
	"strings"

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

type processState struct{}

func detachedUnsupported() error {
	return fmt.Errorf("system/exec detached mode is not supported on windows")
}

func (s *Service) start(ctx context.Context, in, out interface{}) error {
	if _, ok := in.(*StartInput); !ok {
		return svc.NewInvalidInputError(in)
	}
	if _, ok := out.(*StartOutput); !ok {
		return svc.NewInvalidOutputError(out)
	}
	_ = ctx
	return detachedUnsupported()
}

func (s *Service) status(ctx context.Context, in, out interface{}) error {
	if _, ok := in.(*StatusInput); !ok {
		return svc.NewInvalidInputError(in)
	}
	if _, ok := out.(*StatusOutput); !ok {
		return svc.NewInvalidOutputError(out)
	}
	_ = ctx
	return detachedUnsupported()
}

func (s *Service) cancel(ctx context.Context, in, out interface{}) error {
	if _, ok := in.(*CancelInput); !ok {
		return svc.NewInvalidInputError(in)
	}
	if _, ok := out.(*CancelOutput); !ok {
		return svc.NewInvalidOutputError(out)
	}
	_ = ctx
	return detachedUnsupported()
}

func (s *Service) AsyncConfig(toolName string) *asynccfg.Config {
	_ = toolName
	return nil
}

func (s *Service) AsyncConfigs() []*asynccfg.Config {
	return nil
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
