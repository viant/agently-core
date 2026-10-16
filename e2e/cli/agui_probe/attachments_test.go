package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/binding"
	agentsvc "github.com/viant/agently-core/service/agent"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestInlineAttachmentUsesActualSDKUploadAndCanonicalReference(t *testing.T) {
	original := []byte("original authored fixture bytes")
	digest := sha256.Sum256(original)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer same-owner" {
			t.Error("owner credential changed")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/conversations":
			if r.Method != "POST" {
				t.Error("create method")
			}
			io.WriteString(w, `{"id":"actual-native-conversation"}`)
		case "/v1/files":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			defer r.MultipartForm.RemoveAll()
			if r.FormValue("conversationId") != "actual-native-conversation" || r.FormValue("name") != "authored.png" || r.FormValue("contentType") != "image/png" {
				t.Error("original upload metadata lost")
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				return
			}
			defer file.Close()
			actual, _ := io.ReadAll(file)
			if !bytes.Equal(actual, original) {
				t.Error("authored upload bytes changed")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "actual-resource", "resource": map[string]any{"id": "actual-resource", "uri": "scratchpad://actual-resource", "mimeType": "image/png", "sizeBytes": len(original), "sha256": hex.EncodeToString(digest[:])}})
		case "/v1/conversations/actual-native-conversation":
			io.WriteString(w, `{"id":"actual-native-conversation","aguiThreadId":"actual-wire-thread"}`)
		default:
			t.Errorf("unexpected SDK endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := newClient(server.URL, "same-owner", &observer{next: http.DefaultTransport})
	if err != nil {
		t.Fatal(err)
	}
	input := &agentsvc.QueryInput{AgentID: "simple", Query: "original query", Attachments: []*binding.Attachment{{Name: "authored.png", Mime: "image/png", Data: original}}}
	mapped, err := prepareInlineAttachments(context.Background(), client, input)
	if err != nil {
		t.Fatal(err)
	}
	if input.ConversationID != "" || !bytes.Equal(input.Attachments[0].Data, original) || input.Attachments[0].URI != "" {
		t.Fatal("authored input mutated")
	}
	prepared, err := client.PrepareAGUIChat(context.Background(), mapped)
	if err != nil {
		t.Fatal(err)
	}
	var forward agui.ForwardedProps
	if err = json.Unmarshal(prepared.Input.ForwardedProps, &forward); err != nil {
		t.Fatal(err)
	}
	if prepared.ConversationID != "actual-native-conversation" || prepared.Input.ThreadID != "actual-wire-thread" || len(forward.Agently.Payload.Attachments) != 1 || forward.Agently.Payload.Attachments[0].URI != "/v1/files/actual-resource?conversationId=actual-native-conversation" {
		t.Fatal("actual canonical resource/identity not preserved")
	}
	if calls.Load() != 3 {
		t.Fatalf("expected create/upload/metadata exactly once, got%d", calls.Load())
	}
}
func TestAmbiguousInlineAttachmentRejectedBeforeSDKSideEffects(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	client, err := newClient(server.URL, "same-owner", &observer{next: http.DefaultTransport})
	if err != nil {
		t.Fatal(err)
	}
	_, err = prepareInlineAttachments(context.Background(), client, &agentsvc.QueryInput{Attachments: []*binding.Attachment{{URI: "scratchpad://old", Data: []byte("bytes")}}})
	if err == nil || calls.Load() != 0 {
		t.Fatal("ambiguous attachment reached side effect")
	}
}
