package workspaceeval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/viant/agently-core/sdk"
	sdkapi "github.com/viant/agently-core/sdk/api"
	"gopkg.in/yaml.v3"
)

type EvalDoc struct {
	ID              string `yaml:"id"`
	Title           string `yaml:"title"`
	UserPrompt      string `yaml:"user_prompt"`
	EntryAgent      string `yaml:"entry_agent"`
	ExpectedRouting struct {
		Agent   string   `yaml:"agent"`
		Profile string   `yaml:"profile"`
		FanOut  []string `yaml:"fan_out"`
	} `yaml:"expected_routing"`
	ExpectedPreDelegationTools []EvalToolExpectation `yaml:"expected_pre_delegation_tools"`
	ExpectedOutput             struct {
		Template string `yaml:"template"`
	} `yaml:"expected_output"`
}

type EvalToolExpectation struct {
	Name string `yaml:"name"`
}

type AgentDoc struct {
	ID           string           `yaml:"id"`
	StarterTasks []StarterTaskDoc `yaml:"starterTasks"`
	Profile      struct {
		Publish bool `yaml:"publish"`
	} `yaml:"profile"`
}

type StarterTaskDoc struct {
	ID              string   `yaml:"id"`
	Title           string   `yaml:"title"`
	CoverageEvalIDs []string `yaml:"coverageEvalIds"`
}

type PromptDoc struct {
	ID               string `yaml:"id"`
	EvidenceContract struct {
		Required   []string `yaml:"required"`
		Optional   []string `yaml:"optional"`
		Forbidden  []string `yaml:"forbidden"`
		Completion []string `yaml:"completion"`
	} `yaml:"evidenceContract"`
}

type Options struct {
	Workspace            string
	ContractTests        []string
	RequiredProfiles     []string
	Behavioral           bool
	BehavioralCases      string
	BehavioralTimeoutSec int
	BehavioralAPI        string
	BehavioralOOB        string
	BehavioralToken      string
	BehavioralAgentlyBin string
}

type delegatedExpectation struct {
	AgentID         string
	PromptProfileID string
}

func DefaultContractTests() []string {
	return []string{
		"templates/analytics_dashboard_contract.test.js",
		"templates/evaluator_contract.test.js",
		"templates/evidence_contract_profiles.test.js",
		"templates/exploratory_strategy_contract.test.js",
		"templates/operator_memory_contract.test.js",
		"templates/steward_prompt_ownership_contract.test.js",
		"templates/troubleshoot_guardrails_contract.test.js",
		"/Users/awitas/go/src/github.com/viant/agently-core/cmd/steward-evals/recommendation_stage_contract.test.js",
	}
}

func DefaultRequiredEvidenceContractProfiles() []string {
	return []string{
		"diagnostic_baseline",
		"performance_analysis",
		"inventory_diagnosis",
		"selector_signal_impact",
		"frequency_cap_recommendation",
		"site_list_recommendation",
		"configuration_review",
		"verification_overlap",
		"creative_recommendation",
		"supply_kpi",
		"workspace_ui",
	}
}

func Run(opts Options) error {
	workspace := strings.TrimSpace(opts.Workspace)
	if workspace == "" {
		return errors.New("workspace is required")
	}
	agentIndex, err := LoadAgents(filepath.Join(workspace, "agents"))
	if err != nil {
		return err
	}
	contractTests := opts.ContractTests
	if len(contractTests) == 0 {
		contractTests = DefaultContractTests()
	}
	requiredProfiles := opts.RequiredProfiles
	if len(requiredProfiles) == 0 {
		requiredProfiles = DefaultRequiredEvidenceContractProfiles()
	}
	var failures []string
	failures = append(failures, CheckEvalCatalog(filepath.Join(workspace, "evals"), filepath.Join(workspace, "prompts"), filepath.Join(workspace, "templates"), agentIndex)...)
	failures = append(failures, CheckPublicAgentsCovered(filepath.Join(workspace, "evals"), agentIndex)...)
	failures = append(failures, CheckStarterTaskCoverage(filepath.Join(workspace, "evals"), agentIndex)...)
	failures = append(failures, CheckEvidenceContractProfiles(filepath.Join(workspace, "prompts"), requiredProfiles)...)
	failures = append(failures, RunContractTests(workspace, contractTests)...)
	if opts.Behavioral {
		failures = append(failures, RunBehavioralEvals(
			workspace,
			filepath.Join(workspace, "evals"),
			opts.BehavioralCases,
			time.Duration(opts.BehavioralTimeoutSec)*time.Second,
			opts.BehavioralAPI,
			opts.BehavioralOOB,
			opts.BehavioralToken,
			opts.BehavioralAgentlyBin,
		)...)
	}
	if len(failures) == 0 {
		return nil
	}
	return errors.New("- " + strings.Join(failures, "\n- "))
}

