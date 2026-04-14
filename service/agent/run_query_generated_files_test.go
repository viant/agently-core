package agent

import (
	"testing"

	gfread "github.com/viant/agently-core/pkg/agently/generatedfile/read"
)

func TestRewriteSandboxMarkdownLinks(t *testing.T) {
	files := []*gfread.GeneratedFileView{
		{ID: "gf-123", Filename: generatedFileStringPtr("mouse_story.pdf")},
	}
	input := `Created [mouse_story.pdf](sandbox:/mnt/data/mouse_story.pdf).`
	got := rewriteSandboxMarkdownLinks(input, files)
	want := `Created [mouse_story.pdf](/v1/api/generated-files/gf-123/download).`
	if got != want {
		t.Fatalf("unexpected rewritten content\nwant: %s\ngot:  %s", want, got)
	}
}

func TestRewriteSandboxMarkdownLinks_LeavesUnmatchedLinkUntouched(t *testing.T) {
	files := []*gfread.GeneratedFileView{
		{ID: "gf-123", Filename: generatedFileStringPtr("fish_story.pdf")},
	}
	input := `Created [mouse_story.pdf](sandbox:/mnt/data/mouse_story.pdf).`
	got := rewriteSandboxMarkdownLinks(input, files)
	if got != input {
		t.Fatalf("expected content to remain unchanged\nwant: %s\ngot:  %s", input, got)
	}
}

func generatedFileStringPtr(value string) *string {
	return &value
}
