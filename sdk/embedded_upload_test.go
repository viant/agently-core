package sdk

import (
	"context"
	"testing"

	memdata "github.com/viant/agently-core/app/store/data/memory"
	gfread "github.com/viant/agently-core/pkg/agently/generatedfile/read"
)

func TestEmbeddedClient_UploadFileRegistersPayloadAndGeneratedFile(t *testing.T) {
	store := memdata.New()
	client := &backendClient{
		conv: store,
	}

	out, err := client.UploadFile(context.Background(), &UploadFileInput{
		ConversationID: "conv_1",
		Name:           "note.txt",
		Data:           []byte("hello"),
	})
	if err != nil {
		t.Fatalf("upload file: %v", err)
	}
	if out == nil || out.ID == "" {
		t.Fatalf("unexpected output %+v", out)
	}

	files, err := store.GetGeneratedFiles(context.Background(), &gfread.Input{ConversationID: "conv_1"})
	if err != nil {
		t.Fatalf("get generated files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("unexpected files %+v", files)
	}
	if got := *files[0].Filename; got != "note.txt" {
		t.Fatalf("unexpected file name %q", got)
	}
	if got := *files[0].MimeType; got != "application/octet-stream" {
		t.Fatalf("unexpected content type %q", got)
	}
	if got := files[0].Mode; got != "inline" {
		t.Fatalf("unexpected mode %q", got)
	}
	if got := files[0].CopyMode; got != "eager" {
		t.Fatalf("unexpected copy mode %q", got)
	}
	if files[0].PayloadID == nil || *files[0].PayloadID == "" {
		t.Fatalf("expected payload id on generated file %+v", files[0])
	}
	payload, err := store.GetPayload(context.Background(), *files[0].PayloadID)
	if err != nil {
		t.Fatalf("get payload: %v", err)
	}
	if payload == nil {
		t.Fatalf("expected payload")
	}
	if payload.InlineBody == nil || string(*payload.InlineBody) != "hello" {
		t.Fatalf("unexpected payload body %+v", payload)
	}
	if payload.MimeType != "application/octet-stream" {
		t.Fatalf("unexpected payload content type %q", payload.MimeType)
	}
}
