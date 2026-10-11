package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const maxMetadataCaptureBytes = 64 << 10

// Fixed shape and hashes only. This record never contains response text,
// arbitrary field names, credentials, headers, or identifier values.
// Shape describes consumed bytes, never unseen response bytes. Invalid JSON
// without observed EOF is incomplete; Close never drains to discover more.
type conversationMetadataObservation struct {
	RequestedIDHash          string `json:"requestedIdHash"`
	Shape                    string `json:"shape"`
	FieldCount               int    `json:"fieldCount"`
	ReturnedIDPresent        bool   `json:"returnedIdPresent"`
	ReturnedIDHash           string `json:"returnedIdHash,omitempty"`
	ReturnedIDMatchesRequest bool   `json:"returnedIdMatchesRequest"`
}

func metadataRequestID(request *http.Request) (string, bool) {
	if request == nil || request.Method != http.MethodGet || request.URL == nil {
		return "", false
	}
	query := request.URL.Query()
	if len(query) != 1 || len(query["includeTranscript"]) != 1 || query.Get("includeTranscript") != "false" {
		return "", false
	}
	const prefix = "/v1/conversations/"
	path := request.URL.EscapedPath()
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	segment := strings.TrimPrefix(path, prefix)
	if segment == "" || strings.Contains(segment, "/") {
		return "", false
	}
	id, err := url.PathUnescape(segment)
	return id, err == nil && id != ""
}

type metadataCaptureBody struct {
	original              io.ReadCloser
	requestedID           string
	publish               func(conversationMetadataObservation)
	mu                    sync.Mutex
	capture               []byte
	oversize, closed, eof bool
}

func (body *metadataCaptureBody) Read(output []byte) (int, error) {
	n, err := body.original.Read(output)
	if n > 0 || errors.Is(err, io.EOF) {
		body.mu.Lock()
		if !body.closed {
			if errors.Is(err, io.EOF) {
				body.eof = true
			}
			remaining := maxMetadataCaptureBytes - len(body.capture)
			keep := n
			if keep > remaining {
				keep = remaining
				body.oversize = true
			}
			body.capture = append(body.capture, output[:keep]...)
		}
		body.mu.Unlock()
	}
	return n, err
}
func (body *metadataCaptureBody) Close() error {
	// Preserve the original Close, including its error and ability to unblock a
	// concurrent original Read. No additional Read or drain occurs here.
	err := body.original.Close()
	body.mu.Lock()
	if body.closed {
		body.mu.Unlock()
		return err
	}
	body.closed = true
	bytes, oversize, eof := body.capture, body.oversize, body.eof
	body.capture = nil
	body.mu.Unlock()
	value := summarizeMetadata(body.requestedID, bytes, oversize, eof)
	for i := range bytes {
		bytes[i] = 0
	}
	body.publish(value)
	return err
}
func summarizeMetadata(requested string, bytes []byte, oversize, eof bool) conversationMetadataObservation {
	value := conversationMetadataObservation{RequestedIDHash: digest(requested), Shape: "incomplete"}
	if eof {
		value.Shape = "malformed"
	}
	if oversize {
		value.Shape = "oversize"
		return value
	}
	var root json.RawMessage
	if json.Unmarshal(bytes, &root) != nil {
		return value
	}
	text := strings.TrimSpace(string(root))
	if text == "null" {
		value.Shape = "null"
		return value
	}
	if len(text) == 0 || text[0] != '{' {
		value.Shape = "other"
		return value
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(root, &object) != nil {
		return value
	}
	value.Shape = "object"
	value.FieldCount = len(object)
	raw, present := object["id"]
	value.ReturnedIDPresent = present
	if !present || len(raw) == 0 || raw[0] != '"' {
		return value
	}
	var id string
	if json.Unmarshal(raw, &id) == nil {
		value.ReturnedIDHash = digest(id)
		value.ReturnedIDMatchesRequest = id == requested
	}
	return value
}
