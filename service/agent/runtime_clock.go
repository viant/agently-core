package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/protocol/binding"
)

const runtimeClockSourceURI = "internal://runtime/date-context"

func appendRuntimeClockSystemDocument(b *binding.Binding, now time.Time) {
	if b == nil {
		return
	}
	for _, doc := range b.SystemDocuments.Items {
		if strings.EqualFold(strings.TrimSpace(doc.SourceURI), runtimeClockSourceURI) {
			return
		}
	}
	zone := strings.TrimSpace(now.Location().String())
	if zone == "" {
		zone = "Local"
	}
	currentDate := now.Format("2006-01-02")
	previousDate := now.AddDate(0, 0, -1).Format("2006-01-02")
	content := fmt.Sprintf(
		"# Runtime Date Context\n\n- current_date: %s\n- previous_date: %s\n- timezone: %s\n",
		currentDate,
		previousDate,
		zone,
	)
	b.SystemDocuments.Items = append(b.SystemDocuments.Items, &binding.Document{
		Title:       "Runtime Date Context",
		PageContent: content,
		SourceURI:   runtimeClockSourceURI,
		MimeType:    "text/markdown",
		Metadata: map[string]string{
			"kind": "runtime_date_context",
		},
	})
}
