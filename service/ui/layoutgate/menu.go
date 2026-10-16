package layoutgate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/viant/agently-core/service/policy"
	"github.com/viant/agently-core/service/ui/permittedview"
	forgetypes "github.com/viant/forge/backend/types"
)

// MenuProjection applies the existing authored app/menu gate along each exact
// ancestor chain. It preserves all original fields/actions and filters only
// nodes; identical actions in different branches never imply shared grants.
func MenuProjection(runtime *permittedview.Runtime, admission *policy.Runtime) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var root map[string]json.RawMessage
		if json.Unmarshal(raw, &root) != nil {
			return nil, ErrFactsUnavailable
		}
		var nodes []json.RawMessage
		if json.Unmarshal(root["applications"], &nodes) != nil {
			return nil, ErrFactsUnavailable
		}
		var visit func([]json.RawMessage, *permittedview.Snapshot) ([]json.RawMessage, error)
		visit = func(nodes []json.RawMessage, inherited *permittedview.Snapshot) ([]json.RawMessage, error) {
			out := make([]json.RawMessage, 0, len(nodes))
			for _, raw := range nodes {
				var fields map[string]json.RawMessage
				var node struct {
					Authorization *forgetypes.AuthorizationSpec               `json:"authorization"`
					VisibleWhen   any                                         `json:"visibleWhen"`
					HiddenWhen    any                                         `json:"hiddenWhen"`
					Title         string                                      `json:"title"`
					Action        *struct{ Type, Provider, WindowKey string } `json:"action"`
				}
				if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(raw, &node) != nil {
					return nil, ErrFactsUnavailable
				}
				allowed, snapshot, err := ResolveNode(ctx, node.Authorization, node.VisibleWhen, inherited, runtime)
				if errors.Is(err, ErrFactsUnavailable) {
					continue
				}
				if err != nil {
					return nil, err
				}
				if !allowed {
					continue
				}
				if node.HiddenWhen != nil {
					hidden, _, err := ResolveNode(ctx, nil, node.HiddenWhen, snapshot, runtime)
					if errors.Is(err, ErrFactsUnavailable) {
						continue
					}
					if err != nil {
						return nil, err
					}
					if hidden {
						continue
					}
				}
				childKey := "menus"
				if _, ok := fields["children"]; ok {
					childKey = "children"
				}
				if children, ok := fields[childKey]; ok {
					var parsed []json.RawMessage
					if json.Unmarshal(children, &parsed) != nil {
						return nil, ErrFactsUnavailable
					}
					filtered, err := visit(parsed, snapshot)
					if err != nil {
						return nil, err
					}
					if len(filtered) == 0 && node.Action == nil {
						continue
					}
					fields[childKey], err = json.Marshal(filtered)
					if err != nil {
						return nil, err
					}
				}
				encoded, err := json.Marshal(fields)
				if err != nil {
					return nil, err
				}
				out = append(out, encoded)
			}
			return out, nil
		}
		filtered, err := visit(nodes, nil)
		if err != nil {
			return nil, err
		}
		if admission != nil && admission.IsEnabled(policy.OperationWindowView) {
			normalize := func(value string) string {
				if admission.ExactIDs {
					return value
				}
				return strings.ToLower(value)
			}
			windowKey := func(fields map[string]json.RawMessage) string {
				var action struct{ Type, Provider, WindowKey string }
				if json.Unmarshal(fields["action"], &action) != nil || action.Type != "window" {
					return ""
				}
				if action.Provider != "" && action.Provider != "workspace" {
					return "provider:" + action.Provider + ":" + action.WindowKey
				}
				return action.WindowKey
			}
			candidates := []policy.Candidate{}
			seen := map[string]bool{}
			var collect func([]json.RawMessage)
			collect = func(nodes []json.RawMessage) {
				for _, raw := range nodes {
					var fields map[string]json.RawMessage
					_ = json.Unmarshal(raw, &fields)
					if key := windowKey(fields); key != "" && !seen[normalize(key)] {
						seen[normalize(key)] = true
						var title string
						_ = json.Unmarshal(fields["title"], &title)
						candidates = append(candidates, policy.Candidate{ID: key, Kind: "window", Metadata: map[string]any{"title": title}})
					}
					for _, childKey := range []string{"menus", "children"} {
						if child, ok := fields[childKey]; ok {
							var nested []json.RawMessage
							_ = json.Unmarshal(child, &nested)
							collect(nested)
						}
					}
				}
			}
			collect(filtered)
			allowedWindows := map[string]bool{}
			if len(candidates) > 0 {
				allowed, err := admission.Filter(ctx, policy.OperationWindowView, "", candidates, nil)
				if errors.Is(err, policy.ErrDenied) {
					allowed = nil
				} else if err != nil {
					return nil, err
				}
				for _, candidate := range allowed {
					allowedWindows[normalize(candidate.ID)] = true
				}
			}
			var prune func([]json.RawMessage) ([]json.RawMessage, error)
			prune = func(nodes []json.RawMessage) ([]json.RawMessage, error) {
				out := make([]json.RawMessage, 0, len(nodes))
				for _, raw := range nodes {
					var fields map[string]json.RawMessage
					_ = json.Unmarshal(raw, &fields)
					if key := windowKey(fields); key != "" && !allowedWindows[normalize(key)] {
						continue
					}
					empty := false
					for _, childKey := range []string{"menus", "children"} {
						if child, ok := fields[childKey]; ok {
							var nested []json.RawMessage
							_ = json.Unmarshal(child, &nested)
							children, err := prune(nested)
							if err != nil {
								return nil, err
							}
							if len(children) == 0 {
								empty = true
							}
							fields[childKey], err = json.Marshal(children)
							if err != nil {
								return nil, err
							}
						}
					}
					if empty {
						if _, hasAction := fields["action"]; !hasAction {
							continue
						}
					}
					encoded, err := json.Marshal(fields)
					if err != nil {
						return nil, err
					}
					out = append(out, encoded)
				}
				return out, nil
			}
			filtered, err = prune(filtered)
			if err != nil {
				return nil, err
			}
		}
		root["applications"], err = json.Marshal(filtered)
		if err != nil {
			return nil, err
		}
		return json.Marshal(root)
	}
}
