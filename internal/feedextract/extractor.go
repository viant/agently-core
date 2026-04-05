package feedextract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/viant/forge/backend/types"
)

// Spec defines a generalized feed extraction plan.
type Spec struct {
	ID          string                 `json:"id,omitempty" yaml:"id,omitempty"`
	DataSources map[string]*DataSource `json:"dataSources,omitempty" yaml:"dataSources,omitempty"`
	CountSource string                 `json:"countSource,omitempty" yaml:"countSource,omitempty"`
}

// DataSource extends Forge's datasource model with feed extraction fields.
type DataSource struct {
	types.DataSource `json:",inline" yaml:",inline"`
	Source           string            `json:"source,omitempty" yaml:"source,omitempty"`
	Name             string            `json:"name,omitempty" yaml:"name,omitempty"`
	Merge            string            `json:"merge,omitempty" yaml:"merge,omitempty"`
	Inputs           []string          `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	ExposeAs         string            `json:"exposeAs,omitempty" yaml:"exposeAs,omitempty"`
	Root             bool              `json:"root,omitempty" yaml:"root,omitempty"`
	Derive           map[string]string `json:"derive,omitempty" yaml:"derive,omitempty"`
}

// Input carries raw request/response payloads for generalized extraction.
type Input struct {
	Spec             *Spec
	RequestPayloads  []string
	ResponsePayloads []string
}

// Result contains resolved datasource values plus a normalized rootData projection
// used by the SDK feed payload shape.
type Result struct {
	DataSources map[string]interface{} `json:"dataSources,omitempty"`
	RootName    string                 `json:"rootName,omitempty"`
	RootData    map[string]interface{} `json:"rootData,omitempty"`
	ItemCount   int                    `json:"itemCount,omitempty"`
}

// Extract executes generalized datasource extraction against raw tool payloads.
func Extract(input *Input) (*Result, error) {
	if input == nil || input.Spec == nil {
		return nil, nil
	}
	requests := parsePayloadList(input.RequestPayloads)
	responses := parsePayloadList(input.ResponsePayloads)
	spec := input.Spec
	resolved := map[string]interface{}{}
	visiting := map[string]bool{}
	var resolve func(string) error

	resolve = func(name string) error {
		if name = strings.TrimSpace(name); name == "" {
			return nil
		}
		if _, ok := resolved[name]; ok {
			return nil
		}
		ds := spec.DataSources[name]
		if ds == nil {
			resolved[name] = []interface{}{}
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("feedextract: datasource cycle at %q", name)
		}
		visiting[name] = true
		defer delete(visiting, name)

		var value interface{}
		switch {
		case len(ds.Inputs) > 0:
			inputs := make([]interface{}, 0, len(ds.Inputs))
			for _, item := range ds.Inputs {
				item = strings.TrimSpace(item)
				if item == "" {
					continue
				}
				if _, ok := spec.DataSources[item]; ok {
					if err := resolve(item); err != nil {
						return err
					}
					inputs = append(inputs, cloneJSONLike(resolved[item]))
					continue
				}
				inputs = append(inputs, resolveSource(item, requests, responses, ""))
			}
			value = mergeInputs(inputs, ds.Merge)
		case strings.TrimSpace(ds.DataSourceRef) != "":
			parentName := strings.TrimSpace(ds.DataSourceRef)
			if err := resolve(parentName); err != nil {
				return err
			}
			parentValue := resolved[parentName]
			selector := "output"
			if ds.Selectors != nil && strings.TrimSpace(ds.Selectors.Data) != "" {
				selector = strings.TrimSpace(ds.Selectors.Data)
			}
			parentRoot := parentValue
			if arr, ok := parentValue.([]interface{}); ok {
				if len(arr) == 1 {
					parentRoot = arr[0]
				}
			}
			value = selectPath(selector, parentRoot)
		case strings.TrimSpace(ds.Source) != "":
			value = resolveSource(strings.TrimSpace(ds.Source), requests, responses, strings.TrimSpace(ds.Merge))
		default:
			value = []interface{}{}
		}

		if len(ds.UniqueKey) > 0 {
			value = dedupeByUniqueKey(value, ds.UniqueKey)
		}
		if len(ds.Derive) > 0 {
			value = applyDerive(value, ds.Derive)
		}
		resolved[name] = cloneJSONLike(value)
		return nil
	}

	for name := range spec.DataSources {
		if err := resolve(name); err != nil {
			return nil, err
		}
	}

	rootName := rootDataSourceName(spec, resolved)
	rootData := projectLegacyRoot(spec, resolved)
	itemCount := computeItemCount(spec, resolved, rootName)
	return &Result{
		DataSources: resolved,
		RootName:    rootName,
		RootData:    rootData,
		ItemCount:   itemCount,
	}, nil
}

func parsePayloadList(payloads []string) []interface{} {
	var result []interface{}
	for _, payload := range payloads {
		payload = strings.TrimSpace(payload)
		if payload == "" {
			continue
		}
		var parsed interface{}
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			continue
		}
		result = append(result, parsed)
	}
	return result
}

func resolveSource(source string, requests, responses []interface{}, merge string) interface{} {
	scope, path := splitSource(source)
	roots := responses
	if scope == "input" {
		roots = requests
	}
	values := make([]interface{}, 0, len(roots))
	for _, root := range roots {
		val := root
		if path != "" {
			val = selectPath(path, root)
		}
		if val != nil {
			values = append(values, cloneJSONLike(val))
		}
	}
	return mergeValues(values, source, merge)
}

func splitSource(source string) (string, string) {
	source = strings.TrimSpace(source)
	switch {
	case source == "input":
		return "input", ""
	case source == "output":
		return "output", ""
	case strings.HasPrefix(source, "input."):
		return "input", strings.TrimPrefix(source, "input.")
	case strings.HasPrefix(source, "output."):
		return "output", strings.TrimPrefix(source, "output.")
	default:
		return "output", source
	}
}

func mergeValues(values []interface{}, source string, merge string) interface{} {
	merge = strings.TrimSpace(strings.ToLower(merge))
	if merge == "" {
		merge = defaultMerge(source, values)
	}
	switch merge {
	case "append", "union":
		out := make([]interface{}, 0)
		for _, value := range values {
			switch actual := value.(type) {
			case []interface{}:
				for _, item := range actual {
					out = append(out, cloneJSONLike(item))
				}
			default:
				out = append(out, cloneJSONLike(actual))
			}
		}
		return out
	case "merge_object":
		var merged interface{}
		for _, value := range values {
			merged = mergeJSONPreserveFirst(merged, value)
		}
		if merged == nil {
			return map[string]interface{}{}
		}
		return merged
	case "replace_last":
		fallthrough
	default:
		for i := len(values) - 1; i >= 0; i-- {
			if values[i] != nil {
				return cloneJSONLike(values[i])
			}
		}
		return nil
	}
}

func defaultMerge(source string, values []interface{}) string {
	source = strings.TrimSpace(strings.ToLower(source))
	if source == "input" || source == "output" {
		return "replace_last"
	}
	for _, value := range values {
		if _, ok := value.([]interface{}); ok {
			return "append"
		}
	}
	return "replace_last"
}

func mergeInputs(values []interface{}, merge string) interface{} {
	if strings.TrimSpace(merge) == "" {
		merge = "union"
	}
	return mergeValues(values, "", merge)
}

func mergeJSONPreserveFirst(a, b interface{}) interface{} {
	if a == nil {
		return cloneJSONLike(b)
	}
	if b == nil {
		return cloneJSONLike(a)
	}
	am, aok := a.(map[string]interface{})
	bm, bok := b.(map[string]interface{})
	if aok && bok {
		out := cloneMap(am)
		for key, bval := range bm {
			if aval, ok := out[key]; ok {
				out[key] = mergeJSONPreserveFirst(aval, bval)
			} else {
				out[key] = cloneJSONLike(bval)
			}
		}
		return out
	}
	as, aok := a.([]interface{})
	bs, bok := b.([]interface{})
	if aok && bok {
		out := make([]interface{}, 0, len(as)+len(bs))
		for _, item := range as {
			out = append(out, cloneJSONLike(item))
		}
		for _, item := range bs {
			out = append(out, cloneJSONLike(item))
		}
		return out
	}
	if aok {
		out := make([]interface{}, 0, len(as)+1)
		for _, item := range as {
			out = append(out, cloneJSONLike(item))
		}
		out = append(out, cloneJSONLike(b))
		return out
	}
	if bok {
		out := make([]interface{}, 0, len(bs)+1)
		out = append(out, cloneJSONLike(a))
		for _, item := range bs {
			out = append(out, cloneJSONLike(item))
		}
		return out
	}
	return cloneJSONLike(a)
}

func dedupeByUniqueKey(value interface{}, keys []*types.UniqueKey) interface{} {
	items, ok := value.([]interface{})
	if !ok || len(keys) == 0 {
		return value
	}
	seen := map[string]bool{}
	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			out = append(out, cloneJSONLike(item))
			continue
		}
		parts := make([]string, 0, len(keys))
		hasResolved := false
		for _, key := range keys {
			if key == nil || strings.TrimSpace(key.Field) == "" {
				continue
			}
			raw := selectPath(key.Field, m)
			if raw == nil {
				parts = append(parts, "")
				continue
			}
			text := strings.TrimSpace(fmt.Sprintf("%v", raw))
			if text != "" && text != "<nil>" {
				hasResolved = true
			}
			parts = append(parts, text)
		}
		compound := strings.Join(parts, "|")
		if hasResolved && compound != "" && seen[compound] {
			continue
		}
		if hasResolved && compound != "" {
			seen[compound] = true
		}
		out = append(out, cloneJSONLike(item))
	}
	return out
}

func applyDerive(value interface{}, derive map[string]string) interface{} {
	switch actual := value.(type) {
	case map[string]interface{}:
		out := cloneMap(actual)
		for field, template := range derive {
			out[field] = renderTemplate(template, out)
		}
		return out
	case []interface{}:
		out := make([]interface{}, 0, len(actual))
		for _, item := range actual {
			if m, ok := item.(map[string]interface{}); ok {
				derived := cloneMap(m)
				for field, template := range derive {
					derived[field] = renderTemplate(template, derived)
				}
				out = append(out, derived)
				continue
			}
			out = append(out, cloneJSONLike(item))
		}
		return out
	default:
		return value
	}
}

var templateExpr = regexp.MustCompile(`\$\{([^}]+)\}`)

func renderTemplate(template string, data map[string]interface{}) string {
	return templateExpr.ReplaceAllStringFunc(template, func(match string) string {
		sub := templateExpr.FindStringSubmatch(match)
		if len(sub) != 2 {
			return ""
		}
		raw := selectPath(strings.TrimSpace(sub[1]), data)
		if raw == nil {
			return ""
		}
		return fmt.Sprintf("%v", raw)
	})
}

func rootDataSourceName(spec *Spec, resolved map[string]interface{}) string {
	if spec == nil {
		return ""
	}
	for name, ds := range spec.DataSources {
		if ds != nil && ds.Root {
			return name
		}
	}
	for name, value := range resolved {
		if arr, ok := value.([]interface{}); ok && len(arr) > 0 {
			return name
		}
	}
	return ""
}

func computeItemCount(spec *Spec, resolved map[string]interface{}, rootName string) int {
	if spec != nil && strings.TrimSpace(spec.CountSource) != "" {
		if value, ok := resolved[strings.TrimSpace(spec.CountSource)]; ok {
			return itemCount(value)
		}
	}
	if rootName != "" {
		if value, ok := resolved[rootName]; ok {
			return itemCount(value)
		}
	}
	return 0
}

func itemCount(value interface{}) int {
	switch actual := value.(type) {
	case []interface{}:
		return len(actual)
	case map[string]interface{}:
		if len(actual) == 0 {
			return 0
		}
		return 1
	default:
		if actual == nil {
			return 0
		}
		return 1
	}
}

func projectLegacyRoot(spec *Spec, resolved map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	exposed := map[string]string{}
	for name, ds := range spec.DataSources {
		if ds == nil {
			continue
		}
		exposeAs := strings.TrimSpace(ds.ExposeAs)
		if exposeAs != "" {
			out[exposeAs] = cloneJSONLike(resolved[name])
			exposed[name] = exposeAs
			continue
		}
		switch strings.TrimSpace(strings.ToLower(ds.Source)) {
		case "input":
			if _, ok := out["input"]; !ok {
				out["input"] = cloneJSONLike(resolved[name])
				exposed[name] = "input"
			}
		case "output":
			if _, ok := out["output"]; !ok {
				out["output"] = cloneJSONLike(resolved[name])
				exposed[name] = "output"
			}
		}
	}
	if _, ok := out["input"]; !ok {
		out["input"] = map[string]interface{}{}
	}
	if _, ok := out["output"]; !ok {
		out["output"] = map[string]interface{}{}
	}
	for name, ds := range spec.DataSources {
		if ds == nil {
			continue
		}
		parentName := strings.TrimSpace(ds.DataSourceRef)
		if parentName == "" {
			continue
		}
		parentExpose := exposed[parentName]
		if parentExpose == "" {
			continue
		}
		parent, ok := out[parentExpose].(map[string]interface{})
		if !ok {
			continue
		}
		field := selectorField(ds.Selectors)
		if field == "" {
			continue
		}
		parent[field] = cloneJSONLike(resolved[name])
	}
	return out
}

func selectorField(selectors *types.Selectors) string {
	if selectors == nil {
		return ""
	}
	data := strings.TrimSpace(selectors.Data)
	if data == "" || strings.Contains(data, ".") || strings.Contains(data, "[") {
		return ""
	}
	return data
}

func selectPath(selector string, root interface{}) interface{} {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return root
	}
	if selector == "output" {
		if m, ok := root.(map[string]interface{}); ok {
			if val, ok := m["output"]; ok {
				return val
			}
		}
		return root
	}
	if selector == "input" {
		if m, ok := root.(map[string]interface{}); ok {
			if val, ok := m["input"]; ok {
				return val
			}
		}
		return root
	}
	cur := root
	norm := strings.ReplaceAll(selector, "[", ".")
	norm = strings.ReplaceAll(norm, "]", "")
	norm = strings.TrimPrefix(norm, ".")
	if m, ok := cur.(map[string]interface{}); ok {
		if _, hasOut := m["output"]; !hasOut {
			if _, hasIn := m["input"]; !hasIn {
				if strings.HasPrefix(norm, "output.") || strings.HasPrefix(norm, "input.") {
					return selectPath(strings.TrimPrefix(strings.TrimPrefix(norm, "output."), "input."), cur)
				}
			}
		}
	}
	for _, token := range strings.Split(norm, ".") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if cur == nil {
			return nil
		}
		switch actual := cur.(type) {
		case map[string]interface{}:
			if value, ok := actual[token]; ok {
				cur = value
			} else {
				return nil
			}
		case []interface{}:
			idx := -1
			for _, r := range token {
				if r < '0' || r > '9' {
					return nil
				}
			}
			if token != "" {
				fmt.Sscanf(token, "%d", &idx)
			}
			if idx < 0 || idx >= len(actual) {
				return nil
			}
			cur = actual[idx]
		default:
			return nil
		}
	}
	return cur
}

func cloneMap(src map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(src))
	for key, value := range src {
		out[key] = cloneJSONLike(value)
	}
	return out
}

func cloneJSONLike(value interface{}) interface{} {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out interface{}
	if err = json.Unmarshal(data, &out); err != nil {
		return value
	}
	return out
}
