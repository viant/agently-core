package forecastbinding

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
)

// AdmitUserContext freezes optional authenticated user-origin selection/window
// input. No owner, origin, hash, or scope can be supplied in the client object.
// Empty input preserves plaintext/CLI tool-evidence provenance, not guessed text.
func AdmitUserContext(ctx context.Context, conversationID, turnID, starterID string, now time.Time, workspaceZone string, client json.RawMessage) (Admission, error) {
	a := Admission{Scope: Scope{OwnerID: authctx.EffectiveUserID(ctx), ConversationID: conversationID, TurnID: turnID}, StarterMessageID: starterID, ReceivedAt: now, TimeZone: workspaceZone, SelectionOrigin: SelectionToolEvidence, DateOrigin: DateToolEvidence}
	if len(bytes.TrimSpace(client)) == 0 {
		return a, a.Validate()
	}
	var in struct {
		Version  int `json:"version"`
		Selected []struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"selectedEntities"`
		Window *struct {
			Mode     string `json:"mode"`
			From     string `json:"from,omitempty"`
			To       string `json:"to,omitempty"`
			TimeZone string `json:"timeZone,omitempty"`
		} `json:"window,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(client))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&in); e != nil {
		return Admission{}, reject("invalid user forecast context")
	}
	var extra any
	if e := decoder.Decode(&extra); e != io.EOF {
		return Admission{}, reject("trailing user forecast context")
	}
	if in.Version != 1 || len(in.Selected) > 1 {
		return Admission{}, reject("unsupported user forecast context")
	}
	if len(in.Selected) == 1 {
		selected := in.Selected[0]
		id, e := strconv.ParseInt(selected.ID, 10, 64)
		if e != nil || id <= 0 || strconv.FormatInt(id, 10) != selected.ID || selected.Kind != "audience" {
			return Admission{}, reject("invalid selected entity")
		}
		a.SelectionOrigin = SelectionUser
		a.AudienceIDs = []int64{id}
	}
	if in.Window != nil {
		if in.Window.TimeZone != "" {
			a.TimeZone = in.Window.TimeZone
		}
		zone, e := time.LoadLocation(a.TimeZone)
		if e != nil {
			return Admission{}, reject("invalid user timezone")
		}
		switch in.Window.Mode {
		case "workspaceDefault":
			if in.Window.From != "" || in.Window.To != "" {
				return Admission{}, reject("default window contains explicit dates")
			}
			a.DateOrigin = DateWorkspaceDefault
			day := now.In(zone)
			for n := -2; n <= 0; n++ {
				a.Dates = append(a.Dates, day.AddDate(0, 0, n).Format("2006-01-02"))
			}
		case "explicit":
			first, e := time.Parse("2006-01-02", in.Window.From)
			if e != nil {
				return Admission{}, reject("invalid explicit from date")
			}
			last, e := time.Parse("2006-01-02", in.Window.To)
			if e != nil || last.Before(first) || last.Sub(first) > 366*24*time.Hour {
				return Admission{}, reject("invalid explicit to date")
			}
			a.DateOrigin = DateUserWindow
			for day := first; !day.After(last); day = day.AddDate(0, 0, 1) {
				a.Dates = append(a.Dates, day.Format("2006-01-02"))
			}
		default:
			return Admission{}, reject("unsupported user window mode")
		}
	}
	return a, a.Validate()
}