func LoadAgents(agentRoot string) (map[string]AgentDoc, error) {
	index := map[string]AgentDoc{}
	err := filepath.WalkDir(agentRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d == nil || d.IsDir() || filepath.Ext(path) != ".yaml" {
			return nil
		}
		var doc AgentDoc
		if err := LoadYAML(path, &doc); err != nil {
			return err
		}
		if strings.TrimSpace(doc.ID) == "" {
			return nil
		}
		index[strings.TrimSpace(doc.ID)] = doc
		return nil
	})
	if err != nil {
		return nil, err
	}
	return index, nil
}

func CheckEvalCatalog(evalRoot, promptRoot, templateRoot string, agentIndex map[string]AgentDoc) []string {
	files, _ := filepath.Glob(filepath.Join(evalRoot, "*.yaml"))
	sort.Strings(files)
	if len(files) == 0 {
		return []string{"no eval yaml files found"}
	}
	var failures []string
	for _, file := range files {
		var doc EvalDoc
		if err := LoadYAML(file, &doc); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		name := filepath.Base(file)
		if strings.TrimSpace(doc.ID) == "" {
			failures = append(failures, name+": missing id")
		}
		if strings.TrimSpace(doc.Title) == "" {
			failures = append(failures, name+": missing title")
		}
		if strings.TrimSpace(doc.UserPrompt) == "" {
			failures = append(failures, name+": missing user_prompt")
		}
		if agentID := strings.TrimSpace(doc.ExpectedRouting.Agent); agentID != "" {
			if _, ok := agentIndex[agentID]; !ok {
				failures = append(failures, fmt.Sprintf("%s: expected_routing.agent=%s not found in agents/", name, agentID))
			}
		}
		if profileID := strings.TrimSpace(doc.ExpectedRouting.Profile); profileID != "" {
			if _, err := os.Stat(filepath.Join(promptRoot, profileID+".yaml")); err != nil {
				failures = append(failures, fmt.Sprintf("%s: expected_routing.profile=%s not found in prompts/", name, profileID))
			}
		}
		if templateID := strings.TrimSpace(doc.ExpectedOutput.Template); templateID != "" {
			if _, err := os.Stat(filepath.Join(templateRoot, templateID+".yaml")); err != nil {
				failures = append(failures, fmt.Sprintf("%s: expected_output.template=%s not found in templates/", name, templateID))
			}
		}
	}
	return failures
}

func CheckPublicAgentsCovered(evalRoot string, agentIndex map[string]AgentDoc) []string {
	files, _ := filepath.Glob(filepath.Join(evalRoot, "*.yaml"))
	var corpus strings.Builder
	for _, file := range files {
		data, _ := os.ReadFile(file)
		corpus.Write(data)
		corpus.WriteByte('\n')
	}
	content := corpus.String()
	var failures []string
	for id, doc := range agentIndex {
		if !doc.Profile.Publish {
			continue
		}
		if len(doc.StarterTasks) == 0 {
			failures = append(failures, fmt.Sprintf("published agent %s must declare starterTasks", id))
		}
		if !strings.Contains(content, id) {
			failures = append(failures, fmt.Sprintf("published agent %s must be referenced by at least one eval yaml", id))
		}
	}
	return failures
}

