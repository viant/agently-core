package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/viant/agently-core/pkg/jsonrepair"
)

func normalizeFeedResponseData(value interface{}) interface{} {
	var raw json.RawMessage
	switch actual := value.(type) {
	case json.RawMessage:
		raw = actual
	case *json.RawMessage:
		if actual != nil {
			raw = *actual
		}
	default:
		return value
	}
	if len(raw) == 0 || json.Valid(raw) {
		return raw
	}
	if repaired, ok := jsonrepair.NormalizeBareRedactionMarkers(string(raw)); ok {
		return json.RawMessage(repaired)
	}
	return nil
}

func normalizeFeedConfigValue(value interface{}) interface{} {
	switch actual := value.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(actual))
		for key, item := range actual {
			result[key] = normalizeFeedConfigValue(item)
		}
		return result
	case map[interface{}]interface{}:
		result := make(map[string]interface{}, len(actual))
		for key, item := range actual {
			result[fmt.Sprint(key)] = normalizeFeedConfigValue(item)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(actual))
		for index, item := range actual {
			result[index] = normalizeFeedConfigValue(item)
		}
		return result
	default:
		return value
	}
}

func handleListFeeds(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if backend, ok := client.(interface{ ListFeedSpecs() []*FeedSpec }); ok {
			specs := backend.ListFeedSpecs()
			if specs == nil {
				httpJSON(w, http.StatusOK, map[string]interface{}{"feeds": []interface{}{}})
				return
			}
			type feedSummary struct {
				ID            string            `json:"id"`
				Title         string            `json:"title"`
				DeveloperOnly bool              `json:"developerOnly,omitempty"`
				Presentation  *FeedPresentation `json:"presentation,omitempty"`
				Match         FeedMatch         `json:"match"`
			}
			result := make([]feedSummary, 0, len(specs))
			for _, s := range specs {
				result = append(result, feedSummary{ID: s.ID, Title: s.Title, DeveloperOnly: s.DeveloperOnly, Presentation: normalizedFeedPresentation(s), Match: s.Match})
			}
			httpJSON(w, http.StatusOK, map[string]interface{}{"feeds": result})
			return
		}
		httpJSON(w, http.StatusOK, map[string]interface{}{"feeds": []interface{}{}})
	}
}

func handleGetFeedData(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		feedID := r.PathValue("id")
		convID := r.URL.Query().Get("conversationId")
		if feedID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("feed id required"))
			return
		}
		backend, ok := client.(interface {
			ListFeedSpecs() []*FeedSpec
			GetTranscript(ctx context.Context, input *GetTranscriptInput, options ...TranscriptOption) (*ConversationStateResponse, error)
		})
		if !ok {
			httpError(w, http.StatusNotFound, fmt.Errorf("feed %q not found", feedID))
			return
		}
		specs := backend.ListFeedSpecs()
		if specs == nil {
			httpError(w, http.StatusNotFound, fmt.Errorf("feed %q not found", feedID))
			return
		}
		var spec *FeedSpec
		for _, s := range specs {
			if s.ID == feedID {
				spec = s
				break
			}
		}
		if spec == nil {
			httpError(w, http.StatusNotFound, fmt.Errorf("feed %q not found", feedID))
			return
		}
		transcript, err := backend.GetTranscript(r.Context(), &GetTranscriptInput{
			ConversationID:    convID,
			IncludeModelCalls: true,
			IncludeToolCalls:  true,
		}, WithIncludeFeeds())
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		var feedData interface{}
		if transcript != nil {
			for _, f := range transcript.Feeds {
				if f != nil && f.FeedID == feedID {
					feedData = f.Data
					break
				}
			}
			if feedData == nil && transcript.Conversation != nil {
				for _, f := range transcript.Conversation.Feeds {
					if f != nil && f.FeedID == feedID {
						feedData = f.Data
						break
					}
				}
			}
		}
		httpJSON(w, http.StatusOK, map[string]interface{}{
			"feedId":        spec.ID,
			"title":         spec.Title,
			"developerOnly": spec.DeveloperOnly,
			"presentation":  normalizedFeedPresentation(spec),
			"data":          normalizeFeedResponseData(feedData),
			"dataSources":   normalizeFeedConfigValue(spec.DataSource),
			"ui":            normalizeFeedConfigValue(spec.UI),
		})
	}
}
