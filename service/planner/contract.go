package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/afs/file"
	afsurl "github.com/viant/afs/url"
	agentmdl "github.com/viant/agently-core/protocol/agent"
)

type Contract interface {
	Schema(ctx context.Context) (map[string]interface{}, error)
	Parse(raw string) (Output, error)
	Validate(ctx context.Context, out Output, vctx ValidationContext) []ValidationError
	Apply(app *Application, out Output, pctx *PlannerContext)
	GuidanceDocs(turnID string, out Output, pctx *PlannerContext, meta GuidanceMeta, errs []ValidationError) []GuidanceDoc
}

type Resolver interface {
	Resolve(ctx context.Context, plannerAgent *agentmdl.Agent) (Contract, error)
}

type StaticContract struct {
	schema map[string]interface{}
	rules  []ValidationRule
}

func NewStaticContract(schema map[string]interface{}) *StaticContract {
	return &StaticContract{schema: cloneSchemaMap(schema), rules: DefaultValidationRules()}
}

func NewStaticContractWithRules(schema map[string]interface{}, rules []ValidationRule) *StaticContract {
	return &StaticContract{schema: cloneSchemaMap(schema), rules: append([]ValidationRule(nil), rules...)}
}

func (c *StaticContract) Schema(context.Context) (map[string]interface{}, error) {
	if len(c.schema) == 0 {
		return nil, fmt.Errorf("planner contract schema is empty")
	}
	return cloneSchemaMap(c.schema), nil
}

func (c *StaticContract) Parse(raw string) (Output, error) {
	return Parse(raw)
}

func (c *StaticContract) Validate(_ context.Context, out Output, vctx ValidationContext) []ValidationError {
	rules := c.rules
	if len(rules) == 0 {
		rules = DefaultValidationRules()
	}
	return ValidateWithRules(context.Background(), out, rules, vctx)
}

func (c *StaticContract) Apply(app *Application, out Output, pctx *PlannerContext) {
	ApplyOutput(app, out, pctx)
}

func (c *StaticContract) GuidanceDocs(turnID string, out Output, pctx *PlannerContext, meta GuidanceMeta, errs []ValidationError) []GuidanceDoc {
	return BuildGuidanceDocs(turnID, out, pctx, meta, errs)
}

type StaticResolver struct {
	contract Contract
}

func NewStaticResolver(contract Contract) *StaticResolver {
	return &StaticResolver{contract: contract}
}

func (r *StaticResolver) Resolve(_ context.Context, _ *agentmdl.Agent) (Contract, error) {
	if r == nil || r.contract == nil {
		return nil, fmt.Errorf("planner contract not configured")
	}
	return r.contract, nil
}

type FileResolver struct {
	fs             afs.Service
	candidateNames []string
}

func NewFileResolver(fs afs.Service) *FileResolver {
	if fs == nil {
		fs = afs.New()
	}
	return &FileResolver{
		fs:             fs,
		candidateNames: []string{"planner.schema.json", "schema.json"},
	}
}

func (r *FileResolver) Resolve(ctx context.Context, plannerAgent *agentmdl.Agent) (Contract, error) {
	if plannerAgent == nil || plannerAgent.Source == nil || strings.TrimSpace(plannerAgent.Source.URL) == "" {
		return nil, fmt.Errorf("planner contract source is not configured")
	}
	baseURL, _ := afsurl.Split(strings.TrimSpace(plannerAgent.Source.URL), file.Scheme)
	for _, name := range r.candidateNames {
		schemaURL := afsurl.JoinUNC(baseURL, name)
		ok, err := r.fs.Exists(ctx, schemaURL)
		if err != nil {
			return nil, fmt.Errorf("planner contract existence check failed for %s: %w", schemaURL, err)
		}
		if !ok {
			continue
		}
		validationURL := ""
		for _, candidate := range []string{"planner.validation.json", "validation.json"} {
			rulesURL := afsurl.JoinUNC(baseURL, candidate)
			rulesOK, rulesErr := r.fs.Exists(ctx, rulesURL)
			if rulesErr != nil {
				return nil, fmt.Errorf("planner contract validation existence check failed for %s: %w", rulesURL, rulesErr)
			}
			if rulesOK {
				validationURL = rulesURL
				break
			}
		}
		return &FileContract{fs: r.fs, schemaURL: schemaURL, validationURL: validationURL}, nil
	}
	return nil, fmt.Errorf("planner contract schema not found next to %s", strings.TrimSpace(plannerAgent.Source.URL))
}

type FileContract struct {
	fs            afs.Service
	schemaURL     string
	validationURL string
}

func (c *FileContract) Schema(ctx context.Context) (map[string]interface{}, error) {
	if c == nil || c.fs == nil || strings.TrimSpace(c.schemaURL) == "" {
		return nil, fmt.Errorf("planner contract schema location is not configured")
	}
	data, err := c.fs.DownloadWithURL(ctx, strings.TrimSpace(c.schemaURL))
	if err != nil {
		return nil, fmt.Errorf("failed to load planner contract schema: %w", err)
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("failed to decode planner contract schema: %w", err)
	}
	if len(schema) == 0 {
		return nil, fmt.Errorf("planner contract schema is empty")
	}
	return schema, nil
}

func (c *FileContract) Parse(raw string) (Output, error) {
	return Parse(raw)
}

func (c *FileContract) Validate(_ context.Context, out Output, vctx ValidationContext) []ValidationError {
	rules, err := c.rules(context.Background())
	if err != nil {
		return []ValidationError{{
			Code:    "planner_validation_rules_error",
			Field:   "validation",
			Message: err.Error(),
		}}
	}
	return ValidateWithRules(context.Background(), out, rules, vctx)
}

func (c *FileContract) Apply(app *Application, out Output, pctx *PlannerContext) {
	ApplyOutput(app, out, pctx)
}

func (c *FileContract) GuidanceDocs(turnID string, out Output, pctx *PlannerContext, meta GuidanceMeta, errs []ValidationError) []GuidanceDoc {
	return BuildGuidanceDocs(turnID, out, pctx, meta, errs)
}

func cloneSchemaMap(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		out := make(map[string]interface{}, len(src))
		for k, v := range src {
			out[k] = v
		}
		return out
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		fallback := make(map[string]interface{}, len(src))
		for k, v := range src {
			fallback[k] = v
		}
		return fallback
	}
	return out
}

func (c *FileContract) rules(ctx context.Context) ([]ValidationRule, error) {
	if c == nil {
		return nil, fmt.Errorf("planner contract is nil")
	}
	if strings.TrimSpace(c.validationURL) == "" {
		return nil, fmt.Errorf("planner validation rules location is not configured")
	}
	data, err := c.fs.DownloadWithURL(ctx, strings.TrimSpace(c.validationURL))
	if err != nil {
		return nil, fmt.Errorf("failed to load planner validation rules: %w", err)
	}
	var payload struct {
		Rules []ValidationRule `json:"rules,omitempty"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("failed to decode planner validation rules: %w", err)
	}
	if len(payload.Rules) == 0 {
		return nil, fmt.Errorf("planner validation rules are empty")
	}
	return payload.Rules, nil
}