func CheckStarterTaskCoverage(evalRoot string, agentIndex map[string]AgentDoc) []string {
	evals, failures := LoadEvalDocs(evalRoot)
	if len(failures) > 0 {
		return failures
	}
	evalIDs := map[string]bool{}
	for id, doc := range evals {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if strings.TrimSpace(doc.ID) != "" {
			evalIDs[strings.TrimSpace(doc.ID)] = true
		}
		evalIDs[strings.TrimSpace(id)] = true
	}
	for agentID, doc := range agentIndex {
		if !doc.Profile.Publish {
			continue
		}
		for _, task := range doc.StarterTasks {
			taskID := strings.TrimSpace(task.ID)
			if taskID == "" {
				failures = append(failures, fmt.Sprintf("published agent %s has a starterTask missing id", agentID))
				continue
			}
			if len(task.CoverageEvalIDs) == 0 {
				failures = append(failures, fmt.Sprintf("starter task %s on published agent %s must declare coverageEvalIds", taskID, agentID))
				continue
			}
			for _, evalID := range task.CoverageEvalIDs {
				evalID = strings.TrimSpace(evalID)
				if evalID == "" {
					failures = append(failures, fmt.Sprintf("starter task %s on published agent %s has an empty coverageEvalIds entry", taskID, agentID))
					continue
				}
				if !evalIDs[evalID] {
					failures = append(failures, fmt.Sprintf("starter task %s on published agent %s references unknown eval %s", taskID, agentID, evalID))
				}
			}
		}
	}
	return failures
}

func CheckEvidenceContractProfiles(promptRoot string, requiredProfiles []string) []string {
	var failures []string
	for _, profileID := range requiredProfiles {
		full := filepath.Join(promptRoot, profileID+".yaml")
		var doc PromptDoc
		if err := LoadYAML(full, &doc); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if strings.TrimSpace(doc.ID) == "" {
			failures = append(failures, fmt.Sprintf("%s: missing id", filepath.Base(full)))
		}
		if len(doc.EvidenceContract.Required) == 0 {
			failures = append(failures, fmt.Sprintf("%s: evidenceContract.required must not be empty", filepath.Base(full)))
		}
		if len(doc.EvidenceContract.Completion) == 0 {
			failures = append(failures, fmt.Sprintf("%s: evidenceContract.completion must not be empty", filepath.Base(full)))
		}
	}
	return failures
}

func RunContractTests(workspace string, contractTests []string) []string {
	var failures []string
	for _, rel := range contractTests {
		target := rel
		if !filepath.IsAbs(target) {
			target = filepath.Join(workspace, rel)
		}
		if _, err := os.Stat(target); err != nil {
			failures = append(failures, "missing contract test: "+rel)
			continue
		}
		cmd := exec.Command("node", target)
		out, err := cmd.CombinedOutput()
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s failed: %s", rel, strings.TrimSpace(string(out))))
		}
	}
	return failures
}

func RunBehavioralEvals(workspace, evalRoot, selected string, timeout time.Duration, api, oob, token, agentlyBin string) []string {
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	api = strings.TrimSpace(api)
	if api == "" {
		return []string{"behavioral mode requires --behavioral-api so live evals run through the real agently server path"}
	}
	agentlyBin = strings.TrimSpace(agentlyBin)
	if agentlyBin == "" {
		return []string{"behavioral mode requires --behavioral-agently-bin"}
	}
	evals, failures := LoadEvalDocs(evalRoot)
	if len(failures) > 0 {
		return failures
	}
	targets := SelectBehavioralEvals(evals, selected)
	if len(targets) == 0 {
		return []string{"behavioral mode enabled but no evals were selected"}
	}
	client, err := NewBehavioralHTTPClient(context.Background(), api, token, oob)
	if err != nil {
		return []string{fmt.Sprintf("build behavioral http client: %v", err)}
	}
	var behavioralFailures []string
	for _, target := range targets {
		doc := evals[target]
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := RunBehavioralEval(ctx, client, agentlyBin, api, token, oob, doc)
		cancel()
		if err != nil {
			behavioralFailures = append(behavioralFailures, fmt.Sprintf("%s: %v", target, err))
		}
	}
	return behavioralFailures
}

