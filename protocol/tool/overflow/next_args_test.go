package overflow

import "testing"

func TestExtractMessageShowNextArgs_YAML(t *testing.T) {
	body := `overflow: true
messageId: source-msg
nextArgs:
  messageId: source-msg
  byteRange:
    from: 57600
    to: 65912
nextRange:
  bytes:
    offset: 57600
    length: 8312
hasMore: true
useToolToSeeMore: message-show
content: |
  partial body`

	got, ok := ExtractMessageShowNextArgs(body)
	if !ok {
		t.Fatalf("expected nextArgs from yaml wrapper")
	}
	if got["messageId"] != "source-msg" {
		t.Fatalf("messageId = %v, want source-msg", got["messageId"])
	}
	byteRange, ok := got["byteRange"].(map[string]interface{})
	if !ok {
		t.Fatalf("byteRange missing from nextArgs: %#v", got)
	}
	if byteRange["from"] != 57600 {
		t.Fatalf("byteRange.from = %v, want 57600", byteRange["from"])
	}
	if byteRange["to"] != 65912 {
		t.Fatalf("byteRange.to = %v, want 65912", byteRange["to"])
	}
}

func TestExtractMessageShowNextArgs_JSON(t *testing.T) {
	body := `{"messageId":"source-msg","nextArgs":{"messageId":"source-msg","byteRange":{"from":900,"to":1800}},"continuation":{"hasMore":true,"nextRange":{"bytes":{"offset":900,"length":900}}}}`

	got, ok := ExtractMessageShowNextArgs(body)
	if !ok {
		t.Fatalf("expected nextArgs from json continuation body")
	}
	if got["messageId"] != "source-msg" {
		t.Fatalf("messageId = %v, want source-msg", got["messageId"])
	}
	byteRange, ok := got["byteRange"].(map[string]interface{})
	if !ok {
		t.Fatalf("byteRange missing from nextArgs: %#v", got)
	}
	if byteRange["from"] != float64(900) {
		t.Fatalf("byteRange.from = %v, want 900", byteRange["from"])
	}
	if byteRange["to"] != float64(1800) {
		t.Fatalf("byteRange.to = %v, want 1800", byteRange["to"])
	}
}

func TestExtractMessageShowNextArgs_ReturnsFalseWithoutContinuation(t *testing.T) {
	body := `{"messageId":"source-msg","nextArgs":{"messageId":"source-msg","byteRange":{"from":900,"to":1800}},"continuation":{"hasMore":false}}`
	if _, ok := ExtractMessageShowNextArgs(body); ok {
		t.Fatalf("expected no nextArgs when continuation is already exhausted")
	}
}
