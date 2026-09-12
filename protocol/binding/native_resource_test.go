package binding

import (
	"bytes"
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