func LoadEvalDocs(evalRoot string) (map[string]EvalDoc, []string) {
	files, _ := filepath.Glob(filepath.Join(evalRoot, "*.yaml"))
	sort.Strings(files)
	index := map[string]EvalDoc{}
	var failures []string
	for _, file := range files {
		var doc EvalDoc
		if err := LoadYAML(file, &doc); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		key := strings.TrimSpace(doc.ID)
		if key == "" {
			key = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		}
		if strings.TrimSpace(key) == "" {
			failures = append(failures, file+": missing id")
			continue
		}
		index[key] = doc
		index[strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))] = doc
	}
	return index, failures
}

func SelectBehavioralEvals(evals map[string]EvalDoc, selected string) []string {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		return nil
	}
	seen := map[string]bool{}
	var result []string
	for _, item := range strings.Split(selected, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		doc, ok := evals[item]
		if !ok {
			continue
		}
		if seen[doc.ID] {
			continue
		}
		seen[doc.ID] = true
		result = append(result, doc.ID)
	}
	sort.Strings(result)
	return result
}

func RunBehavioralEval(ctx context.Context, client sdk.Client, agentlyBin, api, token, oob string, doc EvalDoc) error {
	entryAgent := strings.TrimSpace(doc.EntryAgent)
	if entryAgent == "" {
		entryAgent = "steward"
	}
	conversationID, output, err := ExecuteBehavioralQuery(ctx, agentlyBin, api, token, oob, entryAgent, doc, TimeoutSeconds(ctx))
	if err != nil {
		return err
	}
	if strings.TrimSpace(conversationID) == "" {
		return fmt.Errorf("query returned no conversation id, output=%q", Truncate(output, 1200))
	}
	transcript, err := client.GetTranscript(ctx, &sdk.GetTranscriptInput{
		ConversationID:    strings.TrimSpace(conversationID),
		IncludeModelCalls: true,
		IncludeToolCalls:  true,
	})
	if err != nil {
		return fmt.Errorf("get transcript: %w", err)
	}
	if transcript == nil || transcript.Conversation == nil || len(transcript.Conversation.Turns) == 0 {
		return errors.New("canonical transcript missing turns")
	}
	if err := AssertBehavioralTranscript(doc, transcript); err != nil {
		return err
	}
	return nil
}

func AssertBehavioralTranscript(doc EvalDoc, transcript *sdk.ConversationStateResponse) error {
	turn := transcript.Conversation.Turns[len(transcript.Conversation.Turns)-1]
	if turn == nil {
		return errors.New("last turn missing")
	}
	if strings.TrimSpace(string(turn.Status)) != string(sdkapi.TurnStatusCompleted) {
		return fmt.Errorf("last turn status=%s, provider_request_preview=%q", strings.TrimSpace(string(turn.Status)), FirstProviderRequestPreview(turn))
	}
	toolSteps := CollectToolSteps(turn)
	if err := assertExpectedDelegations(doc, toolSteps); err != nil {
		return fmt.Errorf("%v, provider_request_preview=%q", err, FirstProviderRequestPreview(turn))
	}
	if err := assertPreDelegationTools(doc, toolSteps); err != nil {
		return fmt.Errorf("%v, provider_request_preview=%q", err, FirstProviderRequestPreview(turn))
	}
	if err := assertExpectedTemplate(doc, toolSteps); err != nil {
		return fmt.Errorf("%v, provider_request_preview=%q", err, FirstProviderRequestPreview(turn))
	}
	return nil
}

func NewBehavioralHTTPClient(ctx context.Context, api, token, oob string) (sdk.Client, error) {
	jar, _ := cookiejar.New(nil)
	httpClient := &http.Client{Jar: jar}
	client, err := sdk.NewHTTP(strings.TrimSpace(api), sdk.WithHTTPClient(httpClient))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(oob) != "" {
		if err := client.AuthLocalOOBSession(ctx, &sdk.LocalOOBSessionOptions{SecretsURL: strings.TrimSpace(oob)}); err != nil {
			return nil, err
		}
		return client, nil
	}
	token = strings.TrimSpace(token)
	if token != "" {
		if err := client.AuthSessionExchange(ctx, token); err == nil {
			return client, nil
		}
		if err := client.AuthCreateSession(ctx, &sdk.CreateSessionRequest{AccessToken: token}); err == nil {
			return client, nil
		}
		if err := client.AuthCreateSession(ctx, &sdk.CreateSessionRequest{IDToken: token}); err == nil {
			return client, nil
		}
		return nil, fmt.Errorf("behavioral token auth failed")
	}
	return client, nil
}

