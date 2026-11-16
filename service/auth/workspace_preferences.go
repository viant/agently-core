package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	identity "github.com/viant/agently-core/protocol/resource"
	"io"
	"strings"
)

// WorkspacePreferences contains presentation choices only. It never controls
// provider connections, resource discovery or authorization.
type WorkspacePreferences struct {
	Title              string                   `json:"title,omitempty"`
	ApplicationLabels  map[string]string        `json:"applicationLabels,omitempty"`
	MenuLabels         map[string]string        `json:"menuLabels,omitempty"`
	HiddenMenus        []string                 `json:"hiddenMenus,omitempty"`
	Presentation       string                   `json:"presentation,omitempty"`
	Defaults           []WorkspaceReportDefault `json:"defaults,omitempty"`
	Enabled            *bool                    `json:"enabled,omitempty"`
	Groups             []WorkspaceGroup         `json:"groups,omitempty"`
	HiddenApplications []string                 `json:"hiddenApplications,omitempty"`
}

type WorkspaceGroup struct {
	ID    string         `json:"id"`
	Label string         `json:"label"`
	Items []WorkspacePin `json:"items,omitempty"`
}

type WorkspacePin struct {
	ID         string                     `json:"id"`
	Kind       string                     `json:"kind"`
	Provider   string                     `json:"provider"`
	URI        string                     `json:"uri"`
	Alias      string                     `json:"alias,omitempty"`
	Parameters map[string]json.RawMessage `json:"parameters,omitempty" description:"Optional launch parameters as a JSON object. Omit for the default launch."`
}

// Pin updates an isolated preference candidate. Persistence must apply the
// candidate atomically against its previously read version.
func (p *WorkspacePreferences) Pin(groupID, groupLabel string, pin WorkspacePin) error {
	if p == nil {
		return fmt.Errorf("workspace preferences required")
	}
	if groupID == "" {
		groupID = "default"
		if groupLabel == "" {
			groupLabel = "Pinned"
		}
	}
	pinIdentity, err := workspacePinIdentity(pin)
	if err != nil {
		return err
	}
	candidate := *p
	candidate.Groups = append([]WorkspaceGroup(nil), p.Groups...)
	index := -1
	for i := range candidate.Groups {
		if candidate.Groups[i].ID == groupID {
			index = i
			break
		}
	}
	if index < 0 {
		if err := workspaceGroupLabelAvailable(candidate.Groups, groupLabel, ""); err != nil {
			return err
		}
		candidate.Groups = append(candidate.Groups, WorkspaceGroup{ID: groupID, Label: groupLabel})
		index = len(candidate.Groups) - 1
	}
	group := &candidate.Groups[index]
	group.Items = append([]WorkspacePin(nil), group.Items...)
	for _, item := range group.Items {
		existingIdentity, err := workspacePinIdentity(item)
		if err != nil {
			return err
		}
		if item.ID == pin.ID {
			if existingIdentity != pinIdentity {
				return fmt.Errorf("pin identity conflicts with an existing item")
			}
			return nil
		}
		if existingIdentity == pinIdentity {
			return nil
		}
	}
	if pin.Parameters != nil {
		parameters := make(map[string]json.RawMessage, len(pin.Parameters))
		for name, value := range pin.Parameters {
			parameters[name] = append(json.RawMessage(nil), value...)
		}
		pin.Parameters = parameters
	}
	group.Items = append(group.Items, pin)
	if candidate.Enabled == nil {
		enabled := true
		candidate.Enabled = &enabled
	}
	if err := candidate.validate(); err != nil {
		return err
	}
	*p = candidate
	return nil
}

func workspaceGroupLabelAvailable(groups []WorkspaceGroup, label, exceptID string) error {
	for _, group := range groups {
		if group.ID != exceptID && strings.EqualFold(group.Label, label) {
			return fmt.Errorf("a workspace group with that name already exists; use the existing group or choose a different name")
		}
	}
	return nil
}

func samePin(a, b WorkspacePin) bool {
	x, ex := workspacePinIdentity(a)
	y, ey := workspacePinIdentity(b)
	return ex == nil && ey == nil && x == y
}

// One canonical launch identity is shared by pin retries, moves and validation.
// Missing parameters and an empty object both mean the default launch.
func workspacePinIdentity(pin WorkspacePin) (string, error) {
	var parameters any = map[string]any{}
	if len(pin.Parameters) > 0 {
		raw, err := json.Marshal(pin.Parameters)
		if err != nil {
			return "", err
		}
		if len(raw) > 16384 {
			return "", fmt.Errorf("workspace launch parameters too large")
		}
		if err := decodeWorkspaceParameters(raw, &parameters); err != nil {
			return "", err
		}
	}
	raw, err := json.Marshal([]any{pin.Kind, pin.Provider, pin.URI, parameters})
	return string(raw), err
}

