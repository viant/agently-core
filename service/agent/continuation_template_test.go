package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/service/core"
)

func TestContinuationTemplatePublicationRequiresSuccessfulMatchingAnchor(t *testing.T) {
	var publication continuationTemplatePublication
	doc := &binding.Document{SourceURI: "template://dashboard", PageContent: "v1", RefreshOnContinuation: true}
	historical := &binding.Document{PageContent: "historical", RefreshOnContinuation: false}
	b := &binding.Binding{}
	b.SystemDocuments.Items = []*binding.Document{doc, historical}
	b.History.LastResponse = &binding.Trace{ID: "old"}
	input := &core.GenerateInput{Binding: b, Prompt: &binding.Prompt{Text: "continue"}}
	digest, restore := publication.prepare(input)
	require.True(t, doc.RefreshOnContinuation)
	restore()
	// Failed/skipped generation cannot consume publication, even with reused binding/doc objects.
	publication.accept(digest, nil)
	publication.accept(digest, &llm.GenerateResponse{})
	retryDigest, restore := publication.prepare(input)
	require.Equal(t, digest, retryDigest)
	require.True(t, doc.RefreshOnContinuation)
	restore()
	publication.accept(digest, &llm.GenerateResponse{ResponseID: "accepted-1"})
	b.History.LastResponse.ID = "accepted-1"
	require.NoError(t, input.Init(t.Context()))
	require.True(t, input.Message[0].RefreshOnContinuation)
	unchangedDigest, restore := publication.prepare(input)
	require.Equal(t, digest, unchangedDigest)
	require.False(t, doc.RefreshOnContinuation)
	// Suppression affects only the delta marker; the full request retains the document.
	require.Equal(t, "v1", input.Message[0].Content)
	require.False(t, input.Message[0].RefreshOnContinuation)
	restore()
	require.True(t, doc.RefreshOnContinuation)
	require.True(t, input.Message[0].RefreshOnContinuation)
	require.False(t, historical.RefreshOnContinuation)
	// A failed unchanged iteration restores trusted provenance and leaves the accepted anchor intact.
	_, restore = publication.prepare(input)
	require.False(t, doc.RefreshOnContinuation)
	restore()
	publication.accept(digest, nil)
	require.Equal(t, "accepted-1", publication.responseID)
	// Edits on the same document object must republish despite a matching accepted anchor.
	doc.PageContent = "v2"
	changedDigest, restore := publication.prepare(input)
	require.NotEqual(t, digest, changedDigest)
	require.True(t, doc.RefreshOnContinuation)
	restore()
	// Failed publication cannot mark edited content as accepted.
	publication.accept(changedDigest, nil)
	_, restore = publication.prepare(input)
	require.True(t, doc.RefreshOnContinuation)
	restore()
	// The same object with unchanged bytes but another anchor also republishes.
	doc.PageContent = "v1"
	b.History.LastResponse.ID = "other-chain"
	_, restore = publication.prepare(input)
	require.True(t, doc.RefreshOnContinuation)
	restore()
	// Successful unchanged iterations advance proof and restore the marker each time.
	publication.accept(digest, &llm.GenerateResponse{ResponseID: "accepted-2"})
	b.History.LastResponse.ID = "accepted-2"
	for range 2 {
		nextDigest, restore := publication.prepare(input)
		require.Equal(t, digest, nextDigest)
		require.False(t, doc.RefreshOnContinuation)
		restore()
		require.True(t, doc.RefreshOnContinuation)
	}
	plain := &binding.Binding{}
	plainDigest, restore := publication.prepare(&core.GenerateInput{Binding: plain})
	require.Empty(t, plainDigest)
	restore()
	require.Empty(t, plain.SystemDocuments.Items)
}