func ExecuteBehavioralQuery(ctx context.Context, agentlyBin, api, token, oob, agentID string, doc EvalDoc, timeoutSec int) (string, string, error) {
	args := []string{
		"query",
		"--api", strings.TrimSpace(api),
		"--agent-id", strings.TrimSpace(agentID),
		"--query", strings.TrimSpace(doc.UserPrompt),
		"--user", "steward-evals",
		"--reset-logs",
	}
	if timeoutSec > 0 {
		args = append(args, "--timeout", strconv.Itoa(timeoutSec))
	}
	if strings.TrimSpace(token) != "" {
		args = append(args, "--token", strings.TrimSpace(token))
	}
	if strings.TrimSpace(oob) != "" {
		args = append(args, "--oob", strings.TrimSpace(oob))
	}
	cmd := exec.CommandContext(ctx, agentlyBin, args...)
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		return "", text, fmt.Errorf("agently query failed: %v, output=%q", err, Truncate(text, 2000))
	}
	conversationID := ParseConversationID(text)
	return conversationID, text, nil
}

func ParseConversationID(output string) string {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "[conversation-id]") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "[conversation-id]"))
	}
	return ""
}

func TimeoutSeconds(ctx context.Context) int {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 1
		}
		return int(remaining.Seconds())
	}
	return 0
}

func CollectToolSteps(turn *sdkapi.TurnState) []*sdkapi.ToolStepState {
	if turn == nil || turn.Execution == nil {
		return nil
	}
	var result []*sdkapi.ToolStepState
	for _, page := range turn.Execution.Pages {
		if page == nil {
			continue
		}
		for _, step := range page.ToolSteps {
			if step == nil {
				continue
			}
			result = append(result, step)
		}
	}
	return result
}

func FirstProviderRequestPreview(turn *sdkapi.TurnState) string {
	if turn == nil || turn.Execution == nil {
		return ""
	}
	for _, page := range turn.Execution.Pages {
		if page == nil {
			continue
		}
		for _, step := range page.ModelSteps {
			if step == nil {
				continue
			}
			if text := DecodePayloadText(step.ProviderRequestPayload); text != "" {
				return Truncate(text, 400)
			}
			if text := DecodePayloadText(step.RequestPayload); text != "" {
				return Truncate(text, 400)
			}
		}
	}
	return ""
}

