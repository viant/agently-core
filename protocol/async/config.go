package async

type Config struct {
	Run    RunConfig     `json:"run" yaml:"run"`
	Status StatusConfig  `json:"status" yaml:"status"`
	Cancel *CancelConfig `json:"cancel,omitempty" yaml:"cancel,omitempty"`
	// Instruction is per-operation guidance for non-terminal state, surfaced in
	// the centralized batch reinforcement template.
	Instruction string `json:"instruction,omitempty" yaml:"instruction,omitempty"`
	// TerminalInstruction is per-operation guidance once the operation reaches a
	// terminal state, surfaced in the centralized batch reinforcement template.
	TerminalInstruction           string `json:"terminalInstruction,omitempty" yaml:"terminalInstruction,omitempty"`
	WaitForResponse               bool   `json:"waitForResponse,omitempty" yaml:"waitForResponse,omitempty"`
	MaxReinforcementsPerOperation int    `json:"maxReinforcementsPerOperation,omitempty" yaml:"maxReinforcementsPerOperation,omitempty"`
	MinIntervalBetweenMs          int    `json:"minIntervalBetweenMs,omitempty" yaml:"minIntervalBetweenMs,omitempty"`
	TimeoutMs                     int    `json:"timeoutMs,omitempty" yaml:"timeoutMs,omitempty"`
	PollIntervalMs                int    `json:"pollIntervalMs,omitempty" yaml:"pollIntervalMs,omitempty"`
}

type RunConfig struct {
	Tool            string                 `json:"tool" yaml:"tool"`
	OperationIDPath string                 `json:"operationIdPath" yaml:"operationIdPath"`
	ExtraArgs       map[string]interface{} `json:"extraArgs,omitempty" yaml:"extraArgs,omitempty"`
	Selector        *Selector              `json:"selector,omitempty" yaml:"selector,omitempty"`
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
