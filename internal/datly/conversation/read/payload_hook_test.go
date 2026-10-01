package read

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"unicode/utf8"
)

func TestEmbeddedPayloadUsesCanonicalDecodeHook(t *testing.T) {
	original := []byte(`{"text":"café 日本語", "marker":"ORANGE-42"}`)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(original); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	raw := compressed.String()
	payload := &ModelCallStreamPayloadView{Id: "owned", Compression: "gzip", InlineBody: &raw}
	if err := payload.OnFetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if payload.Compression != "" || !utf8.ValidString(*payload.InlineBody) || sha256.Sum256([]byte(*payload.InlineBody)) != sha256.Sum256(original) {
		t.Fatalf("decoded payload differs from original or retains compression")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var presented ModelCallStreamPayloadView
	if err := json.Unmarshal(encoded, &presented); err != nil {
		t.Fatal(err)
	}
	if presented.InlineBody == nil || *presented.InlineBody != string(original) {
		t.Fatal("JSON transport changed decoded UTF-8 bytes")
	}
	if err := payload.OnFetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *payload.InlineBody != string(original) {
		t.Fatal("repeat fetch changed decoded body")
	}
}

func TestEmbeddedPayloadCanonicalNilAndUncompressed(t *testing.T) {
	empty := &ModelCallStreamPayloadView{Compression: "gzip"}
	if err := empty.OnFetch(context.Background()); err != nil || empty.InlineBody != nil {
		t.Fatal("nil body changed")
	}
	body := "  {\"marker\":\"ORANGE-42\"} \n"
	p := &ModelCallStreamPayloadView{Compression: "none", InlineBody: &body}
	if err := p.OnFetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *p.InlineBody != `{"marker":"ORANGE-42"}` || p.Compression != "none" {
		t.Fatal("canonical trim/compression behavior changed")
	}
}