func LoadYAML(path string, dest any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func DecodePayloadObject(raw json.RawMessage) map[string]interface{} {
	if len(raw) == 0 {
		return nil
	}
	var object map[string]interface{}
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	for _, key := range []string{"inlineBody", "InlineBody"} {
		inline, _ := object[key].(string)
		inline = strings.TrimSpace(inline)
		if inline == "" {
			continue
		}
		var nested map[string]interface{}
		if err := json.Unmarshal([]byte(inline), &nested); err == nil {
			return nested
		}
		return map[string]interface{}{"_text": inline}
	}
	return object
}

func DecodePayloadText(raw json.RawMessage) string {
	object := DecodePayloadObject(raw)
	if len(object) == 0 {
		return ""
	}
	if text := strings.TrimSpace(StringValue(object["_text"])); text != "" {
		return text
	}
	data, err := json.Marshal(object)
	if err != nil {
		return ""
	}
	return string(data)
}

func StringValue(value interface{}) string {
	switch actual := value.(type) {
	case string:
		return actual
	case nil:
		return ""
	default:
		data, err := json.Marshal(actual)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func Truncate(value string, size int) string {
	value = strings.TrimSpace(value)
	if size <= 0 || len(value) <= size {
		return value
	}
	return value[:size]
}

func expectedDelegations(doc EvalDoc) []delegatedExpectation {
	var result []delegatedExpectation
	if agentID := strings.TrimSpace(doc.ExpectedRouting.Agent); agentID != "" && agentID != "steward" {
		result = append(result, delegatedExpectation{
			AgentID:         agentID,
			PromptProfileID: strings.TrimSpace(doc.ExpectedRouting.Profile),
		})
	}
	for _, item := range doc.ExpectedRouting.FanOut {
		parts := strings.Split(item, "+")
		agentID := ""
		profileID := ""
		if len(parts) > 0 {
			agentID = strings.TrimSpace(parts[0])
		}
		if len(parts) > 1 {
			profileID = strings.TrimSpace(parts[1])
		}
		if agentID == "" {
			continue
		}
		result = append(result, delegatedExpectation{
			AgentID:         agentID,
			PromptProfileID: profileID,
		})
	}
	return result
}

func collectDelegations(toolSteps []*sdkapi.ToolStepState) []delegatedExpectation {
	var result []delegatedExpectation
	for _, step := range toolSteps {
		if step == nil || strings.TrimSpace(step.ToolName) != "llm/agents:start" {
			continue
		}
		payload := DecodePayloadObject(step.RequestPayload)
		result = append(result, delegatedExpectation{
			AgentID:         strings.TrimSpace(StringValue(payload["agentId"])),
			PromptProfileID: strings.TrimSpace(StringValue(payload["promptProfileId"])),
		})
	}
	return result
}

func joinDelegations(items []delegatedExpectation) string {
	if len(items) == 0 {
		return "<none>"
	}
	var parts []string
	for _, item := range items {
		parts = append(parts, strings.TrimSpace(item.AgentID)+"+"+strings.TrimSpace(item.PromptProfileID))
	}
	return strings.Join(parts, ", ")
}

func hasToolName(toolSteps []*sdkapi.ToolStepState, name string) bool {
	name = strings.TrimSpace(name)
	for _, step := range toolSteps {
		if step != nil && strings.TrimSpace(step.ToolName) == name {
			return true
		}
	}
	return false
}

func assertExpectedDelegations(doc EvalDoc, toolSteps []*sdkapi.ToolStepState) error {
	expected := expectedDelegations(doc)
	if len(expected) == 0 {
		return nil
	}
	actual := collectDelegations(toolSteps)
	for _, want := range expected {
		var matched bool
		for _, got := range actual {
			if strings.TrimSpace(got.AgentID) == strings.TrimSpace(want.AgentID) &&
				strings.TrimSpace(got.PromptProfileID) == strings.TrimSpace(want.PromptProfileID) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("missing delegated child %s + %s (actual=%s)", want.AgentID, want.PromptProfileID, joinDelegations(actual))
		}
	}
	return nil
}

func assertPreDelegationTools(doc EvalDoc, toolSteps []*sdkapi.ToolStepState) error {
	if len(doc.ExpectedPreDelegationTools) == 0 {
		return nil
	}
	boundary := len(toolSteps)
	for i, step := range toolSteps {
		if strings.TrimSpace(step.ToolName) == "llm/agents:start" {
			boundary = i
			break
		}
	}
	prefix := toolSteps[:boundary]
	for _, want := range doc.ExpectedPreDelegationTools {
		if strings.TrimSpace(want.Name) == "" {
			continue
		}
		if !hasToolName(prefix, want.Name) {
			return fmt.Errorf("missing pre-delegation tool %s", want.Name)
		}
	}
	return nil
}

func assertExpectedTemplate(doc EvalDoc, toolSteps []*sdkapi.ToolStepState) error {
	templateID := strings.TrimSpace(doc.ExpectedOutput.Template)
	if templateID == "" {
		return nil
	}
	for _, step := range toolSteps {
		if strings.TrimSpace(step.ToolName) != "template:get" {
			continue
		}
		payload := DecodePayloadObject(step.RequestPayload)
		if strings.TrimSpace(StringValue(payload["name"])) == templateID {
			return nil
		}
	}
	return fmt.Errorf("missing template:get for template %s", templateID)
}
