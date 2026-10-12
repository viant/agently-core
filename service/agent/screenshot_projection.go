package agent

import (
	"sort"

	"github.com/viant/agently-core/protocol/binding"
)

// Project only verified screenshot attachments. Stored artifacts, payloads and
// transcript messages remain unchanged; older images keep their owned references.
func (s *Service) projectScreenshotAttachments(history *binding.History, extra []*binding.Attachment) {
	limit := 2
	if s.defaults != nil {
		limit = s.defaults.Projection.ScreenshotImageLimit()
	}
	var screenshots []*binding.Attachment
	add := func(attachments []*binding.Attachment) {
		for _, attachment := range attachments {
			if attachment != nil && attachment.Role == "screenshot" && attachment.ResourceURI != "" {
				attachment.OmitModelBytes = false
				screenshots = append(screenshots, attachment)
			}
		}
	}
	for _, turn := range append(append([]*binding.Turn{}, history.Past...), history.Current) {
		if turn != nil {
			for _, message := range turn.Messages {
				if message != nil {
					add(message.Attachment)
				}
			}
		}
	}
	add(extra)
	if limit < 0 {
		return
	}
	sort.SliceStable(screenshots, func(i, j int) bool { return screenshots[i].PresentationAt.Before(screenshots[j].PresentationAt) })
	seen := map[string]bool{}
	kept := 0
	for index := len(screenshots) - 1; index >= 0; index-- {
		attachment := screenshots[index]
		if seen[attachment.ResourceURI] || kept >= limit {
			attachment.OmitModelBytes = true
			continue
		}
		seen[attachment.ResourceURI] = true
		kept++
	}
}
