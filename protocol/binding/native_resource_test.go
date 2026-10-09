package binding

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

func TestExplicitNativePDFPreservesBinary(t *testing.T) {
	f := fpdf.New("P", "mm", "A4", "")
	f.AddPage()
	f.SetFont("Arial", "", 12)
	f.Cell(40, 10, "PDF text")
	var b bytes.Buffer
	require.NoError(t, f.Output(&b))
	for _, native := range []bool{false, true} {
		msg := &Message{Role: "user", Content: "Read PDF", Attachment: []*Attachment{{Name: "a.pdf", Mime: "application/pdf", Data: b.Bytes(), Native: native}}}
		out := msg.ToLLM()
		require.NotEmpty(t, out.Items)
		if native {
			require.Equal(t, llm.ContentTypeBinary, out.Items[0].Type)
			require.Equal(t, true, out.Items[0].Metadata["nativePresentation"])
		} else {
			require.Equal(t, llm.ContentTypeText, out.Items[0].Type)
			require.Contains(t, out.Items[0].Text, "PDF text")
		}
	}
}

func TestVerifiedWorkbookModelPresentationOmitsBytes(t *testing.T) {
	data := []byte("private workbook bytes")
	for _, ordered := range []bool{false, true} {
		attachment := &Attachment{Name: "book.xlsx", Mime: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ResourceURI: "scratchpad://artifact/verified", Data: data}
		message := &Message{Role: "user", Content: "Use workbook", Attachment: []*Attachment{attachment}}
		if ordered {
			message.ContentItems = []llm.ContentItem{llm.NewTextContent("Use workbook"), llm.NewBinaryContent(data, attachment.Mime, attachment.Name)}
		}
		out := message.ToLLM()
		require.Contains(t, out.Content, attachment.ResourceURI)
		for _, item := range out.Items {
			require.Equal(t, llm.ContentTypeText, item.Type)
			require.NotContains(t, item.Data, base64.StdEncoding.EncodeToString(data))
		}
		require.Equal(t, data, attachment.Data, "stored attachment bytes are retained")
	}
	for _, mime := range []string{"application/pdf", "image/png"} {
		out := (&Message{Role: "user", Attachment: []*Attachment{{Name: "native", Mime: mime, Native: true, ResourceURI: "scratchpad://artifact/verified", Data: data}}}).ToLLM()
		require.Equal(t, base64.StdEncoding.EncodeToString(data), out.Items[0].Data)
	}
	out := (&Message{Role: "user", Attachment: []*Attachment{{Name: "unverified.xlsx", Mime: "application/octet-stream", URI: "scratchpad://artifact/unverified", Data: data}}}).ToLLM()
	require.Equal(t, base64.StdEncoding.EncodeToString(data), out.Items[0].Data)
	metadata := &Attachment{Name: "book.xlsx", Mime: "application/octet-stream", ResourceURI: "scratchpad://artifact/verified"}
	ordered := (&Message{Role: "user", Attachment: []*Attachment{metadata}, ContentItems: []llm.ContentItem{llm.NewBinaryContent(data, metadata.Mime, metadata.Name)}}).ToLLM()
	require.Len(t, ordered.Items, 1)
	require.Equal(t, llm.ContentTypeText, ordered.Items[0].Type)
}

func TestUploadedResourceReferenceRequiresServerProvenance(t *testing.T) {
	for _, verified := range []bool{false, true} {
		attachment := &Attachment{Name: "owned.xlsx", URI: "scratchpad://artifact/owned", Data: []byte("owned bytes"), Mime: "application/octet-stream"}
		if verified {
			attachment.ResourceURI = attachment.URI
		}
		message := (&Message{Role: "user", Content: "Export the uploaded workbook", Attachment: []*Attachment{attachment}}).ToLLM()
		require.Equal(t, verified, strings.Contains(message.Content, "scratchpad://artifact/owned"))
		require.NotContains(t, message.Content, "owned bytes")
	}
	var attachment Attachment
	require.NoError(t, json.Unmarshal([]byte(`{"uri":"scratchpad://artifact/foreign","ResourceURI":"scratchpad://artifact/foreign","resourceURI":"scratchpad://artifact/foreign"}`), &attachment))
	require.Empty(t, attachment.ResourceURI, "wire metadata cannot install trusted provenance")
	require.Empty(t, AttachmentResourceReferences([]*Attachment{nil, &attachment}))
}
