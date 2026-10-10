package tool

import (
	"context"
	"encoding/json"
	"fmt"
	authctx "github.com/viant/agently-core/internal/auth"
	"math"
	"math/big"
	"strconv"
	"strings"
)

type exactJSONKey struct{}
type exactJSONRequest struct{ cursorPath string }

// ExecuteExactJSON is selected only by opt-in datasource paging. It keeps all
// ordinary admission/dispatch protection, but excludes reuse of decoded results.
func (r *Registry) ExecuteExactJSON(ctx context.Context, name string, args map[string]interface{}, cursorPath string) (string, error) {
	return r.Execute(context.WithValue(ctx, exactJSONKey{}, &exactJSONRequest{cursorPath: cursorPath}), name, args)
}
func exactJSONScope(ctx context.Context) *exactJSONRequest {
	scope, _ := ctx.Value(exactJSONKey{}).(*exactJSONRequest)
	return scope
}
func hasUnsafeDecodedNumber(value interface{}) bool {
	switch v := value.(type) {
	case float64, float32:
		// The upstream decoder discarded the original numeric spelling. Even an
		// integer here may have rounded from a fractional or larger raw token.
		return true
	case map[string]interface{}:
		for _, item := range v {
			if hasUnsafeDecodedNumber(item) {
				return true
			}
		}
	case []interface{}:
		for _, item := range v {
			if hasUnsafeDecodedNumber(item) {
				return true
			}
		}
	}
	return false
}
func hasSensitiveJSONNumber(value interface{}) bool {
	switch v := value.(type) {
	case json.Number:
		return !numberSurvivesFloatCodec(v.String())
	case int64:
		return !numberSurvivesFloatCodec(strconv.FormatInt(v, 10))
	case int:
		return !numberSurvivesFloatCodec(strconv.FormatInt(int64(v), 10))
	case uint64:
		return !numberSurvivesFloatCodec(strconv.FormatUint(v, 10))
	case map[string]interface{}:
		for _, item := range v {
			if hasSensitiveJSONNumber(item) {
				return true
			}
		}
	case []interface{}:
		for _, item := range v {
			if hasSensitiveJSONNumber(item) {
				return true
			}
		}
	}
	return false
}
func (r *Registry) validateExactJSONRequest(ctx context.Context, server string, args map[string]interface{}) error {
	if exactJSONScope(ctx) == nil || !hasSensitiveJSONNumber(args) {
		return nil
	}
	if r.mgr == nil {
		return fmt.Errorf("lossless numeric request unsupported by unknown MCP codec")
	}
	opts, err := r.mgr.Options(ctx, server)
	if err != nil {
		return err
	}
	// The verified current client injects July metadata through a float64 map.
	// The older HTTP codec serializes args directly; authenticated stdio rewrites.
	if opts == nil || opts.ClientOptions == nil {
		return fmt.Errorf("lossless numeric request unsupported by unknown MCP codec")
	}
	version := opts.ClientOptions.ProtocolVersion
	if version != "2025-06-18" && version != "2025-11-25" {
		return fmt.Errorf("lossless numeric request unsupported by current MCP metadata codec")
	}
	if strings.EqualFold(opts.ClientOptions.Transport.Type, "stdio") && authctx.MCPAuthToken(ctx, r.mgr.UseIDToken(ctx, server)) != "" {
		return fmt.Errorf("lossless numeric request unsupported by current stdio auth codec")
	}
	return nil
}
func validateExactStructuredCursor(ctx context.Context, result interface{}) error {
	scope := exactJSONScope(ctx)
	if scope == nil || scope.cursorPath == "" {
		return nil
	}
	current := result
	for _, part := range strings.Split(scope.cursorPath, ".") {
		if part == "" {
			continue
		}
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current = object[part]
	}
	if hasUnsafeDecodedNumber(current) {
		return fmt.Errorf("lossless numeric cursor unsupported by current MCP structuredContent codec")
	}
	return nil
}

func numberSurvivesFloatCodec(text string) bool {
	original, ok := new(big.Rat).SetString(text)
	if !ok {
		return false
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return false
	}
	encoded, err := json.Marshal(number)
	if err != nil {
		return false
	}
	transmitted, ok := new(big.Rat).SetString(string(encoded))
	return ok && transmitted.Cmp(original) == 0
}
