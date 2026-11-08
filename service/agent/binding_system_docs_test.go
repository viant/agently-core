package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/service/shared/toolexec"
)

func TestTranscriptSystemDocuments(t *testing.T) {
	content := "file: workspace://localhost/doc.md\n```md\ncontent\n````"
	msg := &apiconv.Message{
		Id:             "msg-1",
		Role:           "system",
		Content:        strPtr(content),
		Tags:           strPtr(toolexec.SystemDocumentTag),
		ContextSummary: strPtr("workspace://localhost/doc.md"),
	}
	dup := &apiconv.Message{
		Id:      "msg-dup",
		Role:    "system",
		Content: strPtr(content),
	}
	turnDup := &apiconv.Turn{Id: "turn-1", Message: []*conversationmodel.MessageView{(*conversationmodel.MessageView)(msg), (*conversationmodel.MessageView)(dup)}}
	docs := transcriptSystemDocuments(apiconv.Transcript{turnDup}, "")

	require.Len(t, docs, 1)
	assert.Equal(t, "workspace://localhost/doc.md", docs[0].SourceURI)
	assert.Equal(t, "doc.md", docs[0].Title)
	assert.Equal(t, "msg-1", docs[0].Metadata["messageId"])
	assert.Equal(t, "turn-1", docs[0].Metadata["turnId"])

	// Ensure appendTranscriptSystemDocs dedupes on source URI
	b := &binding.Binding{}
	svc := &Service{}
	svc.appendTranscriptSystemDocs(apiconv.Transcript{turnDup}, b)
	svc.appendTranscriptSystemDocs(apiconv.Transcript{turnDup}, b)
	require.Len(t, b.SystemDocuments.Items, 1)
}

func TestExtractSystemDocSourceFallback(t *testing.T) {
	content := "FILE: workspace://fallback/doc.txt\nbody"
	msg := &apiconv.Message{
		Id:      "msg-2",
		Role:    "system",
		Content: strPtr(content),
		Tags:    strPtr(toolexec.SystemDocumentTag),
	}
	turn := &apiconv.Turn{Id: "turn-2", Message: []*conversationmodel.MessageView{(*conversationmodel.MessageView)(msg)}}
	docs := transcriptSystemDocuments(apiconv.Transcript{turn}, "")
	require.Len(t, docs, 1)
	assert.Equal(t, "workspace://fallback/doc.txt", docs[0].SourceURI)
	assert.Equal(t, "doc.txt", docs[0].Title)
}

func TestRestoredTemplateDocumentFollowsRequestingTurn(t *testing.T) {
	makeTurn := func(id, creator, source, body string) *apiconv.Turn {
		msg := &apiconv.Message{Id: id + "-message", Role: "system", Content: strPtr(body),
			CreatedByUserId: strPtr(creator), Tags: strPtr(toolexec.SystemDocumentTag), ContextSummary: strPtr(source)}
		return &apiconv.Turn{Id: id, Message: []*conversationmodel.MessageView{(*conversationmodel.MessageView)(msg)}}
	}
	old := makeTurn("report-turn", "template", "template://dashboard", "original report instructions")
	knowledge := makeTurn("knowledge-turn", "knowledge", "workspace://glossary.md", "retained glossary")
	selected := makeTurn("second-report", "template", "template://dashboard", "reselected instructions")
	tr := apiconv.Transcript{old, knowledge, selected}
	for _, tc := range []struct {
		turn     string
		expected []string
	}{
		{"summary-turn", []string{"retained glossary"}},
		{"report-turn", []string{"original report instructions", "retained glossary"}},
		{"second-report", []string{"retained glossary", "reselected instructions"}},
		{"", []string{"retained glossary"}},
	} {
		b := &binding.Binding{}
		b.History.CurrentTurnID = tc.turn
		(&Service{}).appendTranscriptSystemDocs(tr, b)
		var bodies []string
		for _, d := range b.SystemDocuments.Items {
			bodies = append(bodies, d.PageContent)
		}
		require.Equal(t, tc.expected, bodies, "turn %q", tc.turn)
	}
}

func strPtr(val string) *string {
	return &val
}
