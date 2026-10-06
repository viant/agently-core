package sdk

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/viant/agently-core/runtime/streaming"
)

const aguiInteractiveFallback = "[Interactive content]"

type aguiPresentationState struct {
	raw, visible, fingerprint string
	unresolved                bool
}
type aguiPresentation struct {
	messages map[string]*aguiPresentationState
	seen     map[string]map[int64]bool
	internal map[string]bool
}

// project keeps authoring payloads out of standard text messages. Recognized
// Forge transactions travel as typed activities; unaware clients retain useful
// prose and a short fallback instead of displaying authoring JSON as chat.
func (p *aguiPresentation) project(ctx context.Context, event *streaming.Event, client Client) []*streaming.Event {
	if event == nil {
		return nil
	}
	// Keep lifecycle/usage data, but never publish internal assistant bodies.
	id := event.AssistantMessageID
	if id == "" {
		id = event.MessageID
	}
	internalKey := event.ConversationID + "\x00" + event.TurnID + "\x00" + id
	if streaming.IsInternalMessageMode(event.Mode) && id != "" {
		if p.internal == nil {
			p.internal = map[string]bool{}
		}
		p.internal[internalKey] = true
	}
	internal := streaming.IsInternalMessageMode(event.Mode) || p.internal[internalKey]
	userPatch := event.Type == streaming.EventTypeAssistant && event.Patch["role"] == "user"
	if internal && !userPatch {
		switch event.Type {
		case streaming.EventTypeTextDelta, streaming.EventTypeAssistant, streaming.EventTypeItemCompleted, streaming.EventTypeNarration:
			return nil
		case streaming.EventTypeModelCompleted:
			copy := *event
			copy.Content = ""
			copy.Narration = ""
			copy.RenderedContent = nil
			return []*streaming.Event{&copy}
		}
	}
	if event.Type == streaming.EventTypeNarration {
		copy := *event
		copy.Content, _ = plainAGUIContent(event.Content, true)
		copy.Narration, _ = plainAGUIContent(event.Narration, true)
		copy.RenderedContent = nil
		return []*streaming.Event{&copy}
	}
	switch event.Type {
	case streaming.EventTypeTextDelta, streaming.EventTypeAssistant, streaming.EventTypeModelCompleted, streaming.EventTypeItemCompleted:
	default:
		return []*streaming.Event{event}
	}
	if event.Type == streaming.EventTypeAssistant && event.Patch["role"] == "user" {
		return []*streaming.Event{event}
	}
	if event.EventSeq > 0 {
		scope := event.ConversationID + "\x00" + event.TurnID
		if p.seen == nil {
			p.seen = map[string]map[int64]bool{}
		}
		if p.seen[scope] == nil {
			p.seen[scope] = map[int64]bool{}
		}
		if p.seen[scope][event.EventSeq] {
			return nil
		}
		p.seen[scope][event.EventSeq] = true
	}
	if id == "" {
		return []*streaming.Event{event}
	}
	key := event.ConversationID + "\x00" + event.TurnID + "\x00" + id
	if p.messages == nil {
		p.messages = map[string]*aguiPresentationState{}
	}
	state := p.messages[key]
	if state == nil {
		state = &aguiPresentationState{}
		p.messages[key] = state
	}
	previousRaw := state.raw
	if state.unresolved && event.Type == streaming.EventTypeTextDelta && event.ContentMode != "snapshot" {
		return nil
	}
	if event.Type == streaming.EventTypeTextDelta {
		if event.ContentMode == "snapshot" {
			state.raw = event.Content
			state.unresolved = false
		} else if event.ContentOffset != nil {
			offset := *event.ContentOffset
			if offset < 0 || offset > len(state.raw) {
				state.unresolved = true
				return nil // Never forward an unprojected authoring fragment.
			}
			end := offset + len(event.Content)
			if end > len(state.raw) {
				overlap := len(state.raw) - offset
				if state.raw[offset:] != event.Content[:overlap] {
					state.unresolved = true
					return nil
				}
				state.raw += event.Content[overlap:]
			} else if state.raw[offset:end] != event.Content {
				state.unresolved = true
				return nil
			}
		} else {
			state.raw += event.Content
		}
	} else if event.Content != "" {
		state.raw = event.Content
		state.unresolved = false
	} else if state.unresolved {
		return nil
	}
	terminal := event.Type != streaming.EventTypeTextDelta
	visible, rich := plainAGUIContent(state.raw, terminal)
	copy := *event
	if rich && (terminal || containsStreamingFenceBoundary(previousRaw, event.Content)) {
		rendered := normalizeRenderedContent(state.raw, terminal)
		if rendered != nil {
			applyInlineReportWorkspaceCatalog(ctx, rendered, client)
			encoded, _ := json.Marshal(rendered)
			fingerprint := string(encoded)
			if fingerprint != state.fingerprint {
				copy.RenderedContent = rendered
				state.fingerprint = fingerprint
			}
		}
	}
	var output []*streaming.Event
	if event.Type == streaming.EventTypeTextDelta {
		if strings.HasPrefix(visible, state.visible) {
			offset := len(state.visible)
			copy.Content = visible[offset:]
			copy.ContentMode = "delta"
			copy.ContentOffset = &offset
		} else {
			copy.Type = streaming.EventTypeAssistant
			copy.Content = visible
			copy.ContentMode = "snapshot"
			copy.ContentOffset = nil
			copy.Patch = map[string]any{"role": "assistant"}
		}
	} else {
		copy.Content = visible
		copy.ContentMode = "snapshot"
		copy.ContentOffset = nil
		if event.Type != streaming.EventTypeAssistant && visible != state.visible {
			snapshot := copy
			snapshot.Type = streaming.EventTypeAssistant
			snapshot.EventSeq = 0
			snapshot.Patch = map[string]any{"role": "assistant"}
			snapshot.RenderedContent = nil
			output = append(output, &snapshot)
		}
	}
	state.visible = visible
	return append(output, &copy)
}

