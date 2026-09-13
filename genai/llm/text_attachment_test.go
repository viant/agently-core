package llm

import (
	"strings"
	"testing"
)

func TestTextAttachmentContent(t *testing.T) {
	for _, mimeType := range []string{"text/csv", "text/csv; charset=utf-8", "application/json", "text/plain"} {
		item, ok := TextAttachmentContent([]byte("date,spend\n2026-09-09,300\n"), mimeType, "data.csv")
		if !ok || item.Type != ContentTypeText || !strings.Contains(item.Text, "2026-09-09,300") {
			t.Fatalf("text attachment lost for %s: %#v", mimeType, item)
		}
	}
	for _, tc := range []struct {
		mime string
		data []byte
	}{{"image/png", []byte("bytes")}, {"application/octet-stream", []byte("bytes")}, {"text/csv", []byte{255}}, {"text/csv", []byte{'x', 0}}} {
		if _, ok := TextAttachmentContent(tc.data, tc.mime, "file"); ok {
			t.Fatalf("unexpected conversion: %s", tc.mime)
		}
	}
}
func TestNewMessageWithBinariesPreservesTextUploads(t *testing.T) {
	csv := &AttachmentItem{Name: "data.csv", MimeType: "text/csv", Data: []byte("spend\n300")}
	msg := NewMessageWithBinaries(RoleUser, []*AttachmentItem{csv}, "analyze")
	if len(msg.Items) != 2 || msg.Items[0].Type != ContentTypeText {
		t.Fatalf("text-only model would lose CSV: %#v", msg.Items)
	}
	csv.Native = true
	msg = NewMessageWithBinaries(RoleUser, []*AttachmentItem{csv}, "analyze")
	if msg.Items[0].Type != ContentTypeBinary || msg.Items[0].Metadata["nativePresentation"] != true {
		t.Fatal("native opt-in changed")
	}
}
