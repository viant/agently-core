package sdk

import (
	"fmt"
	"net/http"
)

func handleListFeeds(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ec, ok := client.(*EmbeddedClient); ok && ec.feeds != nil {
			specs := ec.feeds.Specs()
			type feedSummary struct {
				ID    string    `json:"id"`
				Title string    `json:"title"`
				Match FeedMatch `json:"match"`
			}
			result := make([]feedSummary, 0, len(specs))
			for _, s := range specs {
				result = append(result, feedSummary{ID: s.ID, Title: s.Title, Match: s.Match})
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
		ec, ok := client.(*EmbeddedClient)
		if !ok || ec.feeds == nil {
			httpError(w, http.StatusNotFound, fmt.Errorf("feed %q not found", feedID))
			return
		}
		var spec *FeedSpec
		for _, s := range ec.feeds.Specs() {
			if s.ID == feedID {
				spec = s
				break
			}
		}
		if spec == nil {
			httpError(w, http.StatusNotFound, fmt.Errorf("feed %q not found", feedID))
			return
		}
		transcript, err := ec.GetTranscript(r.Context(), &GetTranscriptInput{
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
		}
		httpJSON(w, http.StatusOK, map[string]interface{}{
			"feedId":      spec.ID,
			"title":       spec.Title,
			"data":        feedData,
			"dataSources": spec.DataSource,
			"ui":          spec.UI,
		})
	}
}
