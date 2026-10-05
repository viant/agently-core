package forecastbinding

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/viant/agently-core/runtime/evidence"
	forgefenced "github.com/viant/forge/backend/reporting/fenced"
)

// Guard enforces the initial closed forecast boundary: once a trusted plan is
// admitted, all new structured datasets in this turn need bindings. A model's
// label cannot exempt an unrelated table in that same turn.
type Guard struct {
	mu           sync.Mutex
	runtime      *Runtime
	scope        Scope
	check        func(context.Context) error
	active       func() bool
	streams      map[string]*evidence.FenceStream
	accepted     map[string]bool
	allowedPlans map[string]bool
	frames       []forgefenced.Fence
}

func newGuard(runtime *Runtime, scope Scope, check func(context.Context) error, active func() bool) *Guard {
	return &Guard{runtime: runtime, scope: scope, check: check, active: active, streams: map[string]*evidence.FenceStream{}, accepted: map[string]bool{}}
}
func (g *Guard) Content(ctx context.Context, content string) (string, error) {
	if err := g.check(ctx); err != nil {
		return "", err
	}
	if !g.active() {
		return content, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	stream := evidence.NewFenceStream(func(kind, body string) (string, error) { return g.fenceLocked(ctx, kind, body) })
	return stream.Push(content, true)
}
func (g *Guard) Fence(ctx context.Context, kind, body string) (string, error) {
	if err := g.check(ctx); err != nil {
		return "", err
	}
	if !g.active() {
		return body, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fenceLocked(ctx, kind, body)
}
func (g *Guard) Stream(ctx context.Context, message, delta string, final bool) (string, error) {
	if err := g.check(ctx); err != nil {
		return "", err
	}
	if !g.active() {
		return delta, nil
	}
	if message == "" {
		return "", reject("publication message identity missing")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	stream := g.streams[message]
	if stream == nil {
		stream = evidence.NewFenceStream(func(kind, body string) (string, error) { return g.fenceLocked(ctx, kind, body) })
		g.streams[message] = stream
	}
	return stream.Push(delta, final)
}

func (g *Guard) fenceLocked(ctx context.Context, kind, body string) (string, error) {
	if kind == "forge-config" {
		return "", reject("forecast proof supports canonical forge-report layout; forge-config inline/runtime data is unsupported")
	}
	if kind != "forge-data" && kind != "forge-report" {
		return body, nil
	}
	value, err := object(json.RawMessage(body))
	if err != nil {
		return "", err
	}
	scope, _ := value["scope"].(string)
	if scope == "" {
		scope = "message"
	}
	id, _ := value["id"].(string)
	if kind == "forge-data" {
		mode, _ := value["mode"].(string)
		if mode != "" && mode != "replace" {
			return "", reject("forecast bound datasets require replace mode")
		}
		hash, err := RequestHash(json.RawMessage(body))
		if err != nil {
			return "", err
		}
		if !g.accepted[hash] {
			if g.allowedPlans != nil {
				binding, _ := value["sourceBindings"].(map[string]any)
				id, _ := binding["planId"].(string)
				if !g.allowedPlans[id] {
					return "", reject("dataset plan differs from admitted report command")
				}
			}
			bound, err := g.runtime.BindDataEnvelope(ctx, g.scope, json.RawMessage(body))
			if err != nil {
				return "", err
			}
			body = string(bound)
			hash, err = RequestHash(bound)
			if err != nil {
				return "", err
			}
			g.accepted[hash] = true
		}
	} else {
		if err := rejectInlineData(value); err != nil {
			return "", err
		}
	}
	candidate := append(append([]forgefenced.Fence(nil), g.frames...), forgefenced.Fence{Kind: kind, Payload: json.RawMessage(body)})
	mode, _ := value["mode"].(string)
	if kind == "forge-report" && mode == "commit" {
		assembly, err := forgefenced.Assemble(candidate, id)
		if err != nil {
			return "", err
		}
		if assembly == nil || assembly.Assembly == nil || assembly.Assembly.Scope != scope || assembly.Assembly.Status != "committed" {
			return "", reject("forecast report commit is incomplete")
		}
		for _, diagnostic := range assembly.Diagnostics {
			if diagnostic.Severity == "error" {
				return "", reject(diagnostic.Message)
			}
		}
		if err = validateReportReferences(assembly.Assembly.Source, assembly.Assembly.DataSources); err != nil {
			return "", err
		}
	}
	g.frames = candidate
	return body, nil
}

func rejectInlineData(value any) error {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			switch key {
			case "values", "series", "points", "markers":
				if values, ok := child.([]any); ok {
					for _, item := range values {
						if _, numeric := item.(json.Number); numeric {
							return reject("inline chart array bypasses bound forecast data")
						}
					}
				}
			case "rows", "data", "cells", "dataSources", "staticDataSources", "datasets":
				return reject("inline " + key + " bypasses bound forecast data")
			case "value":
				if _, numeric := child.(json.Number); numeric {
					return reject("inline numeric value bypasses bound forecast data")
				}
			}
			if err := rejectInlineData(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := rejectInlineData(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateReportReferences(source map[string]any, datasets map[string]json.RawMessage) error {
	dataKinds := map[string]bool{"kpiBlock": true, "tableBlock": true, "chartBlock": true, "collectionBlock": true, "geoMapBlock": true}
	layoutKinds := map[string]bool{"sectionBlock": true, "textBlock": true, "markdownBlock": true, "tabGroupBlock": true, "dividerBlock": true, "imageBlock": true}
	var visit func(any) error
	visit = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			kind, _ := v["kind"].(string)
			if strings.HasSuffix(kind, "Block") {
				if !dataKinds[kind] && !layoutKinds[kind] {
					return reject("unsupported forecast block kind " + kind)
				}
				if dataKinds[kind] {
					ref, _ := v["datasetRef"].(string)
					if ref == "" && len(datasets) == 1 {
						for key := range datasets {
							ref = key
						}
					}
					if _, exists := datasets[ref]; !exists {
						return reject(fmt.Sprintf("forecast block %v requires a verified datasetRef", v["id"]))
					}
				}
			}
			for _, child := range v {
				if err := visit(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(source)
}