func (p *WorkspacePreferences) validate() error {
	if p == nil {
		return nil
	}
	if p.Title != "" && !workspaceText(p.Title, 120) {
		return fmt.Errorf("invalid workspace title")
	}
	for _, labels := range []map[string]string{p.ApplicationLabels, p.MenuLabels} {
		if len(labels) > 1000 {
			return fmt.Errorf("workspace label limit exceeded")
		}
		for key, label := range labels {
			if !workspaceText(key, 4096) || !workspaceText(label, 120) {
				return fmt.Errorf("invalid workspace display label")
			}
		}
	}
	if len(p.HiddenMenus) > 1000 {
		return fmt.Errorf("workspace menu visibility limit exceeded")
	}
	hiddenMenus := make(map[string]bool, len(p.HiddenMenus))
	for _, key := range p.HiddenMenus {
		if !workspaceText(key, 4096) || hiddenMenus[key] {
			return fmt.Errorf("invalid or duplicate hidden menu")
		}
		hiddenMenus[key] = true
	}
	switch p.Presentation {
	case "", "menu", "tiles", "both":
	default:
		return fmt.Errorf("workspace presentation must be menu, tiles or both")
	}
	if len(p.Groups) > 50 || len(p.HiddenApplications) > 1000 || len(p.Defaults) > 500 {
		return fmt.Errorf("workspace preference limit exceeded")
	}
	seenDefaults := map[string]bool{}
	for _, entry := range p.Defaults {
		source, e1 := identity.ParseResourceURI(entry.SourceURI)
		target, e2 := identity.ParseResourceURI(entry.TargetURI)
		key := entry.SourceProvider + "\x00" + entry.SourceURI
		if !workspaceText(entry.SourceProvider, 512) || e1 != nil || e2 != nil || source.Kind != "report" || target.Kind != "report" || target.Namespace != "users" || seenDefaults[key] {
			return fmt.Errorf("invalid or duplicate personal report default")
		}
		seenDefaults[key] = true
	}
	groups, items := map[string]bool{}, map[string]bool{}
	count := 0
	for _, g := range p.Groups {
		if !workspaceText(g.ID, 512) || !workspaceText(g.Label, 120) || groups[g.ID] {
			return fmt.Errorf("invalid or duplicate workspace group")
		}
		groups[g.ID] = true
		groupIdentities := make(map[string]bool, len(g.Items))
		for _, item := range g.Items {
			count++
			if count > 500 {
				return fmt.Errorf("workspace pin limit exceeded")
			}
			if !workspaceText(item.ID, 512) || items[item.ID] || !workspaceText(item.Provider, 512) || !workspaceText(item.URI, 2048) {
				return fmt.Errorf("invalid or duplicate workspace pin")
			}
			items[item.ID] = true
			if item.Kind != "window" && item.Kind != "report" {
				return fmt.Errorf("workspace pins support windows and reports")
			}
			uri, err := identity.ParseResourceURI(item.URI)
			if err != nil || uri.Kind != item.Kind {
				return fmt.Errorf("workspace pin kind does not match resource URI")
			}
			if item.Alias != "" && !workspaceText(item.Alias, 120) {
				return fmt.Errorf("invalid workspace pin alias")
			}
			key, err := workspacePinIdentity(item)
			if err != nil {
				return err
			}
			if groupIdentities[key] {
				return fmt.Errorf("workspace group already contains this resource and launch parameters")
			}
			groupIdentities[key] = true
		}
	}
	hidden := map[string]bool{}
	for _, id := range p.HiddenApplications {
		if !workspaceText(id, 2048) || hidden[id] {
			return fmt.Errorf("invalid or duplicate hidden application")
		}
		hidden[id] = true
	}
	return nil
}
func workspaceText(s string, limit int) bool {
	return s != "" && len([]rune(s)) <= limit && strings.TrimSpace(s) == s
}

func decodeWorkspaceParameters(raw json.RawMessage, out *any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("invalid workspace launch parameters")
	}
	return nil
}

// WorkspaceReportDefault is an explicit preference, never an authorization grant.
type WorkspaceReportDefault struct {
	SourceProvider string `json:"sourceProvider"`
	SourceURI      string `json:"sourceUri"`
	TargetURI      string `json:"targetUri"`
}