func plainAGUIContent(raw string, complete bool) (string, bool) {
	var out strings.Builder
	richSeen := false
	for cursor := 0; cursor < len(raw); {
		index := strings.Index(raw[cursor:], "```")
		if index < 0 {
			tail := raw[cursor:]
			if !complete {
				for count := 0; count < 2 && strings.HasSuffix(tail, "`"); count++ {
					tail = tail[:len(tail)-1]
				}
			}
			out.WriteString(tail)
			break
		}
		open := cursor + index
		out.WriteString(raw[cursor:open])
		languageStart := open + 3
		languageEnd := languageStart
		for languageEnd < len(raw) && renderedLanguageByte(raw[languageEnd]) {
			languageEnd++
		}
		language := strings.ToLower(raw[languageStart:languageEnd])
		recognized := language == "forge-ui" || language == "forge-report" || language == "forge-data"
		if languageEnd == len(raw) && !complete {
			prefix := false
			for _, known := range []string{"forge-ui", "forge-report", "forge-data"} {
				prefix = prefix || strings.HasPrefix(known, language)
			}
			if prefix {
				break
			}
			out.WriteString(raw[open:])
			break
		}
		_, body, valid := renderedFenceHeaderAndBodyStart(raw, languageEnd)
		if !valid {
			if recognized {
				if !complete {
					break
				}
				if !richSeen {
					out.WriteString(aguiInteractiveFallback)
				}
				richSeen = true
				break
			}
			out.WriteString("```")
			cursor = languageStart
			continue
		}
		close := renderedFenceClose(raw, body, language)
		if recognized {
			if !richSeen {
				out.WriteString(aguiInteractiveFallback)
			}
			richSeen = true
			if close < 0 {
				break
			}
			cursor = close + 3
			continue
		}
		if close < 0 {
			out.WriteString(raw[open:])
			break
		}
		out.WriteString(raw[open : close+3])
		cursor = close + 3
	}
	return out.String(), richSeen
}
