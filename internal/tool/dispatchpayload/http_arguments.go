package dispatchpayload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

// HTTPArguments retains opaque lifecycle members alongside the ordinary tool
// arguments used for authorization, dispatch policy, and reference resolution.
type HTTPArguments struct {
	Values map[string]any
	seen   bool
	opaque map[string]json.RawMessage
}

func (a *HTTPArguments) UnmarshalJSON(data []byte) error {
	if a.seen {
		return errors.New("tool arguments supplied more than once")
	}
	a.seen = true
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	if tok != json.Delim('{') {
		return errors.New("tool arguments must be an object")
	}
	a.Values = make(map[string]any)
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		key := token.(string)
		var value any
		if strings.EqualFold(key, "definition") || strings.EqualFold(key, "sourceManifest") {
			if a.opaque == nil {
				a.opaque = make(map[string]json.RawMessage, 2)
			}
			for previous := range a.opaque {
				if strings.EqualFold(previous, key) {
					return errors.New("ambiguous tool definition or source manifest")
				}
			}
			var raw json.RawMessage
			if err = dec.Decode(&raw); err != nil {
				return err
			}
			// These members carry authored JSON. Policy must see exact numbers too.
			precise := json.NewDecoder(bytes.NewReader(raw))
			precise.UseNumber()
			if err = precise.Decode(&value); err != nil {
				return err
			}
			a.opaque[key] = raw
		} else if err = dec.Decode(&value); err != nil {
			return err
		}
		a.Values[key] = value
	}
	_, err = dec.Token()
	return err
}

type httpArgumentsKey struct{}
type httpInvocation struct {
	name  string
	input *HTTPArguments
}

func (a *HTTPArguments) Context(ctx context.Context, name string) context.Context {
	if len(a.opaque) == 0 {
		return ctx
	}
	return context.WithValue(ctx, httpArgumentsKey{}, httpInvocation{name, a})
}

// ClaimHTTPArguments binds bytes to one registry entry. Nested calls inherit
// a context without the carrier, even when they invoke the same tool.
func ClaimHTTPArguments(ctx context.Context, name string, args map[string]any) (context.Context, *HTTPArguments) {
	bound, ok := ctx.Value(httpArgumentsKey{}).(httpInvocation)
	if !ok || bound.input == nil {
		return ctx, nil
	}
	ctx = context.WithValue(ctx, httpArgumentsKey{}, httpInvocation{})
	if bound.name != name || reflect.ValueOf(bound.input.Values).Pointer() != reflect.ValueOf(args).Pointer() {
		return ctx, nil
	}
	return ctx, bound.input
}

// Wire applies only at the final MCP call. If policy or artifact substitution
// edited a member, dispatch its current value. Never resurrect the old bytes.
func (a *HTTPArguments) Wire(args map[string]any) map[string]any {
	if a == nil {
		return args
	}
	var result map[string]any
	for key, raw := range a.opaque {
		current, ok := args[key]
		if !ok {
			continue
		}
		var original any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&original); err != nil || !reflect.DeepEqual(original, current) {
			continue
		}
		if result == nil {
			result = make(map[string]any, len(args))
			for k, v := range args {
				result[k] = v
			}
		}
		result[key] = raw
	}
	if result == nil {
		return args
	}
	return result
}
