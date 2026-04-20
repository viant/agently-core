package async

import "strings"

type ExecutionMode string

const (
	ExecutionModeWait   ExecutionMode = "wait"
	ExecutionModeDetach ExecutionMode = "detach"
	ExecutionModeFork   ExecutionMode = "fork"
)

type Config struct {
	Run                  RunConfig     `json:"run" yaml:"run"`
	Status               StatusConfig  `json:"status" yaml:"status"`
	Cancel               *CancelConfig `json:"cancel,omitempty" yaml:"cancel,omitempty"`
	DefaultExecutionMode string        `json:"defaultExecutionMode,omitempty" yaml:"defaultExecutionMode,omitempty"`
	TimeoutMs            int           `json:"timeoutMs,omitempty" yaml:"timeoutMs,omitempty"`
	PollIntervalMs       int           `json:"pollIntervalMs,omitempty" yaml:"pollIntervalMs,omitempty"`
	IdleTimeoutMs        int           `json:"idleTimeoutMs,omitempty" yaml:"idleTimeoutMs,omitempty"`
	Narration            string        `json:"narration,omitempty" yaml:"narration,omitempty"`
	NarrationTemplate    string        `json:"narrationTemplate,omitempty" yaml:"narrationTemplate,omitempty"`
}

type RunConfig struct {
	Tool              string                 `json:"tool" yaml:"tool"`
	OperationIDPath   string                 `json:"operationIdPath" yaml:"operationIdPath"`
	ExecutionModePath string                 `json:"executionModePath,omitempty" yaml:"executionModePath,omitempty"`
	IntentPath        string                 `json:"intentPath,omitempty" yaml:"intentPath,omitempty"`
	SummaryPaths      []string               `json:"summaryPaths,omitempty" yaml:"summaryPaths,omitempty"`
	ExtraArgs         map[string]interface{} `json:"extraArgs,omitempty" yaml:"extraArgs,omitempty"`
	Selector          *Selector              `json:"selector,omitempty" yaml:"selector,omitempty"`
}

type StatusConfig struct {
	Tool           string                 `json:"tool" yaml:"tool"`
	OperationIDArg string                 `json:"operationIdArg" yaml:"operationIdArg"`
	ReuseRunArgs   bool                   `json:"reuseRunArgs,omitempty" yaml:"reuseRunArgs,omitempty"`
	ExtraArgs      map[string]interface{} `json:"extraArgs,omitempty" yaml:"extraArgs,omitempty"`
	Selector       Selector               `json:"selector" yaml:"selector"`
}

type CancelConfig struct {
	Tool           string `json:"tool" yaml:"tool"`
	OperationIDArg string `json:"operationIdArg" yaml:"operationIdArg"`
}

type Selector struct {
	StatusPath       string   `json:"statusPath" yaml:"statusPath"`
	MessagePath      string   `json:"messagePath,omitempty" yaml:"messagePath,omitempty"`
	DataPath         string   `json:"dataPath,omitempty" yaml:"dataPath,omitempty"`
	ProgressPath     string   `json:"progressPath,omitempty" yaml:"progressPath,omitempty"`
	ErrorPath        string   `json:"errorPath,omitempty" yaml:"errorPath,omitempty"`
	TerminalStatuses []string `json:"terminalStatuses,omitempty" yaml:"terminalStatuses,omitempty"`
}

func NormalizeExecutionMode(value string, fallback string) string {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(fallback))
	}
	switch ExecutionMode(mode) {
	case ExecutionModeWait, ExecutionModeDetach, ExecutionModeFork:
		return mode
	default:
		if strings.TrimSpace(fallback) != "" {
			return NormalizeExecutionMode(fallback, string(ExecutionModeWait))
		}
		return string(ExecutionModeWait)
	}
}

func ExecutionModeWaits(value string) bool {
	switch ExecutionMode(NormalizeExecutionMode(value, string(ExecutionModeWait))) {
	case ExecutionModeWait, ExecutionModeFork:
		return true
	default:
		return false
	}
}
